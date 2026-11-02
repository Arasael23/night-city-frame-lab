package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"framelab/internal/analysis"
	"framelab/internal/modmap"
	"framelab/internal/platform"
)

// Capture states, as shown in the window.
const (
	StIdle      = "idle"       // nothing set up
	StArming    = "arming"     // putting the recorders into the game
	StWaiting   = "waiting"    // session ready, waiting for the game to start
	StRunning   = "running"    // (not used by sessions any more: the whole game run is recorded)
	StRecording = "recording"  // game running: everything recording
	StFinishing = "finishing"  // game closed: collecting, restoring, analysing
	StDone      = "done"       // finished; SessionID holds the result
	StError     = "error"
)

type Step struct {
	Time string `json:"time"`
	Text string `json:"text"`
	Kind string `json:"kind"` // ok, warn, err, info
}

type CaptureStatus struct {
	State     string `json:"state"`
	Title     string `json:"title"`
	Build     string `json:"build"`
	SessionID string `json:"session_id"`
	Steps     []Step `json:"steps"`
	Error     string `json:"error,omitempty"`
	Started   string `json:"started,omitempty"`
	Recording float64 `json:"recording_s"`
	Focus     bool    `json:"focus"`   // F11 part marked right now
	FocusS    float64 `json:"focus_s"` // seconds marked with F11 so far
	Layers    map[string]bool `json:"layers"`
}

// armed.json: what Frame Lab installed, so it can always be put back (also after a crash).
type armedFile struct {
	Session string    `json:"session"`
	Game    string    `json:"game"`
	Build   string    `json:"build"`
	Scripts bool      `json:"scripts"` // REDscript + CET time per mod switched on in HitchRecorder
	CET     bool      `json:"cet"`     // CET is in the build
	Hitch   bool      `json:"hitch"`   // HitchRecorder put in for this session
	Markers bool      `json:"markers"` // FrameLab.Markers.reds put in for this session
	Time    time.Time `json:"time"`
}

type Controller struct {
	mu       sync.Mutex
	paths    Paths
	settings *Settings
	st       CaptureStatus
	armed    *armedFile
	cancel   chan struct{}
	busy     bool

	// live (while the game runs)
	sampler   platform.Sampler
	winev     platform.WinEvents
	pmCmd     *pmProc
	gamePid   uint32
	hitchBin  string
	recStart  time.Time
	recTotal  time.Duration
	focusAt   time.Time
	focusTot  time.Duration
	analysing map[string]bool
	logMu     sync.Mutex
}

// diag appends to data\framelab.log (kept small) so problems on a real run can be looked at afterwards.
func (c *Controller) diag(text string) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	p := filepath.Join(c.paths.Data, "framelab.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 4<<20 {
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), text)
	f.Close()
}

func NewController(p Paths, s *Settings) *Controller {
	os.MkdirAll(p.Data, 0o755)
	os.MkdirAll(p.Sessions, 0o755)
	c := &Controller{paths: p, settings: s, st: CaptureStatus{State: StIdle}, analysing: map[string]bool{}}
	platform.StopWinEvents() // a Windows event session left running by a crash
	if b, err := os.ReadFile(c.armedPath()); err == nil {
		a := &armedFile{}
		if json.Unmarshal(b, a) == nil && a.Game != "" {
			c.armed = a
			c.st = CaptureStatus{State: StWaiting, Title: "", Build: a.Build, SessionID: filepath.Base(a.Session),
				Layers: map[string]bool{"redscript": a.Hitch && a.Scripts, "cet": a.Hitch && a.Scripts && a.CET, "hitch": a.Hitch}}
			if m, err := analysis.ReadMeta(a.Session); err == nil {
				c.st.Title = m.Title
			}
			c.step("warn", "The recorders from an earlier session are still in "+a.Build+" (the game or PC may have crashed). Start the game to record, or press Cancel and remove to take them out.")
			c.cancel = make(chan struct{})
			go c.watch(c.cancel)
		}
	}
	return c
}

