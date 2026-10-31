package app

// Programs on the graphics card (VRAM tab): which programs hold graphics memory on which card right
// now, and moving the ones that don't need the main card to the integrated graphics. It changes the
// same per-program Windows setting as Settings > System > Display > Graphics ("Power saving").
// What was there before is kept in data\gpu_prefs.json, so Undo puts back exactly that.

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"framelab/internal/analysis"
	"framelab/internal/platform"
)

type gpuPrefSaved struct {
	Was  string    `json:"was"` // the value's data before, or "absent"
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

type gpuPrefFile struct {
	Applied map[string]gpuPrefSaved `json:"applied"` // key: program path as written to the registry
}

var gpuPrefMu sync.Mutex

func gpuPrefPath(data string) string { return filepath.Join(data, "gpu_prefs.json") }

func loadGpuPrefFile(data string) *gpuPrefFile {
	f := &gpuPrefFile{Applied: map[string]gpuPrefSaved{}}
	if b, err := os.ReadFile(gpuPrefPath(data)); err == nil {
		json.Unmarshal(b, f)
	}
	if f.Applied == nil {
		f.Applied = map[string]gpuPrefSaved{}
	}
	return f
}

func saveGpuPrefFile(data string, f *gpuPrefFile) error {
	b, _ := json.MarshalIndent(f, "", " ")
	return os.WriteFile(gpuPrefPath(data), b, 0o644)
}

type GpuAppRow struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	Procs      int     `json:"procs"`
	Running    bool    `json:"running"`
	Exists     bool    `json:"exists"`
	MainMB     float64 `json:"main_mb"`   // on the main card
	IgpuMB     float64 `json:"igpu_mb"`   // on the integrated graphics
	SharedMB   float64 `json:"shared_mb"` // system RAM the program's graphics use
	Pref       string  `json:"pref"`      // "1" power saving, "2" high performance, "0" Windows decides, "" not set
	Ours       bool    `json:"ours"`      // set by Frame Lab (Undo puts back what was there)
	Since      string  `json:"since,omitempty"`
	Class      string  `json:"class"`
	Reason     string  `json:"reason"`
	Can        bool    `json:"can"`                // Frame Lab can set this one
	Versioned  bool    `json:"versioned"`          // its folder is named after its version: set it again after it updates
	Restart    bool    `json:"restart"`            // set to the integrated graphics but still on the main card: needs a restart
	Shared     bool    `json:"shared"`             // shows more than everything but the game together: its number includes the game's memory (overlay / desktop)
	IgpuShared bool    `json:"igpu_shared"`        // shows more on the integrated graphics than they hold in all: not its own (memory shared across cards)
	PrefGpu    string  `json:"pref_gpu,omitempty"` // Windows 11 "specific GPU" choice: that GPU's name
	ToIgpu     bool    `json:"to_igpu"`            // Windows is set to run it on the integrated graphics (power saving or that GPU chosen)
}

type GpuAppsReport struct {
	Adapters    []platform.GpuAdapter `json:"adapters"`
	Main        *platform.GpuAdapter  `json:"main,omitempty"`
	Integrated  *platform.GpuAdapter  `json:"integrated,omitempty"`
	Drivers     []string              `json:"drivers,omitempty"` // display drivers Windows knows (to tell "switched off" from "none")
	Apps        []GpuAppRow           `json:"apps"`
	GameMB      float64               `json:"game_mb"`
	GameRunning bool                  `json:"game_running"`
	OthersMB    float64               `json:"others_mb"`  // everything but the game on the main card (programs, Windows, driver)
	MovableMB   float64               `json:"movable_mb"` // the part the suggested programs hold
	Suggested   int                   `json:"suggested"`  // programs "Move all suggested" would move
	GameWarn    string                `json:"game_warn,omitempty"`
	Error       string                `json:"error,omitempty"`
}

