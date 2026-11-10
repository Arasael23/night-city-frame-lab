// Package gamefs shows a game folder the way the game sees it (2.5).
//
// Normally that is just the folder. When Mod Organizer 2 runs a build, the mods stay in MO2's own
// folders and MO2 lays them over the game folder only for programs it starts (its virtual file
// system); the game also writes new files (logs, HitchRecorder's recordings) into MO2's overwrite
// folder instead of the game folder. Frame Lab is not started by MO2, so for a build that is
// registered with its MO2 instance these functions put the same picture together themselves:
// the game folder, then every enabled mod of the active profile in MO2's priority order, then the
// overwrite folder on top.
//
// Paths in and out are always game-folder paths (C:\Games\Cyberpunk 2077\r6\scripts\x.reds), so
// callers keep working with paths relative to the game; Real gives the file that is really read.
// For paths outside a registered build every function is the plain os / filepath one.
package gamefs

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type vfile struct {
	real string
	mod  string
	d    fs.DirEntry
}

type vchild struct {
	name string
	dir  bool
	d    fs.DirEntry // the mod's entry (files: the winning one)
}

type view struct {
	game    string // clean game folder
	key     string // lower-case game folder
	mo2     string // instance folder as registered
	inst    *MO2
	err     error
	files   map[string]vfile             // lower-case rel path (slashes) -> winning mod file
	dirs    map[string]map[string]vchild // lower-case rel dir ("" = game folder) -> lower-case name -> entry
	sig     string
	built   time.Time // last rebuild of the mod layer
	checked time.Time // last look at the instance's files
}

var (
	mu    sync.Mutex
	views = map[string]*view{} // lower-case game folder -> view
)

// SetAll registers the MO2 instance of each game folder (game -> instance folder; "" = not MO2).
// Folders not in the map are plain folders again.
func SetAll(m map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	next := map[string]*view{}
	for game, inst := range m {
		if strings.TrimSpace(inst) == "" || strings.TrimSpace(game) == "" {
			continue
		}
		g := filepath.Clean(game)
		k := strings.ToLower(g)
		if v := views[k]; v != nil && v.mo2 == inst {
			next[k] = v
			continue
		}
		next[k] = &view{game: g, key: k, mo2: inst}
	}
	views = next
}

// Register sets one game folder's MO2 instance ("" = plain folder again).
func Register(game, inst string) {
	mu.Lock()
	defer mu.Unlock()
	k := strings.ToLower(filepath.Clean(game))
	if strings.TrimSpace(inst) == "" {
		delete(views, k)
		return
	}
	if v := views[k]; v != nil && v.mo2 == inst {
		return
	}
	views[k] = &view{game: filepath.Clean(game), key: k, mo2: inst}
}

// lookup: the registered view a path is in, and the path relative to its game folder (lower case,
// slashes; "" = the game folder itself).
func lookup(p string) (*view, string) {
	v, rel, _ := lookup2(p)
	return v, rel
}