func (c *Controller) armedPath() string { return filepath.Join(c.paths.Data, "armed.json") }

func (c *Controller) step(kind, text string) {
	go c.diag("[" + kind + "] " + text)
	c.st.Steps = append(c.st.Steps, Step{Time: time.Now().Format("15:04:05"), Text: text, Kind: kind})
	if len(c.st.Steps) > 60 {
		c.st.Steps = c.st.Steps[len(c.st.Steps)-60:]
	}
}

func (c *Controller) logf(kind, f string, a ...any) {
	c.mu.Lock()
	c.step(kind, fmt.Sprintf(f, a...))
	c.mu.Unlock()
}

func (c *Controller) Status() CaptureStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.st
	s.Steps = append([]Step(nil), c.st.Steps...)
	rec := c.recTotal
	if c.st.State == StRecording && !c.recStart.IsZero() {
		rec += time.Since(c.recStart)
	}
	s.Recording = rec.Seconds()
	f := c.focusTot
	if c.st.Focus && !c.focusAt.IsZero() {
		f += time.Since(c.focusAt)
	}
	s.FocusS = f.Seconds()
	return s
}

func (c *Controller) Armed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.armed != nil
}

var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sessionFolderName(title string) string {
	t := strings.TrimSpace(unsafeChars.ReplaceAllString(title, " "))
	if t == "" {
		t = "Capture"
	}
	if len(t) > 60 {
		t = t[:60]
	}
	return time.Now().Format("2006-01-02 1504") + " " + t
}

// gameRunningFor reports whether Cyberpunk is running from the given build folder.
func gameRunningFor(game string) (platform.GameProc, bool) {
	for _, g := range platform.FindGame() {
		if g.Path == "" || strings.HasPrefix(strings.ToLower(g.Path), strings.ToLower(filepath.Clean(game))+string(os.PathSeparator)) {
			return g, true
		}
	}
	return platform.GameProc{}, false
}

