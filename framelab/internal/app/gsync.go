package app

// G-SYNC frame doubling (2.5.3), see platform/gsync.go. Checks reads it in both editions; 2.5E can
// switch the driver to "lowest multiplier" and restart the display driver, with Undo. 2.5 gives the
// steps with Orbmu2k's free NVIDIA G-SYNC Tweaks instead (Frame Lab 2.5 never changes Windows).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"framelab/internal/platform"
)

const gsyncUndoID = "gsync"

// what the driver key held before Frame Lab changed it (Undo puts exactly this back)
type gsyncWas struct {
	DriverKey string `json:"driver_key"`
	Instance  string `json:"instance"`
	Had       bool   `json:"had"` // false: the value was not there (driver default)
	Value     uint32 `json:"value"`
}

// ---------------------------------------------------------------- waiting for a driver restart

func gsyncPendingPath(data string) string { return filepath.Join(data, "gsync_pending.json") }

func gsyncMarkPending(data string) {
	os.MkdirAll(data, 0o755)
	os.WriteFile(gsyncPendingPath(data), mustJSON(map[string]time.Time{"at": time.Now()}), 0o644)
}

// gsyncPending: a change waits for the display driver to restart (a Windows restart counts)
func gsyncPending(data string) bool {
	b, err := os.ReadFile(gsyncPendingPath(data))
	if err != nil {
		return false
	}
	var m map[string]time.Time
	json.Unmarshal(b, &m)
	if bt := platform.BootTime(); !bt.IsZero() && m["at"].Before(bt) {
		os.Remove(gsyncPendingPath(data))
		return false
	}
	return true
}

// ---------------------------------------------------------------- the Checks row

// lastPacing: how frames reached the screen in the newest session that has it
func lastPacing(p Paths) (vrr bool, shown float64, ok bool) {
	ents, _ := os.ReadDir(p.Sessions)
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() > ents[j].Name() })
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(p.Sessions, e.Name(), "analysis.json"))
		if err != nil {
			continue
		}
		var a struct {
			Pacing *struct {
				Samples  int     `json:"samples"`
				ShownFps float64 `json:"shown_fps"`
				VRR      bool    `json:"vrr"`
			} `json:"pacing"`
		}
		if json.Unmarshal(b, &a) != nil || a.Pacing == nil || a.Pacing.Samples < 200 {
			continue
		}
		return a.Pacing.VRR, a.Pacing.ShownFps, true
	}
	return false, 0, false
}

const gsyncPage = "https://github.com/Orbmu2k/NvidiaGsyncTweaks"

func gsyncWhy(minHz float64) string {
	return fmt.Sprintf("Below a certain frame rate G-SYNC shows each frame twice so the screen stays inside its range (low framerate compensation). "+
		"By default the NVIDIA driver starts doing that well above the screen's real minimum, so a game running around that frame rate keeps flipping the screen "+
		"between single and double refresh: brightness flicker on OLED screens, small stutters on others. Set to the lowest multiplier, the driver only doubles "+
		"frames below the screen's VRR minimum (%.0f Hz). A game that hovers right at the minimum can still flicker there, and some OLED flicker happens anyway. "+
		"It is one hidden driver value (vrrFrameSplitBehavior, found by u/PykeFeed on Reddit; Orbmu2k's free NVIDIA G-SYNC Tweaks sets it and the driver's other hidden G-SYNC settings). "+
		"The driver reads it when it starts, so it needs a display driver restart: every screen goes black for a few seconds, so close the game and recording or streaming programs first. "+
		"It applies to every screen on the card, only matters with G-SYNC on, and a clean driver install puts NVIDIA's default back. Undo puts back exactly what was there.", minHz)
}

const gsyncDIY = "With the free NVIDIA G-SYNC Tweaks by Orbmu2k: close the game, start G-SYNC Tweaks, on its LFC / frame doubling page pick Lowest multiplier (recommended), then Apply & restart driver (every screen goes black for a few seconds). Its Reset puts NVIDIA's default back."

