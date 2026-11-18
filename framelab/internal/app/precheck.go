package app

// "Before you install" (2.5 and 2.5E, read only): what a Nexus mod would do in a build, from what Frame
// Lab knows about the downloaded mods (optimise.Corpus) and what the build has now.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"framelab/internal/conflicts"
	"framelab/internal/gamefs"
	"framelab/internal/modmap"
	"framelab/internal/optimise"
)

func (s *Server) corpus() *optimise.Corpus {
	return optimise.LoadCorpus(filepath.Dir(optimiseIndexPath(s.Paths)))
}

type PreClash struct {
	Archive string `json:"archive"` // installed archive (relative to the game folder)
	Label   string `json:"label"`
	Files   int    `json:"files"`
	NewWins bool   `json:"new_wins"` // the new mod's archive loads first, so its files are used
}

type PreArchive struct {
	Name    string     `json:"name"`
	Files   int        `json:"files"`
	Clashes []PreClash `json:"clashes,omitempty"`
}

type PreTweak struct {
	Key       string               `json:"key"`
	Value     string               `json:"value"`
	Installed []conflicts.TweakVal `json:"installed"`
}

type PreReplace struct {
	Target    string   `json:"target"`
	Installed []string `json:"installed"` // files in the build that replace it too
}

type PreCheck struct {
	Nexus     int                 `json:"nexus"`
	Name      string              `json:"name"`
	Downloads int                 `json:"downloads,omitempty"`
	Installed bool                `json:"installed"`
	Archives  []PreArchive        `json:"archives,omitempty"`
	Tweaks    []PreTweak          `json:"tweaks,omitempty"`
	Replaces  []PreReplace        `json:"replaces,omitempty"`
	HotWraps  []string            `json:"hot_wraps,omitempty"`
	Lua       []optimise.LuaFrame `json:"lua,omitempty"`
	Save      map[string]int      `json:"save,omitempty"`
	Needs     [][2]any            `json:"needs,omitempty"` // requirements the build doesn't have: [nexus id, name]
	Recipes   []string            `json:"recipes,omitempty"`
	Notes     []string            `json:"notes,omitempty"`
}

// the build's archives and replaced functions, read once every 2 minutes at most
type buildFacts struct {
	at       time.Time
	order    []string
	byHash   map[uint64][]int // hash -> indexes into order
	listed   map[string]bool  // archive names in modlist.txt
	replaces map[string][]string
	tweaks   map[string][]conflicts.TweakVal
	nexus    map[int]bool
}

var (
	factMu   sync.Mutex
	factMemo = map[string]*buildFacts{}
)

