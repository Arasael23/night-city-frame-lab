package app

// Optimise step 2 (2.5): fixes applied on this PC, each with the original file kept and an exact undo.
// Nothing is uploaded or shared. The ledger is data\optimise\fixes.json, the originals are in
// data\optimise\backups\<fix id>\.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"framelab/internal/gamefs"
	"framelab/internal/optimise"
	"framelab/internal/platform"
)

type FixRecord struct {
	ID      string    `json:"id"`
	Build   string    `json:"build"`
	Mod     string    `json:"mod"`
	File    string    `json:"file"` // as the scan shows it (relative to the game folder)
	Real    string    `json:"real"` // the file that was changed (with MO2: inside that mod's folder)
	Line    int       `json:"line"`
	Hz      int       `json:"hz,omitempty"`
	Kind    string    `json:"kind"`   // "lua_throttle"
	Before  string    `json:"before"` // sha256 of the file before the fix
	After   string    `json:"after"`  // and after
	Backup  string    `json:"backup"`
	Applied string    `json:"applied"`
	Undone  string    `json:"undone,omitempty"`
	Gone    string    `json:"gone,omitempty"`   // the mod's file changed since (updated or reinstalled): the fix went with it
	Recipe  string    `json:"recipe,omitempty"` // kind "recipe": which one (optimise/recipes.json)
	What    string    `json:"what,omitempty"`
	Files   []FixFile `json:"files,omitempty"` // kind "recipe": every file it changed or added
	Broke   string    `json:"broke,omitempty"` // not saved: the last REDscript compile named this fix's file in an error
}

// FixFile: one file of a recipe fix.
type FixFile struct {
	Path   string `json:"path"` // relative to the game folder
	Real   string `json:"real"`
	Before string `json:"before,omitempty"` // "" = Frame Lab added the file (undo removes it)
	After  string `json:"after"`
	Backup string `json:"backup,omitempty"`
	// big files (texture archives, hundreds of MB): moved instead of read into memory; the report
	// checks them by size and time, Undo by their hash
	Big   bool  `json:"big,omitempty"`
	Size  int64 `json:"size,omitempty"`
	MTime int64 `json:"mtime,omitempty"`
}

var fixMu sync.Mutex

func (s *Server) fixesPath() string { return filepath.Join(s.Paths.Data, "optimise", "fixes.json") }

func (s *Server) loadFixes() []FixRecord {
	var out []FixRecord
	if b, err := os.ReadFile(s.fixesPath()); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (s *Server) saveFixes(f []FixRecord) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(s.fixesPath()), 0o755)
	tmp := s.fixesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.fixesPath())
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func gameRunning(game string) bool {
	for _, g := range platform.FindGame() {
		if g.Path != "" && strings.HasPrefix(strings.ToLower(filepath.Clean(g.Path)), strings.ToLower(filepath.Clean(game))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// buildFixes: the fixes of one build, newest first, each checked against the file as it is now
func (s *Server) buildFixes(build string) []FixRecord {
	fixMu.Lock()
	defer fixMu.Unlock()
	all := s.loadFixes()
	var out []FixRecord
	for i := len(all) - 1; i >= 0; i-- {
		f := all[i]
		if f.Build != build {
			continue
		}
		if f.Undone == "" && f.Gone == "" {
			if len(f.Files) > 0 {
				for _, x := range f.Files {
					if x.After == "" { // switched off: it has to stay away
						if x.Big && addedAt(f.Files, x.Real) != "" {
							continue // a texture swap put a file of the same name in its place
						}
						if _, err := os.Stat(x.Real); err == nil {
							f.Gone = "the file is back (the mod was updated or reinstalled)"
							break
						}
						continue
					}
					if x.Big {
						if fi, err := os.Stat(x.Real); err != nil || fi.Size() != x.Size || fi.ModTime().UnixNano() != x.MTime {
							f.Gone = "a file changed since (the mod was updated or reinstalled)"
							break
						}
						continue
					}
					if b, err := os.ReadFile(x.Real); err != nil || sha(b) != x.After {
						f.Gone = "a file changed since (the mod was updated or reinstalled)"
						break
					}
				}
			} else if b, err := os.ReadFile(f.Real); err != nil || sha(b) != f.After {
				f.Gone = "the file changed since (the mod was updated or reinstalled)"
			}
		}
		out = append(out, f)
	}
	return out
}

func (s *Server) applyFix(i int, kind, mod, file string, line, hz int) (*FixRecord, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return nil, errors.New("no game build picked yet")
	}
	b := cfg.Builds[i]
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first: CET reads its mods when the game starts")
	}
	rel := filepath.FromSlash(file)
	low := strings.ToLower(filepath.ToSlash(rel))
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return nil, errors.New("not a mod file")
	}
	switch kind {
	case "lua_throttle", "lua_throttle_call", "lua_hubguard":
		if !strings.HasPrefix(low, "bin/x64/plugins/cyber_engine_tweaks/mods/") || !strings.HasSuffix(low, ".lua") {
			return nil, errors.New("not a CET mod's Lua file")
		}
	case "reds_arraycontains":
		if !strings.HasPrefix(low, "r6/scripts/") || !strings.HasSuffix(low, ".reds") {
			return nil, errors.New("not a REDscript file")
		}
	default:
		return nil, errors.New("unknown fix")
	}
	real := gamefs.Real(filepath.Join(b.Path, rel))
	orig, err := os.ReadFile(real)
	if err != nil {
		return nil, err
	}
	patched, err := optimise.ApplyRule(string(orig), kind, line, hz)
	if err != nil {
		return nil, err
	}
	fixMu.Lock()
	defer fixMu.Unlock()
	all := s.loadFixes()
	id := time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", len(all)+1)
	bdir := filepath.Join(s.Paths.Data, "optimise", "backups", id)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return nil, err
	}
	backup := filepath.Join(bdir, filepath.Base(real))
	if err := os.WriteFile(backup, orig, 0o644); err != nil {
		return nil, fmt.Errorf("could not keep the original: %w", err)
	}
	st, _ := os.Stat(real)
	mode := os.FileMode(0o644)
	if st != nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(real, []byte(patched), mode); err != nil {
		return nil, fmt.Errorf("could not change %s: %w", real, err)
	}
	rec := FixRecord{ID: id, Build: b.Name, Mod: mod, File: file, Real: real, Line: line, Hz: hz, Kind: kind,
		Before: sha(orig), After: sha([]byte(patched)), Backup: backup, Applied: time.Now().Format("2006-01-02 15:04")}
	all = append(all, rec)
	if err := s.saveFixes(all); err != nil {
		os.WriteFile(real, orig, mode) // no record, no change
		return nil, err
	}
	s.Ctl.diag(fmt.Sprintf("optimise fix %s (%s): %s line %d of %s (original kept: %s)", id, kind, mod, line, file, backup))
	return &rec, nil
}

