package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"framelab/internal/conflicts"
	"framelab/internal/modmap"
	"framelab/internal/optimise"
)

// Optimise tab (2.5), step 1: reads a build's mod files, recognises the mods and lists what needs
// attention. Nothing in the game folder is changed. Runs in the background and keeps the last result
// in data\optimise\<build>.json. With "Check Nexus for mod updates" on in Settings, it also asks
// Nexus Mods for the current version of every recognised mod (only their Nexus ids are sent).

var (
	optMu    sync.Mutex
	optState = map[string]string{} // build name -> "checking" / "" / "error: ..."
)

func (s *Server) optimisePath(build string) string {
	return filepath.Join(s.Paths.Data, "optimise", strings.TrimSpace(reUnsafe.ReplaceAllString(build, "_"))+".json")
}

func optimiseIndexPath(p Paths) string {
	return filepath.Join(p.Components, "Optimise", "mods-index.json.gz")
}

func (s *Server) optimiseIndex() *optimise.Index {
	x, err := optimise.LoadIndex(optimiseIndexPath(s.Paths))
	if err != nil {
		return nil
	}
	return x
}

func (s *Server) checkOptimise(i int) error {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	b := cfg.Builds[i]
	exe := filepath.Join(b.Path, "bin", "x64", "Cyberpunk2077.exe")
	if _, err := os.Stat(exe); err != nil {
		return errors.New("Cyberpunk2077.exe isn't in " + b.Path + `\bin\x64: check the build's folder in Settings`)
	}
	optMu.Lock()
	if optState[b.Name] == "checking" {
		optMu.Unlock()
		return nil
	}
	optState[b.Name] = "checking"
	optMu.Unlock()
	go func() {
		o := optimise.Options{Build: b.Name, Game: b.Path, GameVersion: gameVersion(exe), Index: s.optimiseIndex(), Corpus: s.corpus()}
		if lib := libraryFor(s.Paths, cfg, b.Path); lib != nil {
			o.Library = lib
		}
		r := optimise.Scan(o)
		if r.Files > 0 {
			// the conflict check (archives, REDscript, TweakXL) belongs in the same list of things to do;
			// its own report is saved too, so the Conflicts tab shows the same state
			co := conflicts.Options{Build: b.Name, Game: b.Path, Library: o.Library, Names: modmap.NamesFile(filepath.Join(s.Paths.Data, "names.txt.gz"))}
			co.Loaded, co.Session = s.loadedInLastSession(b.Path)
			cr := conflicts.Check(co)
			if bs, err := json.Marshal(cr); err == nil {
				cp := s.conflictPath(b.Name)
				os.MkdirAll(filepath.Dir(cp), 0o755)
				os.WriteFile(cp, bs, 0o644)
			}
			r.Findings = append(r.Findings, optimise.ConflictFindings(cr)...)
		}
		if cfg.LiveUpdates && r.Recognised > 0 {
			optimise.CheckLive(r, nil)
		}
		optimise.SetActions(r, cfg.LiveUpdates)
		state := ""
		p := s.optimisePath(b.Name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if bs, err := json.Marshal(r); err != nil {
			state = "error: " + err.Error()
		} else if err := os.WriteFile(p, bs, 0o644); err != nil {
			state = "error: " + err.Error()
		}
		n := map[string]int{}
		for _, f := range r.Findings {
			n[f.Severity]++
		}
		s.Ctl.diag("optimise check " + b.Name + ": " + itoa(r.Files) + " files, " + itoa(len(r.Mods)) + " mods (" + itoa(r.Recognised) + " recognised), " +
			itoa(n["high"]) + " high / " + itoa(n["medium"]) + " medium / " + itoa(n["info"]) + " info")
		if state == "" {
			s.schedMark(b.Name, "analysed")
		}
		optMu.Lock()
		optState[b.Name] = state
		optMu.Unlock()
	}()
	return nil
}

// optimiseReport: the saved report of a build (raw JSON) and the check's state
func (s *Server) optimiseReport(i int) (map[string]any, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return nil, errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	b := cfg.Builds[i]
	optMu.Lock()
	state := optState[b.Name]
	optMu.Unlock()
	out := map[string]any{"build": b.Name, "state": state, "live_updates": cfg.LiveUpdates, "index": fileExists(optimiseIndexPath(s.Paths))}
	sched, err := s.scheduleStatus(i)
	if err != nil {
		return nil, err
	}
	out["schedule"] = sched
	conflictMu.Lock()
	out["conflict_state"] = conflictState[b.Name]
	conflictMu.Unlock()
	out["options"] = s.optionsStatus(b, sched)
	// the report belongs to the open part of the schedule: before the build is measured there is nothing to act on
	if sched.Step == "fix" || sched.Step == "retest" || sched.Step == "decide" {
		if bs, err := os.ReadFile(s.optimisePath(b.Name)); err == nil {
			out["report"] = json.RawMessage(bs)
		}
	}
	fixes := s.buildFixes(b.Name)
	scriptsBroken(b.Path, fixes)
	out["fixes"] = fixes
	out["recipes"] = optimise.MatchRecipes(b.Path)
	out["likely"] = s.likelyCosts(b)
	out["script_fns"] = s.scriptFns(b)
	return out, nil
}