// Arm prepares a capture: puts HitchRecorder (session settings) and the markers into the build, then waits for the game.
func (c *Controller) Arm(title string, buildIdx int) error {
	cfg := c.settings.Get()
	if buildIdx < 0 || buildIdx >= len(cfg.Builds) {
		return errors.New("unknown game build")
	}
	b := cfg.Builds[buildIdx]
	c.mu.Lock()
	if c.busy || (c.st.State != StIdle && c.st.State != StDone && c.st.State != StError) {
		c.mu.Unlock()
		return errors.New("a capture is already set up")
	}
	if len(platform.FindGame()) > 0 {
		c.mu.Unlock()
		return errors.New("close the game first: the recorders can only be put in while it is closed")
	}
	if _, err := os.Stat(filepath.Join(b.Path, "bin", "x64", "Cyberpunk2077.exe")); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("no Cyberpunk2077.exe in %s", b.Path)
	}
	dir := filepath.Join(c.paths.Sessions, sessionFolderName(title))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.mu.Unlock()
		return err
	}
	c.st = CaptureStatus{State: StArming, Title: title, Build: b.Name, SessionID: filepath.Base(dir), Started: time.Now().Format("15:04"),
		Layers: map[string]bool{}}
	c.recTotal, c.recStart, c.focusTot, c.focusAt = 0, time.Time{}, 0, time.Time{}
	c.busy = true
	c.mu.Unlock()

	meta := &analysis.Meta{ID: filepath.Base(dir), Title: title, Build: b.Name, Game: b.Path, Created: time.Now(),
		QpcFreq: platform.QPCFreq(), SpikeMs: cfg.SpikeMs}
	gi := platform.GPU()
	meta.GPU, meta.GpuVendor, meta.GpuSource, meta.GpuReads = gi.Name, gi.Vendor, gi.Source, gi.Reads
	analysis.WriteMeta(dir, meta)

	go func() {
		released := false
		defer func() {
			if !released {
				c.mu.Lock()
				c.busy = false
				c.mu.Unlock()
			}
		}()
		comp := FindComponents(c.paths, cfg)
		a := &armedFile{Session: dir, Game: b.Path, Build: b.Name, Time: time.Now(), Scripts: cfg.UseScripts, CET: HasCET(b.Path)}
		// HitchRecorder + loading-screen markers go in for this one session only
		hitch, markers, err := InstallSessionParts(comp, b.Path, cfg.UseScripts)
		a.Hitch, a.Markers = hitch, markers
		switch {
		case err != nil:
			c.logf("err", "Putting HitchRecorder in failed: %v", err)
		case !hitch:
			c.logf("warn", "HitchRecorder is missing from components\\HitchRecorder: no engine layer this session.")
		default:
			c.logf("ok", "HitchRecorder %s put into %s for this session.", HitchStatus(b.Path).Version, b.Name)
		}
		if markers {
			c.logf("ok", "Loading-screen markers put in (they need CET; written to its gamelog.log).")
		} else if err == nil {
			c.logf("warn", "Loading-screen markers missing from components\\FrameLabMarkers: loading screens will count as play.")
		}
		// which Experiments settings are on for this session (and the standby clear, if switched on)
		if exp := experimentsOn(c.paths.Data, b.Path); len(exp) > 0 {
			if m, err := analysis.ReadMeta(dir); err == nil {
				m.Experiments = exp
				analysis.WriteMeta(dir, m)
			}
			c.logf("info", "Experiments on for this session: %s.", strings.Join(exp, ", "))
		}
		if standbyAuto(c.paths.Data) {
			if err := platform.PurgeStandby(); err != nil {
				c.logf("warn", "Clearing standby memory failed: %v", err)
			} else {
				c.logf("ok", "Standby memory cleared (Experiments).")
			}
		}
		// written now, so a crash before the game starts still gets everything taken out again
		if ab, e := json.MarshalIndent(a, "", " "); e == nil {
			os.WriteFile(c.armedPath(), ab, 0o644)
		}
		switch {
		case !hitch:
		case !cfg.UseScripts:
			c.logf("info", "Script time per mod is switched off in Settings.")
		case a.CET:
			c.logf("ok", "REDscript and CET time per mod: measured by HitchRecorder in the parts you mark with F11.")
		default:
			c.logf("ok", "REDscript time per mod: measured by HitchRecorder in the parts you mark with F11 (no CET in %s, so no CET layer).", b.Name)
		}
		ab, _ := json.MarshalIndent(a, "", " ")
		os.WriteFile(c.armedPath(), ab, 0o644)
		c.mu.Lock()
		c.armed = a
		c.st.State = StWaiting
		c.st.Layers["redscript"], c.st.Layers["cet"] = a.Hitch && a.Scripts, a.Hitch && a.Scripts && a.CET
		c.st.Layers["hitch"] = a.Hitch
		c.step("info", "Ready. Start the game and play: everything records by itself until you quit. Press F11 at a spot that stutters to mark it (script time per mod is measured only in the marked parts), F11 again to end the mark. Quit the game when done.")
		c.cancel = make(chan struct{})
		cancel := c.cancel
		c.busy = false // set-up done: Cancel works while waiting for the game (1.0 stayed busy here)
		released = true
		c.mu.Unlock()
		c.watch(cancel)
	}()
	return nil
}

// watch follows the game from start to exit.
func (c *Controller) watch(cancel chan struct{}) {
	c.mu.Lock()
	a := c.armed
	c.mu.Unlock()
	if a == nil {
		return
	}
	// 4 ticks a second so the F11 beep comes right away; the game check runs once a second
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	running := false
	n := 0
	for {
		select {
		case <-cancel:
			return
		case <-tick.C:
		}
		n++
		if running && n%4 != 0 {
			c.onGameTick()
			continue
		}
		g, ok := gameRunningFor(a.Game)
		switch {
		case ok && !running:
			running = true
			c.onGameStart(a, g)
		case ok && running:
			c.onGameTick()
		case !ok && running:
			c.onGameExit(a)
			return
		}
	}
}