func (s *Server) undoFix(id string) (*FixRecord, error) {
	fixMu.Lock()
	defer fixMu.Unlock()
	all := s.loadFixes()
	for k := range all {
		f := &all[k]
		if f.ID != id {
			continue
		}
		if f.Undone != "" {
			return nil, errors.New("already undone")
		}
		for _, b := range s.Settings.Get().Builds {
			if b.Name == f.Build && gameRunning(b.Path) {
				return nil, errors.New("close the game first")
			}
		}
		if x := newerOrderFix(all, f); x != nil {
			return nil, fmt.Errorf("undo the newer load-order change first (%s): it wrote modlist.txt after this one", x.Mod)
		}
		if len(f.Files) > 0 {
			if err := undoFiles(f.Files); err != nil {
				if strings.HasPrefix(err.Error(), "gone:") {
					f.Gone = strings.TrimPrefix(err.Error(), "gone: ")
					s.saveFixes(all)
					return nil, errors.New(f.Gone + ": nothing to undo")
				}
				return nil, err
			}
			f.Undone = time.Now().Format("2006-01-02 15:04")
			s.Ctl.diag("optimise fix " + id + " undone: " + f.Mod + " has its original files again")
			rec := *f
			return &rec, s.saveFixes(all)
		}
		cur, err := os.ReadFile(f.Real)
		switch {
		case err == nil && sha(cur) == f.After:
			orig, err := os.ReadFile(f.Backup)
			if err != nil || sha(orig) != f.Before {
				return nil, errors.New("the kept original is missing or changed: nothing was touched")
			}
			if err := os.WriteFile(f.Real, orig, 0o644); err != nil {
				return nil, err
			}
		case err == nil && sha(cur) == f.Before:
			// already the original (put back by hand)
		default:
			f.Gone = "the file changed since (the mod was updated or reinstalled), so the fix is already gone with it"
			s.saveFixes(all)
			return nil, errors.New(f.Gone + ": nothing to undo")
		}
		f.Undone = time.Now().Format("2006-01-02 15:04")
		s.Ctl.diag("optimise fix " + id + " undone: " + f.Real + " is the original again")
		rec := *f
		return &rec, s.saveFixes(all)
	}
	return nil, errors.New("no such fix")
}