func (s *Server) buildFactsOf(b BuildCfg) *buildFacts {
	factMu.Lock()
	defer factMu.Unlock()
	if f := factMemo[b.Name]; f != nil && time.Since(f.at) < 2*time.Minute {
		return f
	}
	f := &buildFacts{at: time.Now(), byHash: map[uint64][]int{}, listed: map[string]bool{}, replaces: map[string][]string{}, nexus: map[int]bool{}}
	f.order, _ = conflicts.LoadOrder(b.Path)
	for i, rel := range f.order {
		hs, err := modmap.ArchiveHashes(gamefs.Real(filepath.Join(b.Path, filepath.FromSlash(rel))))
		if err != nil {
			continue
		}
		seen := map[uint64]bool{}
		for _, h := range hs {
			if !seen[h] {
				seen[h] = true
				f.byHash[h] = append(f.byHash[h], i)
			}
		}
	}
	if bs, err := gamefs.ReadFile(filepath.Join(b.Path, "archive", "pc", "mod", "modlist.txt")); err == nil {
		for _, l := range strings.Split(string(bs), "\n") {
			if l = strings.TrimSpace(strings.TrimPrefix(l, "\ufeff")); l != "" && !strings.HasPrefix(l, "#") {
				f.listed[strings.ToLower(l)] = true
			}
		}
	}
	root := filepath.Join(b.Path, "r6", "scripts")
	gamefs.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".reds") {
			return nil
		}
		bs, err := gamefs.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(b.Path, p)
		for _, fn := range optimise.ParseReds(string(bs)) {
			if fn.Kind == "replace" {
				t := fn.Name
				if fn.Class != "" {
					t = fn.Class + "." + fn.Name
				}
				f.replaces[t] = append(f.replaces[t], filepath.ToSlash(rel))
			}
		}
		return nil
	})
	f.tweaks = conflicts.InstalledTweaks(conflicts.Options{Build: b.Name, Game: b.Path})
	if bs, err := os.ReadFile(s.optimisePath(b.Name)); err == nil {
		var r struct {
			Mods []struct {
				Nexus int `json:"nexus"`
			} `json:"mods"`
			Frameworks []optimise.Framework `json:"frameworks"` // saved apart from the mods: RED4ext, CET, redscript ...
		}
		if json.Unmarshal(bs, &r) == nil {
			for _, m := range r.Mods {
				if m.Nexus != 0 {
					f.nexus[m.Nexus] = true
				}
			}
			for _, fw := range r.Frameworks {
				if fw.Installed && fw.Nexus != 0 {
					f.nexus[fw.Nexus] = true
				}
			}
		}
	}
	if len(f.nexus) == 0 {
		// no saved mod check of this build yet (Optimise waits for the test round): recognise its mods
		// now, read only, the same way (the result isn't saved)
		if x := s.optimiseIndex(); x != nil {
			r := optimise.Scan(optimise.Options{Build: b.Name, Game: b.Path, Index: x, Corpus: s.corpus()})
			for _, m := range r.Mods {
				if m.Nexus != 0 {
					f.nexus[m.Nexus] = true
				}
			}
			for _, fw := range r.Frameworks {
				if fw.Installed && fw.Nexus != 0 {
					f.nexus[fw.Nexus] = true
				}
			}
		}
	}
	factMemo[b.Name] = f
	return f
}

// newWins: whether an archive named newName (in archive\pc\mod) would load before the installed one
func (f *buildFacts) newWins(newName, installed string) bool {
	if strings.HasPrefix(installed, "mods/") {
		return true // REDmod archives load after every archive in archive\pc\mod
	}
	inst := filepath.Base(installed)
	if len(f.listed) > 0 {
		if f.listed[strings.ToLower(inst)] {
			return false // listed in modlist.txt: before every archive it doesn't list
		}
	}
	return newName < inst // by name (ASCII), first loaded wins
}

