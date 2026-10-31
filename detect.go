package app

// Finding Cyberpunk 2077 installs: what Steam, GOG, Epic and the running game say, the usual
// folders on every drive, and copies next to any install found (modders often keep several
// builds side by side, e.g. "Cyberpunk 2077 - Vanilla+"). Only folders with bin\x64\Cyberpunk2077.exe count.

import (
	"os"
	"path/filepath"
	"strings"

	"framelab/internal/platform"
)

type FoundBuild struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Store string `json:"store,omitempty"`
	Known bool   `json:"known"` // already in the settings
}

func isGameDir(p string) bool {
	st, err := os.Stat(filepath.Join(p, "bin", "x64", "Cyberpunk2077.exe"))
	return err == nil && !st.IsDir()
}

// places people install the game, under each drive's root
var commonGameDirs = [][]string{
	{"Cyberpunk 2077"},
	{"Games", "Cyberpunk 2077"},
	{"GOG Games", "Cyberpunk 2077"},
	{"GOG Galaxy", "Games", "Cyberpunk 2077"},
	{"Program Files (x86)", "GOG Galaxy", "Games", "Cyberpunk 2077"},
	{"Program Files", "GOG Galaxy", "Games", "Cyberpunk 2077"},
	{"SteamLibrary", "steamapps", "common", "Cyberpunk 2077"},
	{"Steam", "steamapps", "common", "Cyberpunk 2077"},
	{"Games", "Steam", "steamapps", "common", "Cyberpunk 2077"},
	{"Program Files (x86)", "Steam", "steamapps", "common", "Cyberpunk 2077"},
	{"Program Files", "Steam", "steamapps", "common", "Cyberpunk 2077"},
	{"Epic Games", "Cyberpunk2077"},
	{"Games", "Epic Games", "Cyberpunk2077"},
	{"Program Files", "Epic Games", "Cyberpunk2077"},
}

func storeOf(p string) string {
	l := strings.ToLower(p)
	switch {
	case strings.Contains(l, "steamapps"):
		return "Steam"
	case strings.Contains(l, "gog"):
		return "GOG"
	case strings.Contains(l, "epic games"):
		return "Epic"
	}
	return ""
}

func cleanKey(p string) string { return strings.ToLower(strings.TrimRight(filepath.Clean(p), `\/`)) }

// detectBuilds lists every install found; existing marks the ones already in the settings.
func detectBuilds(existing []BuildCfg) []FoundBuild {
	type cand struct{ store, path string }
	var cands []cand
	for _, h := range platform.GameInstallHints() {
		cands = append(cands, cand{h.Store, h.Path})
	}
	for _, d := range platform.Drives() {
		for _, rel := range commonGameDirs {
			cands = append(cands, cand{"", filepath.Join(append([]string{d}, rel...)...)})
		}
	}
	seen := map[string]bool{}
	var found []cand
	add := func(c cand) {
		k := cleanKey(c.path)
		if seen[k] || !isGameDir(c.path) {
			return
		}
		seen[k] = true
		if c.store == "" {
			c.store = storeOf(c.path)
		}
		found = append(found, cand{c.store, filepath.Clean(c.path)})
	}
	for _, c := range cands {
		add(c)
	}
	// copies next to an install ("Cyberpunk 2077 - Vanilla+", "Cyberpunk 2077 modded", ...)
	for _, f := range append([]cand(nil), found...) {
		parent := filepath.Dir(f.path)
		ents, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() && strings.HasPrefix(strings.ToLower(e.Name()), "cyberpunk") {
				add(cand{"", filepath.Join(parent, e.Name())})
			}
		}
	}
	known := map[string]string{} // path -> its name in the settings
	names := map[string]bool{}
	for _, b := range existing {
		known[cleanKey(b.Path)] = b.Name
		names[strings.ToLower(b.Name)] = true
	}
	var out []FoundBuild
	for _, f := range found {
		if n, ok := known[cleanKey(f.path)]; ok {
			out = append(out, FoundBuild{Name: n, Path: f.path, Store: f.store, Known: true})
			continue
		}
		out = append(out, FoundBuild{Name: buildName(f.store, f.path, names), Path: f.path, Store: f.store})
	}
	return out
}

// buildName: "Steam", "GOG", "Epic"; a copy's own folder suffix ("Vanilla+" from "Cyberpunk 2077 -
// Vanilla+"); else "Cyberpunk 2077 (E:)". Made unique against taken (which it updates).
func buildName(store, path string, taken map[string]bool) string {
	base := winBase(strings.TrimRight(path, `\/`))
	suffix := ""
	if l := strings.ToLower(base); strings.HasPrefix(l, "cyberpunk 2077") {
		suffix = base[len("cyberpunk 2077"):]
	} else if strings.HasPrefix(l, "cyberpunk2077") {
		suffix = base[len("cyberpunk2077"):]
	} else {
		suffix = base
	}
	suffix = strings.TrimSpace(strings.Trim(suffix, " -_()[]"))
	vol := ""
	if len(path) >= 2 && path[1] == ':' {
		vol = strings.ToUpper(path[:2])
	}
	name := ""
	switch {
	case suffix != "":
		name = suffix
	case store != "":
		name = store
	case vol != "":
		name = "Cyberpunk 2077 (" + vol + ")"
	default:
		name = "Cyberpunk 2077"
	}
	if taken[strings.ToLower(name)] && vol != "" {
		name += " (" + vol + ")"
	}
	n, i := name, 2
	for taken[strings.ToLower(n)] {
		n = name + " " + string(rune('0'+i))
		i++
	}
	taken[strings.ToLower(n)] = true
	return n
}