func (c *Controller) onGameStart(a *armedFile, g platform.GameProc) {
	cfg := c.settings.Get()
	comp := FindComponents(c.paths, cfg)
	meta, _ := analysis.ReadMeta(a.Session)
	if meta == nil {
		meta = &analysis.Meta{ID: filepath.Base(a.Session), Build: a.Build, Game: a.Game, Created: time.Now(), QpcFreq: platform.QPCFreq(), SpikeMs: cfg.SpikeMs}
	}
	meta.AnchorQpc = platform.QPC()
	meta.AnchorMs = float64(time.Now().UnixNano()) / 1e6
	meta.QpcFreq = platform.QPCFreq()
	analysis.WriteMeta(a.Session, meta)

	c.mu.Lock()
	c.gamePid = g.Pid
	c.st.State = StRecording
	c.recStart = time.Now()
	c.step("ok", fmt.Sprintf("Game started (pid %d): recording.", g.Pid))
	c.mu.Unlock()

	if s, err := platform.StartSampler(g.Pid, filepath.Join(a.Session, analysis.FileSamples)); err == nil {
		c.mu.Lock()
		c.sampler = s
		c.st.Layers["samples"] = true
		c.mu.Unlock()
		c.logf("ok", "GPU health + PC counters recording.")
	} else {
		c.logf("warn", "PC counters not started: %v", err)
	}
	if !cfg.NoWinEvents {
		if e, err := platform.StartWinEvents(g.Pid, filepath.Join(a.Session, analysis.FileWindows)); err == nil {
			c.mu.Lock()
			c.winev = e
			c.st.Layers["windows"] = true
			c.mu.Unlock()
			c.logf("ok", "Windows events recording (drivers, paging, disk).")
		} else {
			c.logf("warn", "Windows events not recorded: %v", err)
		}
	}
	if comp.PresentMon != "" {
		if !platform.IsAdmin() {
			c.logf("warn", "PresentMon needs Frame Lab to run as administrator; frame/GPU timing skipped.")
		} else if cmd, err := startPresentMon(comp.PresentMon, filepath.Join(a.Session, analysis.FilePresentMon), cfg.TrackFrameType); err == nil {
			c.mu.Lock()
			c.pmCmd = cmd
			c.st.Layers["presentmon"] = true
			c.mu.Unlock()
			c.logf("ok", "PresentMon recording frames and GPU time (%s).", filepath.Base(comp.PresentMon))
		} else {
			c.logf("warn", "PresentMon did not start: %v", err)
		}
	} else {
		c.logf("warn", "PresentMon not found (CapFrameX's copy or components\\PresentMon): no GPU time per frame.")
	}
}

type pmProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func startPresentMon(exe, out string, frameType bool) (*pmProc, error) {
	args := []string{"--process_name", "Cyberpunk2077.exe", "--output_file", out, "--qpc_time", "--no_console_stats",
		"--terminate_on_proc_exit", "--stop_existing_session", "--session_name", "NightCityFrameLab"}
	try := func(extra ...string) (*pmProc, error) {
		cmd := exec.Command(exe, append(args, extra...)...)
		platform.HideWindow(cmd)
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf
		cmd.Stdout = io.Discard
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		p := &pmProc{cmd: cmd, done: make(chan struct{})}
		var werr error
		go func() { werr = cmd.Wait(); close(p.done) }()
		select {
		case <-p.done: // exited at once: bad arguments or no rights
			msg := strings.TrimSpace(errBuf.String())
			if msg == "" && werr != nil {
				msg = werr.Error()
			}
			return nil, errors.New(firstLine(msg))
		case <-time.After(2500 * time.Millisecond):
			return p, nil
		}
	}
	if frameType {
		if cmd, err := try("--track_frame_type"); err == nil {
			return cmd, nil
		}
	}
	return try()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func (c *Controller) onGameTick() {
	hs := platform.ReadHitchState()
	c.mu.Lock()
	defer c.mu.Unlock()
	if hs.Valid {
		if hs.BinPath != "" {
			c.hitchBin = hs.BinPath
		}
		// session mode: the shared 'capturing' flag is the F11 mark
		if hs.Capturing && !c.st.Focus {
			c.st.Focus = true
			c.focusAt = time.Now()
			c.step("ok", "F11: marking this part.")
			go platform.Beep(true)
		} else if !hs.Capturing && c.st.Focus {
			c.st.Focus = false
			c.focusTot += time.Since(c.focusAt)
			c.focusAt = time.Time{}
			c.step("info", "F11: mark ended. Recording goes on until you quit.")
			go platform.Beep(false)
		}
	}
}

func (c *Controller) onGameExit(a *armedFile) {
	c.mu.Lock()
	if c.st.State == StRecording {
		c.recTotal += time.Since(c.recStart)
		c.recStart = time.Time{}
	}
	if c.st.Focus {
		c.focusTot += time.Since(c.focusAt)
		c.focusAt = time.Time{}
		c.st.Focus = false
	}
	c.st.State = StFinishing
	c.step("info", "Game closed. Collecting everything ...")
	s, pmCmd, bin, we := c.sampler, c.pmCmd, c.hitchBin, c.winev
	c.sampler, c.pmCmd, c.winev = nil, nil, nil
	c.mu.Unlock()
	if s != nil {
		s.Stop()
	}
	if we != nil {
		st := we.Stop()
		var n uint64
		for _, v := range st.Events {
			n += v
		}
		if st.Error != "" {
			c.logf("warn", "Windows events: %d events, %d lost, error: %s", n, st.EventsLost, st.Error)
		} else {
			c.logf("ok", "Windows events: %d events, %d lost.", n, st.EventsLost)
		}
	}
	if pmCmd != nil {
		// PresentMon ends on its own when the game exits; give it a moment to write its last rows
		select {
		case <-pmCmd.done:
		case <-time.After(10 * time.Second):
			pmCmd.cmd.Process.Kill()
		}
	}
	c.finish(a, bin)
}

// finish collects all layers into the session folder, puts the game back as it was and analyses.
func (c *Controller) finish(a *armedFile, hitchBin string) {
	meta, _ := analysis.ReadMeta(a.Session)

	// HitchRecorder recording of this game run
	if hitchBin == "" {
		hitchBin = newestFile(filepath.Join(hitchDir(a.Game), "recordings"), ".bin", a.Time)
	}
	if hitchBin != "" {
		if err := copyFile(hitchBin, filepath.Join(a.Session, analysis.FileHitch)); err == nil {
			copyFile(strings.TrimSuffix(hitchBin, ".bin")+".log", filepath.Join(a.Session, analysis.FileHitchLog))
			copyFile(strings.TrimSuffix(hitchBin, ".bin")+".owners.txt", filepath.Join(a.Session, analysis.FileHitchOwners))
			c.logf("ok", "Engine recording copied (%s).", filepath.Base(hitchBin))
		} else {
			c.logf("warn", "Could not copy the engine recording: %v", err)
		}
	}
	// loading-screen markers from CET's log
	if a.Markers {
		cetLog := filepath.Join(a.Game, "bin", "x64", "plugins", "cyber_engine_tweaks", "gamelog.log")
		if marks, err := analysis.ParseCETLog(cetLog, a.Time.Add(-10*time.Second)); err != nil {
			c.logf("warn", "No loading-screen markers (CET's gamelog.log: %v).", err)
		} else {
			analysis.WriteMarks(a.Session, marks)
			n := 0
			for _, m := range marks {
				if m.Text == "loading screen on" {
					n++
				}
			}
			c.logf("ok", "Loading-screen markers read (%d loading screen(s)).", n)
		}
	}
	// screen resets (the game re-applying full screen / rebuilding its screen buffers) from ReShade's and CET's logs
	if ev, found := analysis.ReadDisplayLogs(a.Game, a.Time.Add(-10*time.Second)); found {
		analysis.WriteDisplay(a.Session, ev)
		c.logf("ok", "Screen resets read from ReShade's / CET's log (%d).", len(ev))
	}
	c.restore(a)

	if meta != nil {
		meta.Finished = true
		_, err1 := os.Stat(filepath.Join(a.Session, analysis.FileHitch))
		_, err2 := os.Stat(filepath.Join(a.Session, analysis.FilePresentMon))
		_, err3 := os.Stat(filepath.Join(a.Session, analysis.FileSamples))
		meta.Layers = analysis.LayerInfo{Hitch: err1 == nil, PresentMon: err2 == nil, Samples: err3 == nil, Scripts: err1 == nil && a.Scripts}
		if v := bundledHitchVersion(c.paths); v != "" && a.Hitch {
			meta.Layers.HitchVer = v
		}
		analysis.WriteMeta(a.Session, meta)
	}
	c.logf("info", "Analysing ...")
	id := filepath.Base(a.Session)
	if _, err := c.Analyse(id); err != nil {
		c.logf("err", "Analysis: %v", err)
	} else {
		c.logf("ok", "Done. Open the report.")
	}
	c.mu.Lock()
	c.st.State = StDone
	c.mu.Unlock()
}

// restore takes HitchRecorder and the markers out of the build again and forgets the arm.
func (c *Controller) restore(a *armedFile) {
	ok := true
	if a.Hitch || a.Markers || SessionPartsLeft(a.Game) {
		if err := RemoveSessionParts(a.Game); err != nil {
			ok = false
			c.logf("err", "Taking HitchRecorder / the markers out of %s: %v", a.Build, err)
		} else {
			c.logf("ok", "HitchRecorder and the markers taken out of %s.", a.Build)
		}
	}
	if ok {
		os.Remove(c.armedPath())
		c.mu.Lock()
		c.armed = nil
		c.mu.Unlock()
	}
}

// Disarm cancels a capture that has not started (or recovers from an old one): restores everything.
func (c *Controller) Disarm() error {
	c.mu.Lock()
	a := c.armed
	if c.busy {
		c.mu.Unlock()
		return errors.New("busy, try again in a moment")
	}
	if a == nil {
		c.mu.Unlock()
		return errors.New("nothing is set up")
	}
	if c.st.State == StRunning || c.st.State == StRecording || c.st.State == StFinishing {
		c.mu.Unlock()
		return errors.New("the game is running: quit it and the session finishes and cleans up by itself")
	}
	if len(platform.FindGame()) > 0 {
		c.mu.Unlock()
		return errors.New("close the game first")
	}
	if c.cancel != nil {
		close(c.cancel)
		c.cancel = nil
	}
	c.busy = true
	c.st.State = StFinishing
	c.step("info", "Taking the recorders out of the game ...")
	c.mu.Unlock()
	go func() {
		c.restore(a)
		// drop an empty session folder
		if ents, _ := os.ReadDir(a.Session); len(ents) <= 1 {
			os.RemoveAll(a.Session)
		}
		c.mu.Lock()
		c.busy = false
		if c.armed == nil {
			c.st.State = StIdle
			c.step("ok", "Everything is back as it was.")
		} else {
			c.st.State = StError
			c.st.Error = "Restore did not complete; see the steps above."
		}
		c.mu.Unlock()
	}()
	return nil
}

// Shutdown is called when the window closes.
func (c *Controller) Shutdown() {
	c.mu.Lock()
	a := c.armed
	state := c.st.State
	we := c.winev
	c.winev = nil
	c.mu.Unlock()
	if we != nil {
		we.Stop()
	}
	platform.StopWinEvents() // never leave a kernel session of ours running
	if a != nil && (state == StWaiting || state == StIdle) && len(platform.FindGame()) == 0 {
		c.restore(a)
	}
}

// Reset clears a finished/failed capture panel.
func (c *Controller) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.armed == nil && !c.busy {
		c.st = CaptureStatus{State: StIdle}
	}
}