// applyRecipe puts one recipe (optimise/recipes.json) into a build: every file or none.
func (s *Server) applyRecipe(i int, id string) (*FixRecord, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return nil, errors.New("no game build picked yet")
	}
	b := cfg.Builds[i]
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first: the game and CET read mod files when it starts")
	}
	r, ok := optimise.RecipeByID(id)
	if !ok {
		return nil, errors.New("unknown fix")
	}
	type job struct {
		f       optimise.RecipeFile
		real    string
		orig    []byte
		out     []byte
		mode    os.FileMode
		existed bool
	}
	var jobs []job
	for _, f := range r.Files {
		rel := filepath.FromSlash(f.Path)
		if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
			return nil, errors.New("the recipe is damaged")
		}
		j := job{f: f, real: gamefs.Real(filepath.Join(b.Path, rel)), mode: 0o644}
		if st, err := os.Stat(j.real); err == nil {
			j.existed, j.mode = true, st.Mode().Perm()
			if j.orig, err = os.ReadFile(j.real); err != nil {
				return nil, err
			}
		}
		out, err := optimise.ApplyOps(j.orig, f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w. Nothing was changed", f.Path, err)
		}
		j.out = out
		jobs = append(jobs, j)
	}
	fixMu.Lock()
	defer fixMu.Unlock()
	all := s.loadFixes()
	fid := time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", len(all)+1)
	bdir := filepath.Join(s.Paths.Data, "optimise", "backups", fid)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return nil, err
	}
	rec := FixRecord{ID: fid, Build: b.Name, Mod: r.Mod, Kind: "recipe", Recipe: r.ID, What: r.What, Applied: time.Now().Format("2006-01-02 15:04")}
	var written []job
	rollback := func() {
		for _, w := range written {
			if w.existed {
				os.WriteFile(w.real, w.orig, w.mode)
			} else {
				os.Remove(w.real)
			}
		}
	}
	for n, j := range jobs {
		ff := FixFile{Path: j.f.Path, Real: j.real, Before: j.f.Before, After: j.f.After}
		if j.existed {
			ff.Backup = filepath.Join(bdir, fmt.Sprintf("%d-%s", n+1, filepath.Base(j.real)))
			if err := os.WriteFile(ff.Backup, j.orig, 0o644); err != nil {
				rollback()
				return nil, fmt.Errorf("could not keep the original: %w", err)
			}
		} else {
			os.MkdirAll(filepath.Dir(j.real), 0o755)
		}
		if err := os.WriteFile(j.real, j.out, j.mode); err != nil {
			rollback()
			return nil, fmt.Errorf("could not change %s: %w. Nothing was changed", j.real, err)
		}
		written = append(written, j)
		rec.Files = append(rec.Files, ff)
	}
	rec.File, rec.Real, rec.Before, rec.After, rec.Backup = rec.Files[0].Path, rec.Files[0].Real, rec.Files[0].Before, rec.Files[0].After, rec.Files[0].Backup
	all = append(all, rec)
	if err := s.saveFixes(all); err != nil {
		rollback() // no record, no change
		return nil, err
	}
	s.Ctl.diag(fmt.Sprintf("optimise fix %s: recipe %s for %s (%d files, originals kept in %s)", fid, r.ID, r.Mod, len(rec.Files), bdir))
	return &rec, nil
}

