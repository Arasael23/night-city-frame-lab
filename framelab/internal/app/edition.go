package app

// Two editions from one source (2.5):
//   Frame Lab 2.5   records, reads and advises. It never changes Windows, the game's settings or mod
//                   files; the only things it puts in a game are its own recording parts (the markers
//                   mod and HitchRecorder), which it takes out again. Every fix is a do-it-yourself step,
//                   except the easy one: moving programs that don't need the game's card to the
//                   integrated graphics (Windows' per-program graphics preference for this user, the
//                   same switch as Settings > Display > Graphics, with an exact Undo).
//   Frame Lab 2.5E  (Experimental) the same plus Experiments and Optimise: Windows tweaks, game options
//                   tried one at a time, script fixes, merges and patches. Built with -tags experimental.
// Undo stays in both, so changes an earlier Frame Lab version made can always be put back.

import (
	"sort"
	"strings"

	"framelab/internal/analysis"
)

// Edition is the name shown in the window and on Nexus.
func Edition() string {
	if Experimental {
		return "2.5E"
	}
	return "2.5"
}

// writerRoute: API routes that change something outside Frame Lab's own folder. Only 2.5E serves them.
func writerRoute(path string) bool {
	switch path {
	case "optimise/fix", "optimise/undo", "conflicts/fix", "conflicts/merge", "workbench/install", "vram/lighter/apply", "experiments/set", "experiments/run",
		"gameopts/set", "gameopts/mod":
		return true
	}
	return strings.HasPrefix(path, "optimise/option/") || strings.HasPrefix(path, "optimise/schedule")
}

// writerCheck: Checks fixes that change Windows or game files (2.5E). 2.5 shows the steps instead.
func writerCheck(id string) bool {
	switch id {
	case "fth_clear", "debug_off", "us_writable", "bcd_clear", "cpusets_clear":
		return true
	}
	return false
}

const onlyE = "Frame Lab 2.5 doesn't change anything on your PC: the steps to do this yourself are shown with it. Frame Lab 2.5E (Experimental) can try to do it for you."

func init() { analysis.Experimental = Experimental }

// Changes: what Frame Lab changed on this PC (Experiments, Checks fixes, programs moved to the integrated
// graphics). Frame Lab 2.5 makes none itself, but an earlier version or 2.5E may have: 2.5 still shows
// them and can put everything back.
type Changes struct {
	Experiments []string `json:"experiments"`
	Fixes       []string `json:"fixes"`
	Programs    []string `json:"programs"`
}

func changesMade(data string) Changes {
	c := Changes{Experiments: []string{}, Fixes: []string{}, Programs: []string{}}
	expMu.Lock()
	f := loadExp(data)
	expMu.Unlock()
	for id := range f.Applied {
		c.Experiments = append(c.Experiments, expName(id))
	}
	if f.StandbyAuto {
		c.Experiments = append(c.Experiments, "Clear standby memory before each session")
	}
	if f.QuietAuto {
		c.Experiments = append(c.Experiments, "Quiet session")
	}
	for _, u := range undoList(data) {
		c.Fixes = append(c.Fixes, u.What)
	}
	gpuPrefMu.Lock()
	g := loadGpuPrefFile(data)
	gpuPrefMu.Unlock()
	for _, v := range g.Applied {
		c.Programs = append(c.Programs, v.Name)
	}
	sort.Strings(c.Experiments)
	sort.Strings(c.Programs)
	return c
}

// putEverythingBack: every change in Changes goes back to what was there before.
func putEverythingBack(data string) (int, []string) {
	n, errs := undoEverything(data)
	gpuPrefMu.Lock()
	g := loadGpuPrefFile(data)
	gpuPrefMu.Unlock()
	for path, v := range g.Applied {
		if err := setGpuApp(data, path, false); err != nil {
			errs = append(errs, v.Name+": "+err.Error())
			continue
		}
		n++
	}
	return n, errs
}
