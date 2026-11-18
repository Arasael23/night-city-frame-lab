package app

// Optimise stage 3 (2.5E): fixes for conflicts, each with the original kept and an exact undo, in the
// same ledger as the script fixes (data\optimise\fixes.json):
//
//	archive_order  the archive that loses a clash loads before the one that wins it, through
//	               archive\pc\mod\modlist.txt (the game reads it; archives it doesn't list follow by name)
//	tweak_pick     a TweakDB value two mods set differently gets the value the player picks, in a small
//	               file of its own under r6\tweaks\zzzzzz_FrameLab (TweakXL reads it last)
//
// Both only accept what the saved conflict check of the build lists, so nothing else can be written.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"framelab/internal/conflicts"
	"framelab/internal/gamefs"
	"framelab/internal/optimise"
)

func (s *Server) savedConflicts(b BuildCfg) (*conflicts.Report, error) {
	bs, err := os.ReadFile(s.conflictPath(b.Name))
	if err != nil {
		return nil, errors.New("run the conflict check first")
	}
	r := &conflicts.Report{}
	if err := json.Unmarshal(bs, r); err != nil {
		return nil, err
	}
	return r, nil
}

// writeFixFiles writes new contents for a few files (nil = remove) and records them as one fix.
// Everything is checked and kept first; on any error nothing stays changed.
func (s *Server) writeFixFiles(b BuildCfg, rec FixRecord, files map[string][]byte) (*FixRecord, error) {
	fixMu.Lock()
	defer fixMu.Unlock()
	all := s.loadFixes()
	rec.ID = time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", len(all)+1)
	rec.Build = b.Name
	rec.Applied = time.Now().Format("2006-01-02 15:04")
	bdir := filepath.Join(s.Paths.Data, "optimise", "backups", rec.ID)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return nil, err
	}
	type done struct {
		real    string
		orig    []byte
		existed bool
	}
	var written []done
	rollback := func() {
		for _, w := range written {
			if w.existed {
				os.WriteFile(w.real, w.orig, 0o644)
			} else {
				os.Remove(w.real)
			}
		}
	}
	n := 0
	for rel, content := range files {
		n++
		real := gamefs.Real(filepath.Join(b.Path, filepath.FromSlash(rel)))
		ff := FixFile{Path: rel, Real: real, After: sha(content)}
		orig, err := os.ReadFile(real)
		existed := err == nil
		if existed {
			ff.Before = sha(orig)
			ff.Backup = filepath.Join(bdir, fmt.Sprintf("%d-%s", n, filepath.Base(real)))
			if err := os.WriteFile(ff.Backup, orig, 0o644); err != nil {
				rollback()
				return nil, fmt.Errorf("could not keep the original: %w", err)
			}
		}
		os.MkdirAll(filepath.Dir(real), 0o755)
		if err := os.WriteFile(real, content, 0o644); err != nil {
			rollback()
			return nil, fmt.Errorf("could not write %s: %w. Nothing was changed", real, err)
		}
		written = append(written, done{real, orig, existed})
		rec.Files = append(rec.Files, ff)
	}
	rec.File, rec.Real, rec.Before, rec.After, rec.Backup = rec.Files[0].Path, rec.Files[0].Real, rec.Files[0].Before, rec.Files[0].After, rec.Files[0].Backup
	all = append(all, rec)
	if err := s.saveFixes(all); err != nil {
		rollback()
		return nil, err
	}
	s.Ctl.diag(fmt.Sprintf("conflict fix %s (%s): %s", rec.ID, rec.Kind, rec.What))
	return &rec, nil
}