// undoFiles puts every file of a recipe fix back: originals from the backup, added files removed.
// It checks everything first, so it changes all files or none.
func undoFiles(files []FixFile) error {
	type step struct {
		f    FixFile
		orig []byte
		skip bool
	}
	var steps []step
	gone := 0
	for _, f := range files {
		if f.Big {
			st, ok, err := bigUndoStep(f, addedAt(files, f.Real))
			if err != nil {
				return err
			}
			if !ok {
				gone++
				continue
			}
			steps = append(steps, step{f: f, skip: st == "skip"})
			continue
		}
		cur, err := os.ReadFile(f.Real)
		if f.After == "" { // switched off: back from the kept copy, unless something is in its place now
			if err == nil {
				if sha(cur) == f.Before {
					steps = append(steps, step{f: f, skip: true})
				} else {
					gone++
				}
				continue
			}
			orig, err := os.ReadFile(f.Backup)
			if err != nil || sha(orig) != f.Before {
				return errors.New("a kept original is missing or changed: nothing was touched")
			}
			steps = append(steps, step{f: f, orig: orig})
			continue
		}
		switch {
		case err != nil && f.Before == "":
			steps = append(steps, step{f: f, skip: true}) // already removed
		case err == nil && sha(cur) == f.After:
			st := step{f: f}
			if f.Before != "" {
				orig, err := os.ReadFile(f.Backup)
				if err != nil || sha(orig) != f.Before {
					return errors.New("a kept original is missing or changed: nothing was touched")
				}
				st.orig = orig
			}
			steps = append(steps, st)
		case err == nil && f.Before != "" && sha(cur) == f.Before:
			steps = append(steps, step{f: f, skip: true}) // already the original
		default:
			gone++
		}
	}
	if gone == len(files) {
		return errors.New("gone: the files changed since (the mod was updated or reinstalled), so the fix is already gone with them")
	}
	if gone > 0 {
		return errors.New("some of its files changed since (the mod was updated or reinstalled): put the mod back from its download, which also removes the fix")
	}
	// files Frame Lab added go first: a swapped-in archive can have the same name as the original
	sort.SliceStable(steps, func(i, j int) bool {
		return steps[i].f.Before == "" && steps[i].f.After != "" && !(steps[j].f.Before == "" && steps[j].f.After != "")
	})
	for _, st := range steps {
		if st.skip {
			continue
		}
		var err error
		if st.f.Big {
			if st.f.Before == "" {
				err = os.Remove(st.f.Real)
			} else {
				err = moveFile(st.f.Backup, st.f.Real)
			}
		} else if st.f.Before == "" {
			err = os.Remove(st.f.Real)
		} else {
			os.MkdirAll(filepath.Dir(st.f.Real), 0o755)
			err = os.WriteFile(st.f.Real, st.orig, 0o644)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// fixText / undoText: a fix in one line, for the test schedule
func fixText(r *FixRecord) string {
	switch r.Kind {
	case "recipe", "archive_order":
		return r.Mod + ": " + r.What
	case "tweak_pick":
		return "TweakXL: " + r.What
	case "xl_cooked_type", "file_off", "workbench", "texture_swap":
		return r.Mod + ": " + r.What
	case "merge":
		return "Merged: " + r.What
	case "lua_throttle":
		return fmt.Sprintf("%s: its onUpdate (%s, line %d) now runs %d times a second instead of every frame", r.Mod, r.File, r.Line, r.Hz)
	case "lua_throttle_call":
		return fmt.Sprintf("%s: the call on line %d of %s now runs %d times a second instead of every frame", r.Mod, r.Line, r.File, r.Hz)
	case "lua_hubguard":
		return fmt.Sprintf("%s: its dialog helper (%s) only works while its hub is on screen", r.Mod, r.File)
	case "reds_arraycontains":
		return fmt.Sprintf("%s: one ArrayContains instead of a loop (%s, line %d)", r.Mod, r.File, r.Line)
	}
	return r.Mod + ": " + r.Kind + " (" + r.File + ")"
}

func undoText(r *FixRecord) string {
	switch r.Kind {
	case "recipe":
		return "Undone: " + r.Mod + " has its original files again"
	case "archive_order":
		return "Undone: the archive load order is as it was (" + r.Mod + ")"
	case "tweak_pick":
		return "Undone: " + strings.SplitN(r.What, " = ", 2)[0] + " is up to the mods again"
	case "file_off":
		return "Undone: " + r.File + " is back"
	case "workbench":
		return "Undone: " + r.Mod + " has the files it had before the Workbench edit"
	case "xl_cooked_type":
		return "Undone: " + r.File + " is the original again"
	case "texture_swap":
		return "Undone: " + r.Mod + " has its original archives again"
	case "merge":
		return "Undone: the merged archive of " + r.Mod + " is out again"
	}
	return fmt.Sprintf("Undone: %s (%s, line %d) is the original file again", r.Mod, r.File, r.Line)
}

// scriptsBroken: for the REDscript files Frame Lab changed, whether the game's last REDscript compile
// (r6\logs\redscript_rCURRENT.log, written after the fix) reports an error in them. A broken script stops
// every script mod, so the Optimise page puts this first, with Undo.
func scriptsBroken(game string, fixes []FixRecord) {
	logp := filepath.Join(game, "r6", "logs", "redscript_rCURRENT.log")
	st, err := gamefs.Stat(logp)
	if err != nil {
		return
	}
	b, err := gamefs.ReadFile(logp)
	if err != nil {
		return
	}
	var errLines []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "[ERROR") || strings.Contains(l, "ERROR ") {
			errLines = append(errLines, strings.ToLower(filepath.ToSlash(l)))
		}
	}
	if len(errLines) == 0 {
		return
	}
	for i := range fixes {
		f := &fixes[i]
		if f.Undone != "" || f.Gone != "" {
			continue
		}
		applied, err := time.ParseInLocation("2006-01-02 15:04", f.Applied, time.Local)
		if err != nil || st.ModTime().Before(applied) {
			continue // no compile since the fix
		}
		paths := []string{f.File}
		for _, x := range f.Files {
			paths = append(paths, x.Path)
		}
		for _, p := range paths {
			if !strings.HasSuffix(strings.ToLower(p), ".reds") {
				continue
			}
			name := strings.ToLower(filepath.Base(filepath.FromSlash(p)))
			for _, l := range errLines {
				if strings.Contains(l, name) {
					f.Broke = "The game's last REDscript compile reports an error in " + p + ", which this fix changed. Undo it: until then the game runs without mod scripts."
					break
				}
			}
		}
	}
}