func (s *Server) preCheck(i, nexus int) (*PreCheck, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	x := s.optimiseIndex()
	c := s.corpus()
	if x == nil || c == nil {
		return nil, errors.New("Frame Lab's mod data (components\\Optimise) is missing")
	}
	f := s.buildFactsOf(b)
	pc := &PreCheck{Nexus: nexus, Name: x.ModName(nexus), Downloads: c.Downloads(nexus), Installed: f.nexus[nexus]}
	if pc.Name == "" {
		return nil, errors.New("that mod isn't in Frame Lab's data")
	}
	// archives: the game files they carry, against the build's archives
	seenName := map[string]bool{}
	var mine []int
	for _, ai := range c.ArchivesOf(nexus) {
		if k := strings.ToLower(c.Archives[ai].Name); !seenName[k] {
			seenName[k] = true
			mine = append(mine, ai)
		}
	}
	want := map[int]bool{}
	for _, ai := range mine {
		want[ai] = true
	}
	hashes := c.HashesOfMany(want)
	for _, ai := range mine {
		a := c.Archives[ai]
		pa := PreArchive{Name: a.Name, Files: a.N}
		count := map[int]int{}
		for _, h := range hashes[ai] {
			for _, k := range f.byHash[h] {
				count[k]++
			}
		}
		for k, n := range count {
			rel := f.order[k]
			if strings.EqualFold(filepath.Base(rel), a.Name) {
				continue // this mod itself, already installed
			}
			pa.Clashes = append(pa.Clashes, PreClash{Archive: rel, Label: modmap.ArchiveLabel(rel), Files: n, NewWins: f.newWins(a.Name, rel)})
		}
		sort.Slice(pa.Clashes, func(i, j int) bool { return pa.Clashes[i].Files > pa.Clashes[j].Files })
		pc.Archives = append(pc.Archives, pa)
	}
	sort.SliceStable(pc.Archives, func(i, j int) bool { return len(pc.Archives[i].Clashes) > 0 && len(pc.Archives[j].Clashes) == 0 })
	// TweakXL values the build sets differently
	var keys []string
	for k := range c.Tweaks[strconv.Itoa(nexus)] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := c.Tweaks[strconv.Itoa(nexus)][k]
		var diff []conflicts.TweakVal
		for _, iv := range f.tweaks[k] {
			if !strings.EqualFold(iv.Value, v) {
				diff = append(diff, iv)
			}
		}
		if len(diff) > 0 && !pc.Installed {
			pc.Tweaks = append(pc.Tweaks, PreTweak{Key: k, Value: v, Installed: diff})
		}
	}
	// game functions it replaces that the build replaces too (only one replacement runs)
	for _, t := range c.Replaces[strconv.Itoa(nexus)] {
		if in := f.replaces[t]; len(in) > 0 && !pc.Installed {
			pc.Replaces = append(pc.Replaces, PreReplace{Target: t, Installed: in})
		}
	}
	pc.HotWraps = c.HotWraps(nexus)
	pc.Lua = c.LuaEveryFrame(nexus)
	pc.Save = c.SaveChanges(nexus)
	for _, req := range x.Reqs[strconv.Itoa(nexus)] {
		if !f.nexus[req] && req != nexus {
			pc.Needs = append(pc.Needs, [2]any{req, x.ModName(req)})
		}
	}
	seen := map[string]bool{}
	for _, r := range optimise.Recipes() {
		if r.Nexus == nexus && !seen[r.What] { // recipes of several versions often say the same
			seen[r.What] = true
			w := r.What
			if r.Version != "" {
				w += " (for version " + r.Version + ")"
			}
			pc.Recipes = append(pc.Recipes, w)
		}
	}
	return pc, nil
}

// likelyCosts: for the build's recognised mods, what the downloaded copies show runs very often
// (Optimise, before or next to the measurements)
type Likely struct {
	Mod   string              `json:"mod"`
	Nexus int                 `json:"nexus"`
	Hot   []string            `json:"hot,omitempty"`
	Lua   []optimise.LuaFrame `json:"lua,omitempty"`
}

func (s *Server) likelyCosts(b BuildCfg) []Likely {
	c := s.corpus()
	if c == nil {
		return nil
	}
	bs, err := os.ReadFile(s.optimisePath(b.Name))
	if err != nil {
		return nil
	}
	var r struct {
		Mods []struct {
			Name  string `json:"name"`
			Nexus int    `json:"nexus"`
		} `json:"mods"`
	}
	if json.Unmarshal(bs, &r) != nil {
		return nil
	}
	var out []Likely
	for _, m := range r.Mods {
		if m.Nexus == 0 {
			continue
		}
		l := Likely{Mod: m.Name, Nexus: m.Nexus, Hot: c.HotWraps(m.Nexus)}
		for _, f := range c.LuaEveryFrame(m.Nexus) {
			if f.Game && !f.Gated {
				l.Lua = append(l.Lua, f)
			}
		}
		if len(l.Hot) > 0 || len(l.Lua) > 0 {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return len(out[i].Hot)+2*len(out[i].Lua) > len(out[j].Hot)+2*len(out[j].Lua)
	})
	return out
}
