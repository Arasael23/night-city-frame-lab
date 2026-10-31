package modmap

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Library maps archives, script folders and plugins to the library zip they came from.
type Library struct {
	Root  string
	Owner map[string]string // key -> zip path relative to Root
	Zips  []ZipInfo
}

type ZipInfo struct {
	Rel   string   `json:"rel"`
	Size  int64    `json:"size"`
	Mtime int64    `json:"mtime"`
	Keys  []string `json:"keys"`
}

var (
	reNexusDash = regexp.MustCompile(`-\d{2,6}(-[0-9A-Za-z.]+)*-\d{9,11}$`)
	reVortex    = regexp.MustCompile(` \d{2,6} [0-9A-Za-z.\-]+ \d{4}-\d\d-\d\dT[\dZ-]+ [0-9A-Za-z]+$`)
)

// ZipLabel turns a library file name into a readable mod name.
func ZipLabel(rel string) string {
	base := filepath.Base(rel)
	for _, ext := range []string{".zip", ".7z", ".rar"} {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.TrimSuffix(base, ".zip")
	base = reNexusDash.ReplaceAllString(base, "")
	base = reVortex.ReplaceAllString(base, "")
	return strings.TrimSpace(base)
}

func keysForEntry(name string) []string {
	n := strings.ToLower(filepath.ToSlash(name))
	var keys []string
	if strings.HasSuffix(n, ".archive") {
		keys = append(keys, "archive:"+filepath.Base(n))
	}
	if i := strings.Index(n, "r6/scripts/"); i >= 0 && strings.HasSuffix(n, ".reds") {
		rest := n[i+len("r6/scripts/"):]
		if j := strings.Index(rest, "/"); j > 0 {
			keys = append(keys, "reds:"+rest[:j])
		} else {
			keys = append(keys, "reds:"+strings.TrimSuffix(rest, ".reds"))
		}
	}
	if i := strings.Index(n, "r6/tweaks/"); i >= 0 && (strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml")) {
		rest := n[i+len("r6/tweaks/"):]
		if j := strings.Index(rest, "/"); j > 0 {
			keys = append(keys, "tweak:"+rest[:j])
		} else {
			keys = append(keys, "tweak:"+strings.TrimSuffix(strings.TrimSuffix(rest, ".yaml"), ".yml"))
		}
	}
	if i := strings.Index(n, "cyber_engine_tweaks/mods/"); i >= 0 {
		rest := n[i+len("cyber_engine_tweaks/mods/"):]
		if j := strings.Index(rest, "/"); j > 0 {
			keys = append(keys, "cet:"+rest[:j])
		}
	}
	if i := strings.Index(n, "red4ext/plugins/"); i >= 0 {
		rest := n[i+len("red4ext/plugins/"):]
		if j := strings.Index(rest, "/"); j > 0 {
			keys = append(keys, "red4ext:"+rest[:j])
		} else if strings.HasSuffix(rest, ".dll") {
			keys = append(keys, "red4ext:"+strings.TrimSuffix(rest, ".dll"))
		}
	}
	return keys
}

// LoadLibrary scans the zips under root (two folder levels deep). cachePath keeps the result between runs.
func LoadLibrary(root, cachePath string) *Library {
	lib := &Library{Root: root, Owner: map[string]string{}}
	if root == "" {
		return lib
	}
	cache := map[string]ZipInfo{}
	if b, err := os.ReadFile(cachePath); err == nil {
		var zs []ZipInfo
		if json.Unmarshal(b, &zs) == nil {
			for _, z := range zs {
				cache[z.Rel] = z
			}
		}
	}
	var paths []string
	for _, pat := range []string{"*.zip", "*/*.zip", "*/*/*.zip"} {
		m, _ := filepath.Glob(filepath.Join(root, pat))
		paths = append(paths, m...)
	}
	sort.Strings(paths)
	changed := false
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		if c, ok := cache[rel]; ok && c.Size == st.Size() && c.Mtime == st.ModTime().Unix() {
			lib.Zips = append(lib.Zips, c)
			continue
		}
		zi := ZipInfo{Rel: rel, Size: st.Size(), Mtime: st.ModTime().Unix()}
		if zr, err := zip.OpenReader(p); err == nil {
			seen := map[string]bool{}
			for _, f := range zr.File {
				for _, k := range keysForEntry(f.Name) {
					if !seen[k] {
						seen[k] = true
						zi.Keys = append(zi.Keys, k)
					}
				}
			}
			zr.Close()
		}
		lib.Zips = append(lib.Zips, zi)
		changed = true
	}
	for _, z := range lib.Zips {
		for _, k := range z.Keys {
			if _, ok := lib.Owner[k]; !ok {
				lib.Owner[k] = z.Rel
			}
		}
	}
	if changed && cachePath != "" {
		if b, err := json.Marshal(lib.Zips); err == nil {
			os.WriteFile(cachePath, b, 0o644)
		}
	}
	return lib
}

// Find returns the library zip label for a key like "archive:x.archive", "reds:owner", "cet:mod", "red4ext:name".
func (l *Library) Find(key string) (label, rel string) {
	if l == nil {
		return "", ""
	}
	rel = l.Owner[strings.ToLower(key)]
	if rel == "" {
		return "", ""
	}
	return ZipLabel(rel), rel
}

// AddManifest merges a build manifest ({"Folder/zip name": [[member, game path], ...]}), which also covers
// .7z/.rar downloads the zip scan can't open. Zip-scan results win where both know a key.
func (l *Library) AddManifest(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var m map[string][][]string
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	n := 0
	for zipRel, files := range m {
		for _, pair := range files {
			if len(pair) < 2 {
				continue
			}
			for _, k := range keysForEntry(pair[1]) {
				if _, ok := l.Owner[k]; !ok {
					l.Owner[k] = filepath.FromSlash(zipRel)
					n++
				}
			}
		}
	}
	return n
}