// ---------------------------------------------------------------- sessions

type SessionInfo struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Build     string  `json:"build"`
	Created   string  `json:"created"`
	Finished  bool    `json:"finished"`
	Analysed  bool    `json:"analysed"`
	DurationS float64 `json:"duration_s"`
	Spikes    int     `json:"spikes"`
	MedianMs  float64 `json:"median_ms"`
	Low1Fps   float64 `json:"low1_fps"`
	Sort      int64   `json:"-"`
}

func (c *Controller) Sessions() []SessionInfo {
	ents, _ := os.ReadDir(c.paths.Sessions)
	out := []SessionInfo{} // [] not null when there are none yet
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(c.paths.Sessions, e.Name())
		m, err := analysis.ReadMeta(dir)
		if err != nil {
			continue
		}
		si := SessionInfo{ID: e.Name(), Title: m.Title, Build: m.Build, Created: m.Created.Format("2 Jan 2006 15:04"), Finished: m.Finished,
			Sort: m.Created.Unix()}
		if b, err := os.ReadFile(filepath.Join(dir, analysis.FileAnalysis)); err == nil {
			var r struct {
				KPI analysis.KPI `json:"kpi"`
			}
			if json.Unmarshal(b, &r) == nil {
				si.Analysed = true
				si.DurationS, si.Spikes, si.MedianMs, si.Low1Fps = r.KPI.DurationS, r.KPI.Spikes, r.KPI.MedianMs, r.KPI.Low1Fps
			}
		}
		out = append(out, si)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sort > out[j].Sort })
	return out
}

