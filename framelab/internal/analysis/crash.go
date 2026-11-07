package analysis

// The end of a session that ended in a crash: whether the graphics card hung (no frames, the
// card stuck "busy" at low power until Windows reset it) and the Advanced Crash Reporter report
// written at that time, if that mod is installed.

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const FileEvents = "events.json"

// SysEvent: an entry from Windows' System log (written by Frame Lab after the game closed).
type SysEvent struct {
	Time   time.Time `json:"time"`
	ID     string    `json:"id"`
	Source string    `json:"source"`
	Level  string    `json:"level"`
	Text   string    `json:"text"`
}

func readEvents(dir string) []SysEvent {
	b, err := os.ReadFile(filepath.Join(dir, FileEvents))
	if err != nil {
		return nil
	}
	var ev []SysEvent
	json.Unmarshal(b, &ev)
	return ev
}

type CrashInfo struct {
	Clock   string  `json:"clock"`             // local time the card hung or the game stopped
	T       float64 `json:"t"`                 // seconds on the timeline (-1 = after the recording)
	Hung    bool    `json:"gpu_hung"`          // the card stayed busy at low power with no frames
	HangS   float64 `json:"hang_s,omitempty"`  // how long it stayed like that
	Report  string  `json:"report,omitempty"`  // crash report file name
	Summary string  `json:"summary,omitempty"` // the report's own summary line
	Error   string  `json:"error,omitempty"`   // e.g. DXGI_ERROR_DEVICE_HUNG
	Place   string  `json:"place,omitempty"`   // the engine's last streaming position
	Shader  string  `json:"shader,omitempty"`  // what the card was running (from the report)
	Pass    string  `json:"pass,omitempty"`    // the render pass that never finished
	Device  string  `json:"device_state,omitempty"`
}

var reportName = regexp.MustCompile(`^(\d{8}-\d{6})-crash-.*\.txt$`)

// findCrash looks at the end of the recording for a GPU hang and at the crash reporter's folder.
func (d *data) findCrash(res *Result, t0 int64) *CrashInfo {
	if d.meta == nil || d.freq <= 0 {
		return nil
	}
	tsec := func(q int64) float64 { return float64(q-t0) / d.freq }
	clock := func(q int64) string { return time.UnixMilli(int64(d.qpcToUnix(q))).Format("15:04:05") }

	// last frame the game itself made
	var lastQ int64 = -1
	if d.pmc != nil && d.pmc.HasQpc {
		for i := len(d.pmc.Frames) - 1; i >= 0; i-- {
			f := d.pmc.Frames[i]
			if f.Qpc > 0 && !f.Generated {
				lastQ = f.Qpc
				break
			}
		}
	}

	ci := &CrashInfo{T: -1}
	// the card after the last frame: busy at low power while the game draws nothing
	if lastQ > 0 && len(d.samp) > 0 {
		var start, end int64 = -1, -1
		for _, s := range d.samp {
			if s.Qpc < lastQ-int64(d.freq) {
				continue
			}
			stuck := s.GpuUtilPct >= 95 && s.GameGpu3DPct < 5
			if stuck && start < 0 {
				start = s.Qpc
			}
			if start >= 0 {
				if stuck {
					end = s.Qpc
				} else {
					break
				}
			}
		}
		if start >= 0 && end > start && float64(end-start)/d.freq >= 3 {
			ci.Hung = true
			ci.HangS = math.Round(float64(end-start)/d.freq*10) / 10
			ci.T = math.Round(tsec(lastQ)*1000) / 1000
			ci.Clock = clock(lastQ)
		}
	}

	// the crash reporter's report from this session
	from := d.meta.Created.Add(-time.Minute)
	to := time.Now()
	if len(d.samp) > 0 {
		to = time.UnixMilli(int64(d.qpcToUnix(d.samp[len(d.samp)-1].Qpc))).Add(5 * time.Minute)
	}
	if rep := findReport(d.meta.Game, from, to); rep != "" {
		ci.Report = filepath.Base(rep)
		readReport(rep, ci)
		if ci.Clock == "" {
			if m := reportName.FindStringSubmatch(ci.Report); m != nil {
				if t, err := time.ParseInLocation("20060102-150405", m[1], time.Local); err == nil {
					ci.Clock = t.Format("15:04:05")
				}
			}
		}
	}
	if !ci.Hung && ci.Report == "" {
		return nil
	}

	// the long "frame" that ends the recording is the hang, not a stutter
	if ci.Hung {
		for i := range res.Spikes {
			s := &res.Spikes[i]
			if s.T+s.Ms/1000 >= ci.T-0.5 && s.T <= ci.T+1 && s.Ms >= 1000 {
				old := s.Group
				s.Cause = "the graphics card hung (the game crashed)"
				s.Group = "crash"
				s.Verdict = "The graphics card stopped responding here and the game crashed after Windows reset it. See the crash item at the top."
				moveCause(res, old, s.Ms)
			}
		}
	}
	return ci
}

