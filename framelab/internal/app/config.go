// Package app is Night City Frame Lab itself: settings, the capture controller and the local web API
// the window talks to.
package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const Version = "2.5.2"

type BuildCfg struct {
	Name string `json:"name"`
	Path string `json:"path"`
	MO2  string `json:"mo2,omitempty"` // Mod Organizer 2 instance folder (the one with ModOrganizer.ini) when MO2 runs this build (2.5)
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
	FeltKey        int        `json:"felt_key,omitempty"`          // "I felt a stutter" key, Windows key code (0 = F10) (2.5)
	MarkKey        int        `json:"mark_key,omitempty"`          // "mark a part" key = HitchRecorder's capture key (0 = F11) (2.5)
	LiveUpdates    bool       `json:"live_updates,omitempty"`      // Optimise: ask Nexus Mods for newer mod versions (off unless switched on) (2.5)
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
		c.Builds = append(c.Builds, BuildCfg{Name: f.Name, Path: f.Path})
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
	registerViews(*s.cfg)
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
	if err := checkKeys(&c); err != nil {
		return err
	}
	// builds: tidy pasted paths, name the unnamed ones, and refuse a new folder that isn't a game folder
	// (one already saved is kept as it is, so a drive that is unplugged for now doesn't block saving)
	old := map[string]bool{}
	taken := map[string]bool{}
	for _, b := range s.cfg.Builds {
		old[cleanKey(b.Path)] = true
	}
	for _, b := range c.Builds {
		if b.Name = strings.TrimSpace(b.Name); b.Name != "" {
			taken[strings.ToLower(b.Name)] = true
		}
	}
	for i := range c.Builds {
		b := &c.Builds[i]
		b.Name = strings.TrimSpace(b.Name)
		b.Path = gameRoot(b.Path)
		if b.Path == "" {
			return fmt.Errorf("build %d has no game folder", i+1)
		}
		if !isGameDir(b.Path) && !old[cleanKey(b.Path)] {
			return fmt.Errorf("%s is not a Cyberpunk 2077 folder: pick the folder that has bin\\x64\\Cyberpunk2077.exe in it", b.Path)
		}
		if b.Name == "" {
			b.Name = buildName(storeOf(b.Path), b.Path, taken)
		}
	}
	for i := range c.Builds {
		if err := checkMO2(&c.Builds[i]); err != nil {
			return err
		}
	}
	s.cfg = &c
	registerViews(c)
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
	Markers25  string // the same for game 2.5 and later (the game itself has LoadingScreenProgressBarController.OnUninitialize there)
	MarkersLua string // folder with the markers as a CET mod (init.lua), used on game 2.5 and later, or ""
	OptionsLua string // folder with the kept-options CET mod (init.lua), or ""
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
	out.Markers25 = firstExisting(filepath.Join(p.Components, "FrameLabMarkers", "FrameLab.Markers.2.5.reds"))
	if d := filepath.Join(p.Components, "FrameLabMarkers", "cet"); firstExisting(filepath.Join(d, "init.lua")) != "" {
		out.MarkersLua = d
	}
	if d := filepath.Join(p.Components, "FrameLabOptions"); firstExisting(filepath.Join(d, "init.lua")) != "" {
		out.OptionsLua = d
	}
	return out
}
