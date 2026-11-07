package app

// VRAM tab: GPU data per mod for a build (internal/vram), run in the background like the conflict
// check and kept in data\vram\<build>.json.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"framelab/internal/modmap"
	"framelab/internal/platform"
	"framelab/internal/vram"
)

var (
	vramMu    sync.Mutex
	vramState = map[string]string{}
)

func (s *Server) vramPath(build string) string {
	return filepath.Join(s.Paths.Data, "vram", strings.TrimSpace(reUnsafe.ReplaceAllString(build, "_"))+".json")
}

func (s *Server) scanVram(i int) error {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	b := cfg.Builds[i]
	vramMu.Lock()
	if vramState[b.Name] == "scanning" {
		vramMu.Unlock()
		return nil
	}
	vramState[b.Name] = "scanning"
	vramMu.Unlock()
	go func() {
		o := vram.Options{Build: b.Name, Game: b.Path, Names: modmap.NamesFile(filepath.Join(s.Paths.Data, "names.txt.gz"))}
		if dec, err := platform.Oodle(filepath.Join(b.Path, "bin", "x64", "oo2ext_7_win64.dll")); err == nil {
			o.Decompress = dec
		}
		if cfg.Library != "" {
			o.Library = modmap.LoadLibrary(cfg.Library, filepath.Join(s.Paths.Data, "library_index.json"))
			o.Library.AddManifest(filepath.Join(s.Paths.Data, "library_manifest.json"))
		}
		o.Loaded, o.Session = s.loadedInLastSession(b.Path)
		vortex := modmap.VortexSources(b.Path)
		o.Nexus = func(rel string) string {
			if l := o.Library; l != nil {
				if _, zip := l.Find("archive:" + strings.ToLower(filepath.Base(rel))); zip != "" {
					if id := modmap.NexusID(zip); id != "" {
						return id
					}
				}
			}
			return modmap.NexusID(vortex[strings.ToLower(rel)])
		}
		r := vram.Scan(o)
		state := ""
		p := s.vramPath(b.Name)
		if bs, err := os.ReadFile(p); err == nil {
			var prev vram.Report
			if json.Unmarshal(bs, &prev) == nil && prev.Sigs != nil {
				r.Changes = vram.Diff(&prev, r)
			}
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		if bs, err := json.Marshal(r); err != nil {
			state = "error: " + err.Error()
		} else if err := os.WriteFile(p, bs, 0o644); err != nil {
			state = "error: " + err.Error()
		}
		s.Ctl.diag("vram scan " + b.Name + ": " + itoa(len(r.Mods)) + " mods")
		vramMu.Lock()
		vramState[b.Name] = state
		vramMu.Unlock()
	}()
	return nil
}

func (s *Server) vramReport(i int) (map[string]any, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return nil, errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	b := cfg.Builds[i]
	vramMu.Lock()
	state := vramState[b.Name]
	vramMu.Unlock()
	out := map[string]any{"build": b.Name, "state": state}
	if bs, err := os.ReadFile(s.vramPath(b.Name)); err == nil {
		out["report"] = json.RawMessage(bs)
	}
	return out, nil
}