func gpuAppsReport(data string) GpuAppsReport {
	rep := GpuAppsReport{Apps: []GpuAppRow{}}
	ads, procs, err := platform.GpuSnapshot()
	if err != nil {
		rep.Error = err.Error()
	}
	rep.Adapters = ads
	for i := range ads {
		a := &ads[i]
		if a.Integrated {
			if rep.Integrated == nil {
				rep.Integrated = a
			}
			continue
		}
		if rep.Main == nil || a.MemMB > rep.Main.MemMB || (a.MemMB == rep.Main.MemMB && a.UsedMB > rep.Main.UsedMB) {
			rep.Main = a
		}
	}
	if rep.Integrated == nil {
		rep.Drivers = platform.DisplayDrivers()
	}
	prefs, perr := platform.GpuPrefs()
	if perr != nil && rep.Error == "" {
		rep.Error = "could not read the Windows graphics settings: " + perr.Error()
	}
	prefOf := func(path string) string { // the value's data
		for k, v := range prefs {
			if strings.EqualFold(k, path) {
				return v
			}
		}
		return ""
	}
	adapterName := func(vendor uint32) string {
		for _, a := range ads {
			if a.Vendor == vendor {
				return a.Name
			}
		}
		return ""
	}
	for k, v := range prefs {
		if strings.EqualFold(winBase(k), "Cyberpunk2077.exe") && platform.GpuPrefOf(v) == "1" {
			rep.GameWarn = "Windows is set to run the game (" + k + ") on the power-saving graphics. Set it back in Settings > System > Display > Graphics: High performance, or let Windows decide."
		}
	}
	gpuPrefMu.Lock()
	saved := loadGpuPrefFile(data)
	gpuPrefMu.Unlock()

	rows := map[string]*GpuAppRow{}
	key := func(name, path string) string {
		if path != "" {
			return strings.ToLower(path)
		}
		return "name:" + strings.ToLower(name)
	}
	pids := map[string]map[uint32]bool{}
	for _, p := range procs {
		k := key(p.Name, p.Path)
		r := rows[k]
		if r == nil {
			r = &GpuAppRow{Name: p.Name, Path: p.Path, Running: true, Exists: true}
			rows[k] = r
			pids[k] = map[uint32]bool{}
		}
		pids[k][p.Pid] = true
		switch {
		case rep.Main != nil && p.Luid == rep.Main.Luid:
			r.MainMB += p.DedicatedMB
		case rep.Integrated != nil && p.Luid == rep.Integrated.Luid:
			r.IgpuMB += p.DedicatedMB
		}
		r.SharedMB += p.SharedMB
	}
	for path, s := range saved.Applied { // set by Frame Lab, not running now: still listed for Undo
		k := strings.ToLower(path)
		if rows[k] == nil {
			rows[k] = &GpuAppRow{Name: s.Name, Path: path, Exists: platform.ProgramExists(path)}
		}
	}
	for k, r := range rows {
		r.Procs = len(pids[k])
		r.Class, r.Reason = analysis.GpuAppClass(r.Name, r.Path)
		if strings.EqualFold(r.Name, "Cyberpunk2077.exe") {
			rep.GameMB += r.MainMB
		} else {
			rep.OthersMB += r.MainMB
		}
		if r.Path != "" {
			data := prefOf(r.Path)
			r.Pref = platform.GpuPrefOf(data)
			r.Versioned = analysis.GpuPathVersioned(r.Path)
			if r.Pref == platform.GpuPrefSpecific { // Windows 11: a chosen GPU
				v := platform.GpuPrefVendor(data)
				r.PrefGpu = adapterName(v)
				r.ToIgpu = rep.Integrated != nil && v == rep.Integrated.Vendor
			}
		}
		r.ToIgpu = r.ToIgpu || r.Pref == "1"
		for p, s := range saved.Applied {
			if strings.EqualFold(p, r.Path) {
				r.Ours, r.Since = true, s.At.Format("2 Jan 15:04")
			}
		}
		r.Can = r.Path != "" && (r.Class == "suggest" || r.Class == "other")
	}
	// everything but the game on the main card: the card's own count minus the game. The programs'
	// own numbers can't simply be added up: Windows counts the game's memory again for programs that
	// hold a handle to it (overlays, the desktop compositor), so they show more than the card has.
	if rep.Main != nil && rep.Main.UsedMB > 0 {
		rep.OthersMB = math.Max(0, rep.Main.UsedMB-rep.GameMB)
	}
	for _, r := range rows {
		// more than everything but the game on that card together (or than the integrated graphics
		// hold at all): not its own memory
		if r.MainMB > rep.OthersMB+50 && !strings.EqualFold(r.Name, "Cyberpunk2077.exe") {
			r.Shared = true
		}
		if rep.Integrated != nil && r.IgpuMB > rep.Integrated.UsedMB+50 {
			r.IgpuShared = true
		}
		r.Restart = r.Running && r.ToIgpu && rep.Integrated != nil && r.IgpuMB < 5 && r.MainMB >= 20 && !r.Shared
		if r.Class == "suggest" && r.Can && r.Running && !r.ToIgpu && r.MainMB >= 20 && !r.Shared {
			rep.Suggested++
			rep.MovableMB += r.MainMB
		}
	}
	rep.GameRunning = rep.GameMB > 0
	for _, r := range rows {
		if !r.Running || r.MainMB >= 5 || r.IgpuMB >= 5 || r.Ours || r.Pref != "" {
			rep.Apps = append(rep.Apps, *r)
		}
	}
	own := func(r GpuAppRow) float64 { // what is its own (numbers that include another program's memory count as 0)
		v := 0.0
		if !r.Shared {
			v += r.MainMB
		}
		if !r.IgpuShared {
			v += r.IgpuMB
		}
		return v
	}
	sort.Slice(rep.Apps, func(i, j int) bool {
		a, b := rep.Apps[i], rep.Apps[j]
		if a.Running != b.Running {
			return a.Running
		}
		if own(a) != own(b) {
			return own(a) > own(b)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return rep
}

// setGpuApp moves a program to the integrated graphics (on) or puts back what was there (off).
func setGpuApp(data, path string, on bool) error {
	gpuPrefMu.Lock()
	defer gpuPrefMu.Unlock()
	f := loadGpuPrefFile(data)
	prefs, err := platform.GpuPrefs()
	if err != nil {
		return err
	}
	name, cur := "", ""
	for k, v := range prefs {
		if strings.EqualFold(k, path) {
			name, cur = k, v
		}
	}
	if !on {
		for p, s := range f.Applied {
			if !strings.EqualFold(p, path) {
				continue
			}
			was := s.Was
			if was == "absent" {
				was = ""
			}
			target := p
			if name != "" {
				target = name
			}
			if err := platform.SetGpuPref(target, was); err != nil {
				return err
			}
			delete(f.Applied, p)
			return saveGpuPrefFile(data, f)
		}
		// set outside Frame Lab: take the choice out, keep the rest of the value
		if name == "" {
			return nil
		}
		return platform.SetGpuPref(name, platform.GpuPrefData(cur, ""))
	}
	if !strings.HasSuffix(strings.ToLower(path), ".exe") || len(path) < 4 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return errors.New("not a program path")
	}
	if !platform.ProgramExists(path) {
		return errors.New("the program isn't there any more: " + path)
	}
	cl, why := analysis.GpuAppClass(winBase(path), path)
	if cl != "suggest" && cl != "other" {
		return errors.New(winBase(path) + ": " + why)
	}
	ads, _, _ := platform.GpuSnapshot()
	igpu := false
	for _, a := range ads {
		igpu = igpu || a.Integrated
	}
	if !igpu {
		return errors.New("Windows has no integrated graphics switched on, so there is nothing to move the program to (see How to below)")
	}
	target := path
	if name != "" {
		target = name
	}
	if err := platform.SetGpuPref(target, platform.GpuPrefData(cur, "1")); err != nil {
		return err
	}
	done := false
	for p := range f.Applied {
		done = done || strings.EqualFold(p, path)
	}
	if !done { // keep the first "before", so Undo always goes back to how it was before Frame Lab
		was := cur
		if name == "" {
			was = "absent"
		}
		f.Applied[target] = gpuPrefSaved{Was: was, Name: winBase(path), At: time.Now()}
	}
	return saveGpuPrefFile(data, f)
}

// moveSuggested moves every running suggested program that holds 20 MB+ on the main card.
func moveSuggested(data string) ([]string, error) {
	rep := gpuAppsReport(data)
	if rep.Integrated == nil {
		return nil, errors.New("Windows has no integrated graphics switched on (see How to below)")
	}
	var moved []string
	for _, r := range rep.Apps {
		if r.Class == "suggest" && r.Can && r.Running && !r.ToIgpu && r.MainMB >= 20 && !r.Shared {
			if err := setGpuApp(data, r.Path, true); err != nil {
				return moved, errors.New(r.Name + ": " + err.Error())
			}
			moved = append(moved, r.Name)
		}
	}
	return moved, nil
}

// winBase: the file name of a Windows path (also when Frame Lab is built for testing elsewhere)
func winBase(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}