// moveCause takes one spike out of its cause count and puts it under "crash".
func moveCause(res *Result, from string, ms float64) {
	n := 0
	for i := range res.Causes {
		n += res.Causes[i].Count
	}
	out := res.Causes[:0]
	for _, c := range res.Causes {
		if c.Group == from && c.Count > 0 {
			c.Count--
			c.TotalMs = math.Max(0, math.Round(c.TotalMs-ms))
		}
		if c.Count > 0 {
			out = append(out, c)
		}
	}
	res.Causes = out
	found := false
	for i := range res.Causes {
		if res.Causes[i].Group == "crash" {
			res.Causes[i].Count++
			res.Causes[i].TotalMs = math.Round(res.Causes[i].TotalMs + ms)
			found = true
		}
	}
	if !found {
		res.Causes = append(res.Causes, CauseCount{Cause: "the graphics card hung (the game crashed)", Group: "crash", Count: 1, TotalMs: math.Round(ms)})
	}
	if n > 0 {
		for i := range res.Causes {
			res.Causes[i].Pct = round2(100 * float64(res.Causes[i].Count) / float64(n))
		}
	}
	sort.SliceStable(res.Causes, func(i, j int) bool { return res.Causes[i].Count > res.Causes[j].Count })
}

// findReport: the newest Advanced Crash Reporter report written between from and to.
func findReport(game string, from, to time.Time) string {
	if game == "" {
		return ""
	}
	dir := filepath.Join(game, "bin", "x64", "plugins", "AdvancedCrashReporter", "reports")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	var bestT time.Time
	for _, e := range ents {
		m := reportName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		t, err := time.ParseInLocation("20060102-150405", m[1], time.Local)
		if err != nil || t.Before(from) || t.After(to) {
			continue
		}
		if best == "" || t.After(bestT) {
			best, bestT = filepath.Join(dir, e.Name()), t
		}
	}
	return best
}

// readReport picks the lines Frame Lab shows from a crash report.
func readReport(path string, ci *CrashInfo) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	val := func(line string) string {
		if i := strings.Index(line, ":"); i >= 0 {
			return strings.TrimSpace(line[i+1:])
		}
		return ""
	}
	for n := 0; sc.Scan() && n < 6000; n++ {
		line := sc.Text()
		t := strings.TrimSpace(line)
		switch {
		case ci.Summary == "" && strings.HasPrefix(t, "Summary "):
			ci.Summary = val(t)
		case ci.Error == "" && strings.HasPrefix(t, "Device removed"):
			v := val(t)
			if i := strings.Index(v, " "); i > 0 {
				v = strings.TrimSpace(v[i+1:])
			}
			ci.Error = v
		case ci.Device == "" && strings.HasPrefix(t, "Device state"):
			ci.Device = val(t)
		case ci.Shader == "" && strings.HasPrefix(t, "Active shader"):
			ci.Shader = val(t)
		case ci.Pass == "" && strings.HasPrefix(t, "Pass in progress"):
			ci.Pass = val(t)
		case ci.Place == "" && strings.HasPrefix(t, "Streaming/LastObserverPosition"):
			if i := strings.Index(t, "="); i >= 0 {
				ci.Place = strings.TrimSpace(t[i+1:])
			}
		}
	}
}
