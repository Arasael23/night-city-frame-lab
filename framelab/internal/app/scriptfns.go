package app

// Optimise stage 2 (2.5E): the costliest mod REDscript functions of a build, from its newest recording
// that has them (HitchRecorder 1.3 hitch.fn.txt), each with the file and line it is in.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"framelab/internal/analysis"
	"framelab/internal/gamefs"
	"framelab/internal/optimise"
)

// ScriptFnsOut: what the Optimise page shows.
type ScriptFnsOut struct {
	Session string            `json:"session"`
	Title   string            `json:"title"`
	Created string            `json:"created"`
	Test    bool              `json:"test"` // a test recording (scripts timed for the whole run)
	Seconds float64           `json:"seconds"`
	Fns     []analysis.FnCost `json:"fns"`
}

var (
	fnCacheMu sync.Mutex
	fnCache   = map[string]struct {
		at  time.Time
		out *ScriptFnsOut
	}{}
)

func (s *Server) scriptFns(b BuildCfg) *ScriptFnsOut {
	for _, si := range s.Ctl.Sessions() {
		if !si.Analysed || si.Build != b.Name {
			continue
		}
		dir, err := s.Ctl.SessionDir(si.ID)
		if err != nil {
			continue
		}
		bs, err := os.ReadFile(filepath.Join(dir, analysis.FileAnalysis))
		if err != nil {
			continue
		}
		var r struct {
			ScriptFns *analysis.FnReport `json:"script_fns"`
		}
		if json.Unmarshal(bs, &r) != nil || r.ScriptFns == nil || len(r.ScriptFns.Fns) == 0 {
			continue
		}
		fnCacheMu.Lock()
		c, ok := fnCache[b.Name+"|"+si.ID]
		fnCacheMu.Unlock()
		if ok && time.Since(c.at) < time.Minute {
			return c.out
		}
		m, _ := analysis.ReadMeta(dir)
		out := &ScriptFnsOut{Session: si.ID, Title: si.Title, Created: si.Created, Seconds: r.ScriptFns.Seconds, Fns: r.ScriptFns.Fns}
		if m != nil {
			out.Test = m.Test
		}
		locateFns(b.Path, out.Fns)
		fnCacheMu.Lock()
		fnCache[b.Name+"|"+si.ID] = struct {
			at  time.Time
			out *ScriptFnsOut
		}{time.Now(), out}
		fnCacheMu.Unlock()
		return out
	}
	return nil
}

// locateFns fills in where each function is: r6\scripts of the build (with MO2: as the game sees it).
// HitchRecorder names the mod by its folder (or file) under r6\scripts; that folder is searched first.
func locateFns(game string, fns []analysis.FnCost) {
	if len(fns) == 0 {
		return
	}
	root := filepath.Join(game, "r6", "scripts")
	type hit struct {
		rel string
		f   optimise.RedsFunc
	}
	byName := map[string][]hit{} // function name -> where
	gamefs.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".reds") {
			return nil
		}
		b, err := gamefs.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(game, p)
		for _, f := range optimise.ParseReds(string(b)) {
			byName[f.Name] = append(byName[f.Name], hit{filepath.ToSlash(rel), f})
		}
		return nil
	})
	for i := range fns {
		c := &fns[i]
		label := c.Fn
		name := label
		if k := strings.LastIndex(label, "::"); k >= 0 {
			name = label[k+2:]
		}
		var best *hit
		for _, h := range byName[name] {
			h := h
			if _, ok := optimise.FindRedsFunc([]optimise.RedsFunc{h.f}, label); !ok {
				continue
			}
			inMod := strings.HasPrefix(strings.ToLower(h.rel), strings.ToLower("r6/scripts/"+c.Mod))
			if best == nil || inMod {
				best = &h
				if inMod {
					break
				}
			}
		}
		if best != nil {
			c.File, c.Line = best.rel, best.f.Line
			c.Wraps = best.f.Kind == "wrap" || best.f.Kind == "replace"
		}
	}
}