func gsyncRows(f platform.CheckFacts, vrrOff bool, p Paths) []CheckRow {
	ads := platform.NvAdapters()
	if len(ads) == 0 {
		return nil
	}
	var d platform.DisplayFact
	for _, x := range f.Displays {
		if x.Primary || d.Name == "" {
			d = x
		}
	}
	maxHz := float64(d.MaxHz)
	if maxHz < float64(d.Hz) {
		maxHz = float64(d.Hz)
	}
	minHz, minNote := float64(d.VRRMin), ""
	if minHz <= 0 {
		minHz, minNote = 48, " (usual; the screen doesn't say)"
	}
	fixed, custom := true, false
	for _, a := range ads {
		fixed = fixed && platform.SplitFixed(a.Split)
		custom = custom || platform.SplitCustom(a.Split)
	}
	ours := false
	for _, u := range undoList(p.Data) {
		ours = ours || u.ID == gsyncUndoID
	}
	name := "G-SYNC frame doubling"
	r := row("gsync", name, "", "info", "Info").why(gsyncWhy(minHz))
	r.UndoID = gsyncUndoID
	if gsyncPending(p.Data) {
		r.Detail = "changed: waiting for the display driver to restart (or a Windows restart)"
		r.Status, r.Label = "check", "Restart"
		r = r.act("gsync_restart", "Restart driver").diy("Restart Windows: the driver reads the value when it starts.", "", "")
		return []CheckRow{r}
	}
	if fixed {
		r.Detail = fmt.Sprintf("lowest multiplier: frames are only doubled below %.0f fps, the screen's VRR minimum%s", minHz, minNote)
		if !ours {
			r.Detail += " (set outside Frame Lab)"
		}
		r.Status, r.Label = "fine", "Fixed"
		return []CheckRow{r}
	}
	e, rel := platform.SplitPoints(ads[0].Split, maxHz, minHz)
	if e == 0 {
		r.Detail = fmt.Sprintf("this screen's range (%.0f-%.0f Hz) is too narrow for frame doubling", minHz, maxHz)
		return []CheckRow{r}
	}
	who := "NVIDIA default"
	if custom {
		who = "changed by another tool"
	}
	r.Detail = fmt.Sprintf("%s: doubles frames below %.0f fps (back above %.0f); this %.0f Hz screen can go down to %.0f%s", who, e, rel, maxHz, minHz, minNote)
	if ours {
		r.Detail += ". Frame Lab set the lowest multiplier before: a clean driver install puts the default back"
	}
	vrr, shown, have := lastPacing(p)
	switch {
	case vrrOff:
		r.Status, r.Label = "info", "G-SYNC off"
		r.Detail += ". Only matters with G-SYNC on"
	case e-minHz < 4: // e.g. 120 Hz: 50 fps instead of 48, little to gain
		r.Detail += fmt.Sprintf(". On this screen the fix only moves it by about %.0f fps", e-minHz)
	case have && vrr && shown >= e-5 && shown <= rel+6:
		r.Status, r.Label = "found", "Found"
		r.Detail += fmt.Sprintf(". Your last session: about %.0f fps with variable refresh, close to that point", shown)
	case maxHz >= 144:
		r.Status, r.Label = "check", "Check"
	}
	r = r.act("gsync_lfc", "Lowest multiplier").diy(gsyncDIY, "gsynctweaks", "Open G-SYNC Tweaks")
	return []CheckRow{r}
}

// ---------------------------------------------------------------- 2.5E: switch and restart

func (s *Server) gsyncFix() (string, error) {
	ads := platform.NvAdapters()
	if len(ads) == 0 {
		return "", errors.New("no NVIDIA graphics card found")
	}
	var was []gsyncWas
	for _, a := range ads {
		if platform.SplitFixed(a.Split) {
			continue
		}
		if err := platform.SetSplit(a.DriverKey, true, a.Split|platform.SplitLowestFit); err != nil {
			if len(was) > 0 {
				undoAdd(s.Paths.Data, UndoEntry{ID: gsyncUndoID, What: "G-SYNC frame doubling (NVIDIA driver value)", Kind: "gsync", Data: mustJSON(was)})
				gsyncMarkPending(s.Paths.Data)
			}
			return "", err
		}
		was = append(was, gsyncWas{DriverKey: a.DriverKey, Instance: a.Instance, Had: a.SplitSet, Value: a.Split})
	}
	if len(was) == 0 {
		return "The driver already uses the lowest multiplier.", nil
	}
	undoAdd(s.Paths.Data, UndoEntry{ID: gsyncUndoID, What: "G-SYNC frame doubling (NVIDIA driver value)", Kind: "gsync", Data: mustJSON(was)})
	gsyncMarkPending(s.Paths.Data)
	msg, err := s.gsyncRestart()
	if err != nil {
		return "Set. It takes effect after the display driver restarts: " + err.Error(), nil
	}
	return "Set to the lowest multiplier. " + msg, nil
}

// gsyncRestart restarts the NVIDIA cards' driver so it reads the value again (only with the game closed)
func (s *Server) gsyncRestart() (string, error) {
	if len(platform.FindGame()) > 0 {
		return "", errors.New("close the game first, then press Restart driver on this row (or restart Windows)")
	}
	ads := platform.NvAdapters()
	if len(ads) == 0 {
		return "", errors.New("no NVIDIA graphics card found")
	}
	reboot := false
	var errs []string
	for _, a := range ads {
		nr, err := platform.RestartAdapter(a.Instance)
		if err != nil {
			errs = append(errs, err.Error())
		}
		reboot = reboot || nr
	}
	if len(errs) > 0 {
		return "", errors.New("the driver didn't restart (" + strings.Join(errs, "; ") + "): restart Windows instead")
	}
	if reboot {
		return "Windows says it takes effect after a Windows restart.", nil
	}
	os.Remove(gsyncPendingPath(s.Paths.Data))
	return "The display driver restarted and uses it now.", nil
}

// undo: put the driver keys back exactly as they were (the driver reads them at its next restart)
func gsyncUndo(e UndoEntry) error {
	var was []gsyncWas
	if err := json.Unmarshal(e.Data, &was); err != nil {
		return err
	}
	for _, w := range was {
		if err := platform.SetSplit(w.DriverKey, w.Had, w.Value); err != nil {
			if strings.Contains(err.Error(), "not in this PC") {
				continue // the card is gone: nothing to put back
			}
			return err
		}
	}
	return nil
}
