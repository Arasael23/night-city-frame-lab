package app

// Session snapshot at Arm (2.0): game / driver / Windows versions, the game's settings and the
// mod files of the build, for Compare.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"framelab/internal/analysis"
	"framelab/internal/platform"
)

// mod places in a game folder; each entry right under them counts as one item
var modRoots = []string{
	`archive\pc\mod`, `mods`, `r6\scripts`, `r6\tweaks`, `red4ext\plugins`,
	`bin\x64\plugins\cyber_engine_tweaks\mods`, `bin\x64\plugins`,
}

func buildSnapshot(game, exe, gpuName string) *analysis.Snapshot {
	s := &analysis.Snapshot{Game: gameVersion(exe)}
	s.Windows, s.Driver = platform.SystemVersions(gpuName)
	if b, err := os.ReadFile(platform.UserSettingsPath()); err == nil {
		s.Settings = flattenSettings(b)
	}
	s.Mods = modFiles(game)
	return s
}

// flattenSettings: UserSettings.json -> "group/option" = value (index for list options).
func flattenSettings(b []byte) map[string]string {
	var doc struct {
		Data []struct {
			Group   string `json:"group_name"`
			Options []struct {
				Name  string          `json:"name"`
				Value json.RawMessage `json:"value"`
				Index *int            `json:"index"`
			} `json:"options"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	out := map[string]string{}
	for _, g := range doc.Data {
		for _, o := range g.Options {
			v := string(o.Value)
			if len(v) > 1 && v[0] == '"' {
				var str string
				if json.Unmarshal(o.Value, &str) == nil {
					v = str
				}
			}
			out[g.Group+"/"+o.Name] = v
		}
	}
	return out
}

func modFiles(game string) []analysis.ModFile {
	var out []analysis.ModFile
	seen := map[string]bool{}
	for _, root := range modRoots {
		dir := filepath.Join(game, root)
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			rel := filepath.Join(root, e.Name())
			if seen[rel] || (root == `bin\x64\plugins` && e.Name() == "cyber_engine_tweaks") {
				continue
			}
			seen[rel] = true
			if root == `bin\x64\plugins` && !e.IsDir() && filepath.Ext(e.Name()) != ".asi" && filepath.Ext(e.Name()) != ".dll" {
				continue
			}
			mf := analysis.ModFile{Path: rel}
			if !e.IsDir() {
				if fi, err := e.Info(); err == nil {
					mf.Size, mf.MTime = fi.Size(), fi.ModTime().Unix()
				}
			} else {
				n := 0
				filepath.WalkDir(filepath.Join(dir, e.Name()), func(p string, d fs.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return nil
					}
					n++
					if n > 5000 {
						return filepath.SkipAll
					}
					if fi, err := d.Info(); err == nil {
						mf.Size += fi.Size()
						if t := fi.ModTime().Unix(); t > mf.MTime {
							mf.MTime = t
						}
					}
					return nil
				})
			}
			out = append(out, mf)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func writeSnapshot(dir, game, gpuName string) {
	exe := filepath.Join(game, "bin", "x64", "Cyberpunk2077.exe")
	if err := analysis.WriteSnapshot(dir, buildSnapshot(game, exe, gpuName)); err != nil {
		fmt.Fprintln(os.Stderr, "snapshot:", err)
	}
}