// applyArchiveWin makes loser load before winner (both in archive\pc\mod).
func (s *Server) applyArchiveWin(i int, winner, loser string) (*FixRecord, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first: it reads the archive order when it starts")
	}
	r, err := s.savedConflicts(b)
	if err != nil {
		return nil, err
	}
	var pair *conflicts.Pair
	for k := range r.Pairs {
		p := &r.Pairs[k]
		if p.Winner < len(r.Archives) && p.Loser < len(r.Archives) && r.Archives[p.Winner].Rel == winner && r.Archives[p.Loser].Rel == loser {
			pair = p
		}
	}
	if pair == nil {
		return nil, errors.New("that clash isn't in the last conflict check: run it again")
	}
	const dir = "archive/pc/mod/"
	if !strings.HasPrefix(winner, dir) || !strings.HasPrefix(loser, dir) || strings.Contains(winner[len(dir):], "/") || strings.Contains(loser[len(dir):], "/") {
		return nil, errors.New("only archives in archive\\pc\\mod can be put in another order here; REDmod archives load by their mods folder's name")
	}
	order, _ := conflicts.LoadOrder(b.Path)
	var names []string
	for _, o := range order {
		if strings.HasPrefix(o, dir) {
			names = append(names, o[len(dir):])
		}
	}
	w, l := winner[len(dir):], loser[len(dir):]
	var out []string
	found := 0
	for _, n := range names {
		if n == l {
			found++
			continue
		}
		if n == w {
			found++
			out = append(out, l)
		}
		out = append(out, n)
	}
	if found != 2 {
		return nil, errors.New("one of the two archives isn't in the build any more: run the conflict check again")
	}
	content := []byte("# Night City Frame Lab: archive load order (first line loads first). Archives not listed load after these, by name.\r\n" + strings.Join(out, "\r\n") + "\r\n")
	rec := FixRecord{Kind: "archive_order", Mod: r.Archives[pair.Loser].Label,
		What: fmt.Sprintf("%s now loads before %s, so its %d files are the ones used", r.Archives[pair.Loser].Label, r.Archives[pair.Winner].Label, pair.Files)}
	return s.writeFixFiles(b, rec, map[string][]byte{dir + "modlist.txt": content})
}

var reNotFile = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// applyTweakPick sets one clashing TweakDB value to the value of the player's choice.
func (s *Server) applyTweakPick(i int, key, value string) (*FixRecord, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first: TweakXL reads tweaks when the game starts")
	}
	r, err := s.savedConflicts(b)
	if err != nil {
		return nil, err
	}
	var from string
	for _, t := range r.Tweaks {
		if t.Key != key {
			continue
		}
		for _, v := range t.Values {
			if v.Value == value {
				from = v.Mod
			}
		}
	}
	if from == "" {
		return nil, errors.New("that value isn't in the last conflict check: run it again")
	}
	if strings.ContainsAny(key+value, "\r\n") {
		return nil, errors.New("not a simple value")
	}
	name := reNotFile.ReplaceAllString(key, "_")
	if len(name) > 120 {
		name = name[:120]
	}
	rel := "r6/tweaks/zzzzzz_FrameLab/" + name + ".yaml"
	content := []byte("# Night City Frame Lab: your pick for a value two mods set differently (" + from + "'s). Undo in Frame Lab removes this file.\r\n" + key + ": " + value + "\r\n")
	rec := FixRecord{Kind: "tweak_pick", Mod: from, What: key + " = " + value + " (" + from + "'s value)"}
	return s.writeFixFiles(b, rec, map[string][]byte{rel: content})
}

// touchesModlist: the fix wrote archive\pc\mod\modlist.txt (an order fix, or a merge that put its archive on top)
func touchesModlist(f *FixRecord) bool {
	if f.Kind == "archive_order" {
		return true
	}
	for _, x := range f.Files {
		if strings.EqualFold(filepath.Base(filepath.FromSlash(x.Path)), "modlist.txt") {
			return true
		}
	}
	return false
}

