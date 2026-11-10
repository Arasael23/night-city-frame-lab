package app

// Mod Organizer 2 builds (2.5): a build can name the MO2 instance that runs it. Frame Lab then
// reads the build through that instance (internal/gamefs): mod list, clash check, per-mod costs,
// VRAM per mod, and HitchRecorder's recordings and the game's logs, which MO2 puts in its
// overwrite folder.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"framelab/internal/gamefs"
	"framelab/internal/modmap"
	"framelab/internal/platform"
)

// registerViews tells gamefs which builds MO2 runs.
func registerViews(c Config) {
	m := map[string]string{}
	for _, b := range c.Builds {
		if b.MO2 != "" {
			m[b.Path] = b.MO2
		}
	}
	gamefs.SetAll(m)
}

// checkMO2: the instance folder has a ModOrganizer.ini for this game (the folder is stored, not the ini).
func checkMO2(b *BuildCfg) error {
	b.MO2 = strings.Trim(strings.TrimSpace(b.MO2), `"`)
	if b.MO2 == "" {
		return nil
	}
	ini := gamefs.IniPath(b.MO2)
	b.MO2 = filepath.Dir(ini)
	if _, err := os.Stat(ini); err != nil {
		return fmt.Errorf("%s: no ModOrganizer.ini in %s (MO2 folder: the one with ModOrganizer.exe for a portable MO2, or %%LOCALAPPDATA%%\\ModOrganizer\\<instance>)", b.Name, b.MO2)
	}
	m, err := gamefs.ReadMO2(b.MO2)
	if m == nil {
		return fmt.Errorf("%s: %v", b.Name, err)
	}
	if m.GamePath != "" && m.GamePath != "." && !m.SameGame(b.Path) {
		return fmt.Errorf("%s: that MO2 instance runs %s, not %s", b.Name, m.GamePath, b.Path)
	}
	return nil
}

// MO2State: what the Settings tab shows for a build.
type MO2State struct {
	Index     int           `json:"index"`
	Instance  *gamefs.MO2   `json:"instance,omitempty"` // the build's instance as read now
	Error     string        `json:"error,omitempty"`
	Suggested []*gamefs.MO2 `json:"suggested,omitempty"` // instances found for this game when none is set
}

// mo2States: each build's instance (or the ones found for it).
func mo2States(c Config) []MO2State {
	var extra []string
	for _, p := range platform.ProcessPaths("ModOrganizer.exe") {
		extra = append(extra, filepath.Dir(p))
	}
	var out []MO2State
	for i, b := range c.Builds {
		st := MO2State{Index: i}
		if b.MO2 != "" {
			m, err := gamefs.ReadMO2(b.MO2)
			st.Instance = m
			if err != nil {
				st.Error = err.Error()
			}
		} else if b.Path != "" {
			st.Suggested = gamefs.FindMO2(b.Path, extra)
		}
		out = append(out, st)
	}
	return out
}

// libraryFor: the mod library for naming mods: the zips in Settings > Mod library, and for an MO2
// build the MO2 mods themselves (an MO2 mod's name wins: it is what the game really runs).
func libraryFor(p Paths, c Config, game string) *modmap.Library {
	var lib *modmap.Library
	if c.Library != "" {
		lib = modmap.LoadLibrary(c.Library, filepath.Join(p.Data, "library_index.json"))
		lib.AddManifest(filepath.Join(p.Data, "library_manifest.json"))
	}
	if game != "" && gamefs.Managed(game) != nil {
		if lib == nil {
			lib = &modmap.Library{Owner: map[string]string{}}
		}
		lib.AddMO2(game)
	}
	return lib
}
