package app

// Experiments: Windows settings people change to make games run better, each one switched on by
// itself and undone exactly; every session remembers which were on, so two sessions in Compare
// show whether a setting helped on this PC.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"framelab/internal/platform"
)

type ExpDef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	What   string `json:"what"`   // what it changes, plainly
	Expect string `json:"expect"` // what to expect / when it helps
	Risk   string `json:"risk,omitempty"`
	When   string `json:"when"`   // when it takes effect
	Action bool   `json:"action"` // one-shot (run now), not on/off
	Warn   bool   `json:"warn"`   // shown with a warning colour
}

var expDefs = []ExpDef{
	{ID: "power", Name: "High performance power plan",
		What:   "Switches Windows to the High performance power plan (CPU cores don't park or clock down between frames).",
		Expect: "Can smooth frame times on CPUs that drop clocks quickly. Costs some power at idle.",
		When:   "at once"},
	{ID: "priority", Name: "Game at high CPU and disk priority",
		What:   "Tells Windows to start Cyberpunk2077.exe with high CPU priority and high disk priority (Image File Execution Options).",
		Expect: "Helps when other programs compete for the CPU or disk while you play. Little effect on an otherwise idle PC.",
		When:   "next time the game starts"},
	{ID: "pstate", Name: "NVIDIA: keep GPU clocks at maximum",
		What:   "Sets DisableDynamicPstate = 1 on the NVIDIA card, so it never steps down to lower clocks.",
		Expect: "Most cards already hold their boost clock under load (check the GPU clock chart of a session first); this mostly adds heat and power at idle.",
		Risk:   "Higher idle power and temperature.",
		When:   "after a restart"},
	{ID: "tdrdelay", Name: "Longer GPU timeout (TdrDelay 20 s)",
		What:   "Gives the GPU 20 seconds instead of 2 before Windows treats it as hung and resets the driver.",
		Expect: "Avoids driver resets during very long GPU work (heavy path tracing, loading). No effect on fps.",
		When:   "after a restart"},
	{ID: "tdroff", Name: "Turn off GPU hang recovery (TdrLevel 0)", Warn: true,
		What:   "Windows stops watching for GPU hangs at all.",
		Expect: "No fps gain. Only stops the 'display driver stopped responding' reset.",
		Risk:   "If the GPU really hangs, the whole PC freezes and needs a hard reset instead of the screen flickering back.",
		When:   "after a restart"},
	{ID: "defender", Name: "Defender exclusion for the game", Warn: true,
		What:   "Microsoft Defender stops scanning the game folder and Cyberpunk2077.exe.",
		Expect: "Can cut stutter while the game streams files from disk (Defender checks files as they are read).",
		Risk:   "Anything placed in the game folder isn't scanned (mods you download are still scanned in Downloads).",
		When:   "at once"},
	{ID: "cfg", Name: "Turn off Control Flow Guard for the game", Warn: true,
		What:   "Switches off the CFG exploit protection for Cyberpunk2077.exe only (Windows Security > App & browser control > Exploit protection, set per program).",
		Expect: "CFG adds a small check to indirect calls; some people measure a little more CPU headroom without it.",
		Risk:   "Removes a protection against exploits for this one program. (DEP can't be turned off for 64-bit games, so it isn't offered.)",
		When:   "next time the game starts"},
	{ID: "standbyauto", Name: "Clear standby memory before each session",
		What:   "When you start a capture, Frame Lab empties Windows' standby list (cached file data kept in RAM) first, like ISLC does.",
		Expect: "Helps only when free RAM runs low during play; with plenty of RAM it changes nothing.",
		When:   "at the start of each capture"},
	{ID: "standby", Name: "Clear standby memory now", Action: true,
		What:   "Empties Windows' standby list once.",
		Expect: "Same as above, once.",
		When:   "at once"},
	{ID: "shadercache", Name: "Clear the shader caches", Action: true, Warn: true,
		What:   "Deletes the graphics driver's and DirectX's shader cache files (NVIDIA DXCache, AMD DxCache / DxcCache, Intel ShaderCache, Windows D3DSCache). The cache size setting is left alone.",
		Expect: "Only useful after a driver update or a broken cache. If your sessions show no stutter from shader builds, leave it.",
		Risk:   "The next session stutters while every shader is built again.",
		When:   "at once (close the game first)"},
}

