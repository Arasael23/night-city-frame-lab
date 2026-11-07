// Package app is Night City Frame Lab itself: settings, the capture controller and the local web API
// the window talks to.
package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const Version = "2.0.0"

type BuildCfg struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Config struct {
	Builds         []BuildCfg `json:"builds"`
	Active         int        `json:"active"`
	SpikeMs        float64    `json:"spike_ms"`
	PresentMon     string     `json:"presentmon,omitempty"` // override path to PresentMon-*.exe
	Library        string     `json:"library"`              // optional: folder of mod zips (which download each file came from)
	WolvenKit      string     `json:"wolvenkit"`            // WolvenKit folder (file-name list)
	TrackFrameType bool       `json:"track_frame_type"`
	UseScripts     bool       `json:"use_script_profilers"`
	NoWinEvents    bool       `json:"no_windows_events,omitempty"` // Windows event recorder off (on by default)
}

// Paths of the app's own folders (portable: everything lives next to the exe).
type Paths struct {
	Root       string // folder of the exe
	Data       string // root\data
	Sessions   string // root\Sessions
	Components string // root\components
	Settings   string // root\FrameLab.settings.json
}

func NewPaths(root string) Paths {
	return Paths{Root: root, Data: filepath.Join(root, "data"), Sessions: filepath.Join(root, "Sessions"),
		Components: filepath.Join(root, "components"), Settings: filepath.Join(root, "FrameLab.settings.json")}
}

func baseConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		SpikeMs:        50,
		WolvenKit:      filepath.Join(home, "Documents", "WolvenKit"),
		TrackFrameType: true,
		UseScripts:     true,
	}
}

func defaultConfig(p Paths) *Config {
	c := baseConfig()
	c.Builds = []BuildCfg{}
	// first start: every Cyberpunk 2077 install that can be found (Settings > Find game installs adds more later)
	for _, f := range detectBuilds(nil) {
		c.Builds = append(c.Builds, BuildCfg{f.Name, f.Path})
	}
	return c
}

type Settings struct {
	mu    sync.Mutex
	paths Paths
	cfg   *Config
}

func LoadSettings(p Paths) *Settings {
	s := &Settings{paths: p}
	if b, err := os.ReadFile(p.Settings); err == nil {
		c := baseConfig()
		if json.Unmarshal(b, c) == nil && len(c.Builds) > 0 {
			s.cfg = c
		}
	}
	if s.cfg == nil {
		s.cfg = defaultConfig(p)
	}
	if s.cfg.SpikeMs < 10 {
		s.cfg.SpikeMs = 50
	}
	if s.cfg.Active < 0 || s.cfg.Active >= len(s.cfg.Builds) {
		s.cfg.Active = 0
	}
	return s
}

func (s *Settings) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *s.cfg
	c.Builds = append([]BuildCfg{}, s.cfg.Builds...)
	return c
}

func (s *Settings) Set(c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(c.Builds) == 0 {
		c.Builds = s.cfg.Builds
	}
	if c.Active < 0 || c.Active >= len(c.Builds) {
		c.Active = 0
	}
	if c.SpikeMs < 10 {
		c.SpikeMs = 50
	}
	s.cfg = &c
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.Encode(c)
	return os.WriteFile(s.paths.Settings, buf.Bytes(), 0o644)
}

// ---------------------------------------------------------------- bundled / found tools

type Components struct {
	PresentMon string // exe path or ""
	HitchPkg   string // folder holding red4ext\plugins\HitchRecorder (to install from), or ""
	Markers    string // FrameLab.Markers.reds (loading-screen markers for a session), or ""
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func globNewest(pattern string) string {
	m, _ := filepath.Glob(pattern)
	if len(m) == 0 {
		return ""
	}
	sort.Strings(m)
	return m[len(m)-1]
}

func FindComponents(p Paths, c Config) Components {
	var out Components
	out.PresentMon = firstExisting(c.PresentMon,
		globNewest(filepath.Join(p.Components, "PresentMon", "PresentMon-*.exe")),
		globNewest(`C:\Program Files (x86)\CapFrameX\PresentMon\PresentMon-*.exe`),
		globNewest(`C:\Program Files\Intel\PresentMon\PresentMon-*.exe`))
	out.HitchPkg = firstExisting(filepath.Join(p.Components, "HitchRecorder"))
	out.Markers = firstExisting(filepath.Join(p.Components, "FrameLabMarkers", "FrameLab.Markers.reds"))
	return out
}