func (c *Controller) SessionDir(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", errors.New("bad session id")
	}
	dir := filepath.Join(c.paths.Sessions, id)
	if _, err := os.Stat(filepath.Join(dir, analysis.FileMeta)); err != nil {
		return "", errors.New("no such session")
	}
	return dir, nil
}

// Analyse (re)builds analysis.json for a session.
func (c *Controller) Analyse(id string) (*analysis.Result, error) {
	dir, err := c.SessionDir(id)
	if err != nil {
		return nil, err
	}
	meta, _ := analysis.ReadMeta(dir)
	cfg := c.settings.Get()
	env := analysis.Env{Names: modmap.NamesFile(filepath.Join(c.paths.Data, "names.txt.gz"))}
	if meta != nil && meta.Game != "" {
		env.Mods = modmap.ScanBuild(meta.Game)
	}
	if cfg.Library != "" {
		env.Library = modmap.LoadLibrary(cfg.Library, filepath.Join(c.paths.Data, "library_index.json"))
		env.Library.AddManifest(filepath.Join(c.paths.Data, "library_manifest.json"))
	}
	if meta != nil && cfg.SpikeMs > 0 && meta.SpikeMs != cfg.SpikeMs {
		meta.SpikeMs = cfg.SpikeMs
		analysis.WriteMeta(dir, meta)
	}
	return analysis.Run(dir, env)
}

// ---------------------------------------------------------------- file helpers

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func newestFile(dir, ext string, after time.Time) string {
	ents, _ := os.ReadDir(dir)
	best, bestT := "", after
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ext) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(bestT) {
			best, bestT = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best
}

func newestDir(dir, prefix string, after time.Time) string {
	ents, _ := os.ReadDir(dir)
	best, bestT := "", after.Add(-time.Minute)
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(bestT) {
			best, bestT = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best
}