type expSaved struct {
	Prev map[string]string `json:"prev"`
	At   time.Time         `json:"at"`
	Game string            `json:"game"`
}

type expFile struct {
	Applied     map[string]expSaved `json:"applied"`
	StandbyAuto bool                `json:"standby_auto"`
}

var expMu sync.Mutex

func expPath(data string) string { return filepath.Join(data, "experiments.json") }

func loadExp(data string) *expFile {
	f := &expFile{Applied: map[string]expSaved{}}
	if b, err := os.ReadFile(expPath(data)); err == nil {
		json.Unmarshal(b, f)
	}
	if f.Applied == nil {
		f.Applied = map[string]expSaved{}
	}
	return f
}

func saveExp(data string, f *expFile) error {
	b, _ := json.MarshalIndent(f, "", " ")
	return os.WriteFile(expPath(data), b, 0o644)
}

type ExpState struct {
	ExpDef
	On     bool   `json:"on"`
	Detail string `json:"detail"`
	OK     bool   `json:"ok"`   // can be used on this PC
	Ours   bool   `json:"ours"` // switched on by Frame Lab (undo restores exactly what was there)
	Since  string `json:"since,omitempty"`
}

// experimentStates reads every setting (a few seconds: two of them ask PowerShell).
func experimentStates(data, game string) []ExpState {
	expMu.Lock()
	f := loadExp(data)
	expMu.Unlock()
	out := make([]ExpState, len(expDefs))
	var wg sync.WaitGroup
	for i, d := range expDefs {
		wg.Add(1)
		go func(i int, d ExpDef) {
			defer wg.Done()
			st := ExpState{ExpDef: d}
			st.On, st.Detail, st.OK = platform.ExpRead(d.ID, game)
			if d.ID == "standbyauto" {
				st.On = f.StandbyAuto
			}
			if s, ok := f.Applied[d.ID]; ok {
				st.Ours = true
				st.Since = s.At.Format("2 Jan 15:04")
			}
			out[i] = st
		}(i, d)
	}
	wg.Wait()
	// NVIDIA-only settings stay out of the list on PCs without an NVIDIA card (unless Frame Lab
	// switched one on before, so it can still be undone)
	kept := out[:0]
	for _, st := range out {
		if st.ID == "pstate" && !st.OK && !st.Ours {
			continue
		}
		kept = append(kept, st)
	}
	return kept
}

func setExperiment(data, game, id string, on bool) error {
	expMu.Lock()
	defer expMu.Unlock()
	f := loadExp(data)
	if id == "standbyauto" {
		f.StandbyAuto = on
		return saveExp(data, f)
	}
	if len(platform.FindGame()) > 0 && (id == "priority" || id == "cfg") {
		return errors.New("close the game first")
	}
	if on {
		if _, done := f.Applied[id]; done {
			return nil
		}
		prev, err := platform.ExpOn(id, game)
		if err != nil {
			return err
		}
		f.Applied[id] = expSaved{Prev: prev, At: time.Now(), Game: game}
		return saveExp(data, f)
	}
	// off: put back exactly what was there, or (switched on outside Frame Lab) Windows' default
	s, ok := f.Applied[id]
	prev := s.Prev
	if !ok {
		prev = map[string]string{"scheme": "381b4222-f694-41f0-9685-ff5bb260df2e"} // power: Balanced
		s.Game = game
	}
	if err := platform.ExpRestore(id, s.Game, prev); err != nil {
		return err
	}
	delete(f.Applied, id)
	return saveExp(data, f)
}

func runExperiment(data, game, id string) (string, error) {
	if id == "shadercache" && len(platform.FindGame()) > 0 {
		return "", errors.New("close the game first")
	}
	return platform.ExpRun(id, game)
}

// experimentsOn: the names of the settings that are on, for the session's notes
func experimentsOn(data, game string) []string {
	var out []string
	for _, s := range experimentStates(data, game) {
		if s.On && !s.Action {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

func standbyAuto(data string) bool {
	expMu.Lock()
	defer expMu.Unlock()
	return loadExp(data).StandbyAuto
}

func (s *Server) buildPath(i int) string {
	cfg := s.Settings.Get()
	if i >= 0 && i < len(cfg.Builds) {
		return cfg.Builds[i].Path
	}
	if len(cfg.Builds) > 0 {
		return cfg.Builds[0].Path
	}
	return ""
}
