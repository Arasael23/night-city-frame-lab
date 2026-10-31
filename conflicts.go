package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"framelab/internal/analysis"
	"framelab/internal/conflicts"
	"framelab/internal/hrec"
	"framelab/internal/modmap"
)

// Conflict check per build: runs in the background (a few seconds for a few hundred archives) and
// keeps the last result in data\conflicts\<build>.json.

var (
	conflictMu    sync.Mutex
	conflictState = map[string]string{} // build name -> "checking" / "" / "error: ..."
	reUnsafe      = regexp.MustCompile(`[^A-Za-z0-9 ._+-]+`)
)

func (s *Server) conflictPath(build string) string {
	return filepath.Join(s.Paths.Data, "conflicts", strings.TrimSpace(reUnsafe.ReplaceAllString(build, "_"))+".json")
}

// latest finished session of this build with a HitchRecorder recording: the files the game loaded
func (s *Server) loadedInLastSession(game string) (map[uint64]bool, string) {
	for _, si := range s.Ctl.Sessions() {
		dir, err := s.Ctl.SessionDir(si.ID)
		if err != nil {
			continue
		}
		m, err := analysis.ReadMeta(dir)
		if err != nil || !m.Finished || !strings.EqualFold(filepath.Clean(m.Game), filepath.Clean(game)) {
			continue
		}
		f, err := hrec.Read(filepath.Join(dir, analysis.FileHitch))
		if err != nil {
			continue
		}
		out := map[uint64]bool{}
		for i := range f.Recs {
			if r := &f.Recs[i]; hrec.IsPath(r.Kind) && r.Hash != 0 {
				out[r.Hash] = true
			}
		}
		title := m.Title
		if title == "" {
			title = si.ID
		}
		return out, title + " (" + si.Created + ")"
	}
	return nil, ""
}

func (s *Server) checkConflicts(i int) error {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	b := cfg.Builds[i]
	conflictMu.Lock()
	if conflictState[b.Name] == "checking" {
		conflictMu.Unlock()
		return nil
	}
	conflictState[b.Name] = "checking"
	conflictMu.Unlock()
	go func() {
		o := conflicts.Options{Build: b.Name, Game: b.Path, Names: modmap.NamesFile(filepath.Join(s.Paths.Data, "names.txt.gz"))}
		if cfg.Library != "" {
			o.Library = modmap.LoadLibrary(cfg.Library, filepath.Join(s.Paths.Data, "library_index.json"))
			o.Library.AddManifest(filepath.Join(s.Paths.Data, "library_manifest.json"))
		}
		o.Loaded, o.Session = s.loadedInLastSession(b.Path)
		r := conflicts.Check(o)
		state := ""
		p := s.conflictPath(b.Name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if bs, err := json.Marshal(r); err != nil {
			state = "error: " + err.Error()
		} else if err := os.WriteFile(p, bs, 0o644); err != nil {
			state = "error: " + err.Error()
		}
		s.Ctl.diag("conflict check " + b.Name + ": " + itoa(len(r.Pairs)) + " archive pairs, " + itoa(len(r.Scripts)) + " script, " + itoa(len(r.Tweaks)) + " tweak conflicts")
		conflictMu.Lock()
		conflictState[b.Name] = state
		conflictMu.Unlock()
	}()
	return nil
}

// conflictReport: the saved report of a build (raw JSON) and the check's state
func (s *Server) conflictReport(i int) (map[string]any, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return nil, errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	b := cfg.Builds[i]
	conflictMu.Lock()
	state := conflictState[b.Name]
	conflictMu.Unlock()
	out := map[string]any{"build": b.Name, "state": state}
	if bs, err := os.ReadFile(s.conflictPath(b.Name)); err == nil {
		out["report"] = json.RawMessage(bs)
	}
	return out, nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