// lookup2 also gives the relative path as written (for the overwrite folder, which is read live).
func lookup2(p string) (v *view, rel, orig string) {
	if p == "" {
		return nil, "", ""
	}
	c := filepath.Clean(p)
	lc := strings.ToLower(c)
	mu.Lock()
	defer mu.Unlock()
	if len(views) == 0 {
		return nil, "", ""
	}
	for k, v := range views {
		if lc == k {
			v.refresh()
			return v, "", ""
		}
		if strings.HasPrefix(lc, k) && len(lc) > len(k) && (lc[len(k)] == '\\' || lc[len(k)] == '/') {
			v.refresh()
			o := lc[len(k)+1:]
			if len(c) == len(lc) { // lower-casing kept the byte offsets (always for ASCII)
				o = c[len(k)+1:]
			}
			o = strings.ReplaceAll(o, `\`, "/")
			return v, strings.ToLower(o), o
		}
	}
	return nil, "", ""
}

// refresh rebuilds the mod layer when the instance or its mod list changed, or after 30 s
// (a mod's files can change without the list changing). Called with mu held.
func (v *view) refresh() {
	if v.files != nil && time.Since(v.checked) < 3*time.Second {
		return
	}
	v.checked = time.Now()
	sig := sigOf(v.mo2)
	if v.files != nil && sig == v.sig && time.Since(v.built) < 30*time.Second {
		return
	}
	v.sig = sig
	v.built = time.Now()
	v.files = map[string]vfile{}
	v.dirs = map[string]map[string]vchild{}
	v.inst, v.err = ReadMO2(v.mo2)
	if v.inst == nil {
		return
	}
	for _, mod := range v.inst.Mods { // lowest priority first: later mods win
		root := filepath.Join(v.inst.ModsDir, mod)
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == root {
				return nil
			}
			if strings.HasSuffix(strings.ToLower(d.Name()), ".mohidden") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = strings.ToLower(filepath.ToSlash(rel))
			if !d.IsDir() && (rel == "meta.ini") {
				return nil
			}
			v.add(rel, mod, p, d)
			return nil
		})
	}
}

// add puts one mod file or folder into the layer (a later call for the same file wins).
func (v *view) add(rel, mod, real string, d fs.DirEntry) {
	if !d.IsDir() {
		v.files[rel] = vfile{real: real, mod: mod, d: d}
	}
	dir, name := pathSplit(rel)
	m := v.dirs[dir]
	if m == nil {
		m = map[string]vchild{}
		v.dirs[dir] = m
	}
	if old, ok := m[name]; ok && old.dir && d.IsDir() {
		return
	}
	m[name] = vchild{name: d.Name(), dir: d.IsDir(), d: d}
}

func pathSplit(rel string) (dir, name string) {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i], rel[i+1:]
	}
	return "", rel
}

// overwrite: the path in MO2's overwrite folder (orig: relative path as written).
func (v *view) overwrite(orig string) string {
	if v.inst == nil || v.inst.OverwriteDir == "" {
		return ""
	}
	if orig == "" {
		return v.inst.OverwriteDir
	}
	return filepath.Join(v.inst.OverwriteDir, filepath.FromSlash(orig))
}

// real: where the game's file at rel really is ("" = not in the mod layers: the game folder's own).
func (v *view) real(rel, orig string) (path, mod string) {
	if rel == "" {
		return "", ""
	}
	if ow := v.overwrite(orig); ow != "" {
		if st, err := os.Stat(ow); err == nil && !st.IsDir() {
			return ow, "Overwrite"
		}
	}
	if f, ok := v.files[rel]; ok {
		return f.real, f.mod
	}
	return "", ""
}

// ---------------------------------------------------------------- public

// Managed: the folder of p is shown through MO2 (the instance, or nil).
func Managed(p string) *MO2 {
	v, _ := lookup(p)
	if v == nil {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	return v.inst
}

// Status of a registered game folder: the instance and the error reading it, if any.
func Status(game string) (*MO2, error) {
	v, _ := lookup(game)
	if v == nil {
		return nil, nil
	}
	mu.Lock()
	defer mu.Unlock()
	return v.inst, v.err
}

// Real: the file the game reads for p (p itself outside an MO2 build or when no mod has it).
func Real(p string) string {
	v, rel, orig := lookup2(p)
	if v == nil {
		return p
	}
	mu.Lock()
	r, _ := v.real(rel, orig)
	mu.Unlock()
	if r == "" {
		return p
	}
	return r
}

// Source: the MO2 mod p comes from ("Overwrite" for MO2's overwrite folder), "" for the game folder.
func Source(p string) string {
	v, rel, orig := lookup2(p)
	if v == nil {
		return ""
	}
	mu.Lock()
	defer mu.Unlock()
	_, mod := v.real(rel, orig)
	return mod
}

// Overwrite: MO2's overwrite folder for a game folder, or "".
func Overwrite(game string) string {
	v, _ := lookup(game)
	if v == nil {
		return ""
	}
	mu.Lock()
	defer mu.Unlock()
	return v.overwrite("")
}

func ReadFile(p string) ([]byte, error) { return os.ReadFile(Real(p)) }
func Open(p string) (*os.File, error)   { return os.Open(Real(p)) }
func Exists(p string) bool              { _, err := Stat(p); return err == nil }
func IsFile(p string) bool              { st, err := Stat(p); return err == nil && !st.IsDir() }

// Stat: the file or folder as the game sees it.
func Stat(p string) (fs.FileInfo, error) {
	v, rel, orig := lookup2(p)
	if v == nil || rel == "" {
		return os.Stat(p)
	}
	mu.Lock()
	r, _ := v.real(rel, orig)
	var dirEnt fs.DirEntry
	if r == "" {
		d, n := pathSplit(rel)
		if c, ok := v.dirs[d][n]; ok && c.dir {
			dirEnt = c.d
		}
	}
	ow := v.overwrite(orig)
	mu.Unlock()
	if r != "" {
		return os.Stat(r)
	}
	if st, err := os.Stat(p); err == nil {
		return st, nil
	}
	if dirEnt != nil {
		return dirEnt.Info()
	}
	if ow != "" {
		if st, err := os.Stat(ow); err == nil && st.IsDir() {
			return st, nil
		}
	}
	return os.Stat(p)
}

// ReadDir: the folder's entries as the game sees them (game folder + mods + overwrite), by name.
func ReadDir(dir string) ([]fs.DirEntry, error) {
	v, rel, orig := lookup2(dir)
	if v == nil {
		return os.ReadDir(dir)
	}
	base, errBase := os.ReadDir(dir)
	byName := map[string]fs.DirEntry{}
	for _, e := range base {
		byName[strings.ToLower(e.Name())] = e
	}
	mu.Lock()
	mods := v.dirs[rel]
	ow := v.overwrite(orig)
	for k, c := range mods {
		if old, ok := byName[k]; ok && old.IsDir() && c.dir {
			continue // same folder in both
		}
		byName[k] = c.d
	}
	mu.Unlock()
	found := errBase == nil || len(mods) > 0
	if ow != "" {
		if oe, err := os.ReadDir(ow); err == nil {
			found = true
			for _, e := range oe {
				k := strings.ToLower(e.Name())
				if old, ok := byName[k]; ok && old.IsDir() && e.IsDir() {
					continue
				}
				byName[k] = e
			}
		}
	}
	if !found {
		return nil, errBase
	}
	out := make([]fs.DirEntry, 0, len(byName))
	for _, e := range byName {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// WalkDir is filepath.WalkDir over the game's view: paths handed to fn are game-folder paths.
func WalkDir(root string, fn fs.WalkDirFunc) error {
	if v, _ := lookup(root); v == nil {
		return filepath.WalkDir(root, fn)
	}
	st, err := Stat(root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walk(root, fs.FileInfoToDirEntry(st), fn)
	}
	if err == filepath.SkipDir || err == filepath.SkipAll {
		return nil
	}
	return err
}

func walk(p string, d fs.DirEntry, fn fs.WalkDirFunc) error {
	if err := fn(p, d, nil); err != nil || !d.IsDir() {
		if err == filepath.SkipDir && d.IsDir() {
			err = nil
		}
		return err
	}
	ents, err := ReadDir(p)
	if err != nil {
		if err = fn(p, d, err); err != nil {
			if err == filepath.SkipDir && d.IsDir() {
				err = nil
			}
			return err
		}
	}
	for _, e := range ents {
		if err := walk(filepath.Join(p, e.Name()), e, fn); err != nil {
			if err == filepath.SkipDir {
				break
			}
			return err
		}
	}
	return nil
}

// Glob is filepath.Glob over the game's view (names match without regard to case, like Windows).
func Glob(pattern string) ([]string, error) {
	if v, _ := lookup(pattern); v == nil {
		if v2, _ := lookup(firstPlain(pattern)); v2 == nil {
			return filepath.Glob(pattern)
		}
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, err
	}
	dir, file := filepath.Split(pattern)
	dir = cleanGlobPath(dir)
	if !hasMeta(dir) {
		return globDir(dir, file, nil), nil
	}
	if dir == pattern {
		return nil, filepath.ErrBadPattern
	}
	parents, err := Glob(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range parents {
		out = globDir(d, file, out)
	}
	return out, nil
}

// firstPlain: the part of a pattern before its first wildcard element.
func firstPlain(pattern string) string {
	parts := strings.Split(filepath.Clean(pattern), string(filepath.Separator))
	var keep []string
	for _, p := range parts {
		if hasMeta(p) {
			break
		}
		keep = append(keep, p)
	}
	return strings.Join(keep, string(filepath.Separator))
}

func globDir(dir, pattern string, out []string) []string {
	if st, err := Stat(dir); err != nil || !st.IsDir() {
		return out
	}
	ents, err := ReadDir(dir)
	if err != nil {
		return out
	}
	lp := strings.ToLower(pattern)
	for _, e := range ents {
		if ok, _ := filepath.Match(lp, strings.ToLower(e.Name())); ok {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func cleanGlobPath(p string) string {
	switch p {
	case "":
		return "."
	case string(filepath.Separator):
		return p
	default:
		return p[:len(p)-1]
	}
}

func hasMeta(p string) bool { return strings.ContainsAny(p, `*?[`) }

// ModFiles: every file the enabled MO2 mods put into a game folder: path relative to the game
// folder (lower case, slashes) -> the mod it comes from (the one that wins).
func ModFiles(game string) map[string]string {
	v, _ := lookup(game)
	if v == nil {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]string, len(v.files))
	for rel, f := range v.files {
		out[rel] = f.mod
	}
	return out
}