// newerOrderFix: an archive_order fix of the same build made after this one and still in place (it
// rewrote modlist.txt again, so this one can only be undone after it)
func newerOrderFix(all []FixRecord, f *FixRecord) *FixRecord {
	if !touchesModlist(f) {
		return nil
	}
	at := -1
	for k := range all {
		if all[k].ID == f.ID {
			at = k
		}
	}
	for k := at + 1; at >= 0 && k < len(all); k++ {
		x := &all[k]
		if x.Build == f.Build && touchesModlist(x) && x.Undone == "" && x.Gone == "" {
			return x
		}
	}
	return nil
}

// applyLogFix: a fix for an error in the build's logs (optimise.LogFixes on the saved conflict check).
func (s *Server) applyLogFix(i int, kind, file string, index int) (*FixRecord, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first")
	}
	r, err := s.savedConflicts(b)
	if err != nil {
		return nil, err
	}
	var fix *optimise.LogFix
	for _, f := range optimise.LogFixes(b.Path, r.Logs) {
		f := f
		if f.Kind == kind && f.File == file && f.Index == index {
			fix = &f
		}
	}
	if fix == nil {
		return nil, errors.New("that error isn't in the last check of the logs: run the conflict check again")
	}
	modName := filepath.Base(filepath.FromSlash(file))
	switch kind {
	case "xl_cooked_type":
		real := gamefs.Real(filepath.Join(b.Path, filepath.FromSlash(file)))
		orig, err := os.ReadFile(real)
		if err != nil {
			return nil, err
		}
		out, ok := optimise.CookedType(string(orig), index)
		if !ok {
			return nil, errors.New("the file doesn't have exactly one entry for that node any more")
		}
		rec := FixRecord{Kind: kind, Mod: modName, What: fmt.Sprintf("node #%d is a worldCookedStaticMeshNode since game 2.5", index)}
		return s.writeFixFiles(b, rec, map[string][]byte{file: []byte(out)})
	case "file_off":
		return s.switchOff(b, file, FixRecord{Kind: kind, Mod: modName, What: "switched off (" + fix.Log + " error)"})
	}
	return nil, errors.New("unknown fix")
}

// switchOff moves one mod file into Frame Lab's data folder; Undo puts it back.
func (s *Server) switchOff(b BuildCfg, rel string, rec FixRecord) (*FixRecord, error) {
	low := strings.ToLower(rel)
	if strings.Contains(rel, "..") || !(strings.HasPrefix(low, "archive/pc/mod/") || strings.HasPrefix(low, "mods/") || strings.HasPrefix(low, "r6/scripts/") || strings.HasPrefix(low, "r6/tweaks/")) {
		return nil, errors.New("only mod files can be switched off")
	}
	real := gamefs.Real(filepath.Join(b.Path, filepath.FromSlash(rel)))
	orig, err := os.ReadFile(real)
	if err != nil {
		return nil, err
	}
	fixMu.Lock()
	defer fixMu.Unlock()
	all := s.loadFixes()
	rec.ID = time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", len(all)+1)
	rec.Build, rec.Applied, rec.File = b.Name, time.Now().Format("2006-01-02 15:04"), rel
	bdir := filepath.Join(s.Paths.Data, "optimise", "backups", rec.ID)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return nil, err
	}
	backup := filepath.Join(bdir, filepath.Base(real))
	if err := os.WriteFile(backup, orig, 0o644); err != nil {
		return nil, fmt.Errorf("could not keep the file: %w", err)
	}
	if err := os.Remove(real); err != nil {
		return nil, fmt.Errorf("could not switch %s off: %w", real, err)
	}
	rec.Files = []FixFile{{Path: rel, Real: real, Before: sha(orig), After: "", Backup: backup}}
	rec.Real, rec.Before, rec.Backup = real, sha(orig), backup
	all = append(all, rec)
	if err := s.saveFixes(all); err != nil {
		os.WriteFile(real, orig, 0o644)
		return nil, err
	}
	s.Ctl.diag("fix " + rec.ID + ": " + rel + " switched off (kept in " + backup + ")")
	return &rec, nil
}
