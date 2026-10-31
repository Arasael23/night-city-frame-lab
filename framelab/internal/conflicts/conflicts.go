// Package conflicts checks one game build for mods that get in each other's way, without starting
// the game: archives that replace the same game file, REDscript mods that replace the same game
// function, TweakXL files that set the same value differently, and the errors and warnings the
// frameworks wrote in their last logs (RED4ext and its plugins, REDscript, CET).
package conflicts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"framelab/internal/modmap"
)

// Archive is one mod archive in load order.
type Archive struct {
	Rel    string `json:"rel"`           // archive/pc/mod/X.archive or mods/Y/archives/X.archive
	Label  string `json:"label"`         // library zip name, or the archive name
	Zip    string `json:"zip,omitempty"` // library zip (relative to the library)
	Order  int    `json:"order"`         // 1 = loads first
	REDmod bool   `json:"redmod,omitempty"`
	Files  int    `json:"files"`
	Shared int    `json:"shared"` // files another mod archive also has
	Lost   int    `json:"lost"`   // of those, files an earlier archive wins
}

type FileRef struct {
	Hash   string `json:"hash"`
	Name   string `json:"name,omitempty"`
	Loaded bool   `json:"loaded,omitempty"`
	Same   bool   `json:"same,omitempty"` // identical contents in both archives
}

// Pair: Winner (loads first) and Loser both contain Files game files; the loser's copies are never used.
type Pair struct {
	Winner int       `json:"winner"` // index into Archives
	Loser  int       `json:"loser"`
	Files  int       `json:"files"`
	Loaded int       `json:"loaded"` // how many of them the game loaded in the session checked
	Kinds  string    `json:"kinds"`  // e.g. "12 mesh, 3 xbm"
	Sample []FileRef `json:"sample"`
	Order  string    `json:"order"` // "sure" or why the winner is not certain
	// Patch: the winner looks like a compatibility patch / merge / fix made to override the other
	// mod (name says so), so this is most likely on purpose
	Patch bool `json:"patch,omitempty"`
	// Same: files whose contents are byte-identical in both archives: nothing is lost
	Same int `json:"same"`
	// Slot: the loser is a framework that ships placeholder files for other mods to replace (a "core"
	// mod losing small sets of files to several different mods), so this is how it's meant to work
	Slot bool `json:"slot,omitempty"`
}

// Script: a game function that more than one mod replaces (only one replacement runs).
type Script struct {
	Target string   `json:"target"` // Class.Func(types) or Func(types)
	Kind   string   `json:"kind"`   // replaceMethod / replaceGlobal
	Mods   []string `json:"mods"`
	Files  []string `json:"files"`
	Wraps  []string `json:"wraps,omitempty"` // mods that wrap the same function (fine, listed for context)
}

// Tweak: a TweakDB value that more than one mod sets, to different values.
type Tweak struct {
	Key    string     `json:"key"`
	Values []TweakVal `json:"values"`
	Patch  bool       `json:"patch,omitempty"` // one of the mods looks like a compatibility patch
}

type TweakVal struct {
	Mod   string `json:"mod"`
	Value string `json:"value"`
	File  string `json:"file"`
}

// LogSource: the last log of one framework or plugin and the problems in it.
type LogSource struct {
	Name     string    `json:"name"`
	File     string    `json:"file"`
	When     time.Time `json:"when"`
	Errors   int       `json:"errors"`
	Warnings int       `json:"warnings"`
	Lines    []LogLine `json:"lines"`
}

type LogLine struct {
	Level string `json:"level"` // error / warning
	Text  string `json:"text"`
	Count int    `json:"count"`
}

type Report struct {
	Build     string      `json:"build"`
	Game      string      `json:"game"`
	Generated time.Time   `json:"generated"`
	TookS     float64     `json:"took_s"`
	Order     string      `json:"order"` // how the archive order was worked out
	Session   string      `json:"session,omitempty"`
	Archives  []Archive   `json:"archives"`
	Pairs     []Pair      `json:"pairs"`
	Contested int         `json:"contested"` // game files in more than one mod archive
	Scripts   []Script    `json:"scripts"`
	ScriptsN  int         `json:"scripts_scanned"`
	Tweaks    []Tweak     `json:"tweaks"`
	TweaksN   int         `json:"tweaks_scanned"`
	Logs      []LogSource `json:"logs"`
	Notes     []string    `json:"notes,omitempty"`
}

// Options: what the app knows besides the build folder.
type Options struct {
	Build   string
	Game    string
	Library *modmap.Library  // may be nil
	Names   modmap.NamesFile // may be ""
	Loaded  map[uint64]bool  // file hashes the game loaded in a session (may be nil)
	Session string           // that session's title
}

// LooksLikePatch: a mod name that says it exists to override or combine other mods.
func LooksLikePatch(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"compat", "patch", "merge", "priority", "fix for", "texfix", "override", "addon for", "add-on for"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// LooksLikeFramework: a mod name that says other mods build on it.
func LooksLikeFramework(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"core", "framework", "library", "resource pack", "resources"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

func Check(o Options) *Report {
	t0 := time.Now()
	r := &Report{Build: o.Build, Game: o.Game, Generated: t0, Session: o.Session,
		Archives: []Archive{}, Pairs: []Pair{}, Scripts: []Script{}, Tweaks: []Tweak{}, Logs: []LogSource{}}
	if _, err := os.Stat(filepath.Join(o.Game, "bin", "x64")); err != nil {
		r.Notes = append(r.Notes, "Game folder not found: "+o.Game)
		return r
	}
	checkArchives(r, o)
	checkScripts(r, o)
	checkTweaks(r, o)
	checkLogs(r, o.Game)
	r.TookS = time.Since(t0).Seconds()
	return r
}

// ---------------------------------------------------------------- archives

// LoadOrder lists a build's mod archives the way the game loads them: archive/pc/mod in ASCII order of
// the file name (or the order in archive/pc/mod/modlist.txt when that file exists, unlisted ones after),
// then REDmod folders (mods/<name>/archives) by folder name. For a game file in several archives the
// first one loaded wins.
func LoadOrder(game string) (order []string, how string) {
	modDir := filepath.Join(game, "archive", "pc", "mod")
	var loose []string
	if ents, err := os.ReadDir(modDir); err == nil {
		for _, e := range ents {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".archive") {
				loose = append(loose, e.Name())
			}
		}
	}
	sort.Strings(loose)
	how = "alphabetical (ASCII) in archive/pc/mod, first loaded wins"
	if b, err := os.ReadFile(filepath.Join(modDir, "modlist.txt")); err == nil {
		seen := map[string]bool{}
		have := map[string]string{}
		for _, n := range loose {
			have[strings.ToLower(n)] = n
		}
		var listed []string
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
			if line == "" {
				continue
			}
			if n, ok := have[strings.ToLower(line)]; ok && !seen[n] {
				listed = append(listed, n)
				seen[n] = true
			}
		}
		for _, n := range loose {
			if !seen[n] {
				listed = append(listed, n)
			}
		}
		loose = listed
		how = "archive/pc/mod/modlist.txt (archives it doesn't list follow alphabetically), first loaded wins"
	}
	for _, n := range loose {
		order = append(order, "archive/pc/mod/"+n)
	}
	var red []string
	if ents, err := os.ReadDir(filepath.Join(game, "mods")); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				red = append(red, e.Name())
			}
		}
	}
	sort.Strings(red)
	for _, m := range red {
		ents, _ := os.ReadDir(filepath.Join(game, "mods", m, "archives"))
		var as []string
		for _, e := range ents {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".archive") {
				as = append(as, e.Name())
			}
		}
		sort.Strings(as)
		for _, a := range as {
			order = append(order, "mods/"+m+"/archives/"+a)
		}
	}
	return order, how
}

func archiveLabel(lib *modmap.Library, rel string) (label, zip string) {
	if l, z := lib.Find("archive:" + strings.ToLower(filepath.Base(rel))); l != "" {
		return l, z
	}
	return modmap.ArchiveLabel(rel), ""
}

func checkArchives(r *Report, o Options) {
	order, how := LoadOrder(o.Game)
	r.Order = how
	byHash := map[uint64][]int{}
	sha := map[[2]uint64]string{} // (hash, archive) -> content key, for contested files only (filled below)
	entries := map[int][]modmap.Entry{}
	segsOf := map[int][]modmap.Seg{}
	for _, rel := range order {
		hs, segs, err := modmap.ArchiveIndex(filepath.Join(o.Game, filepath.FromSlash(rel)))
		if err != nil {
			r.Notes = append(r.Notes, "Could not read "+rel+": "+err.Error())
			continue
		}
		label, zip := archiveLabel(o.Library, rel)
		i := len(r.Archives)
		r.Archives = append(r.Archives, Archive{Rel: rel, Label: label, Zip: zip, Order: i + 1, REDmod: strings.HasPrefix(rel, "mods/"), Files: len(hs)})
		entries[i] = hs
		segsOf[i] = segs
		seen := map[uint64]bool{}
		for _, e := range hs {
			if !seen[e.Hash] { // an archive can list a hash twice; count it once
				seen[e.Hash] = true
				byHash[e.Hash] = append(byHash[e.Hash], i)
			}
		}
	}
	// contents of the contested files, read from the archives (a few hundred files at most)
	for i, es := range entries {
		var f *os.File
		for _, e := range es {
			if len(byHash[e.Hash]) < 2 {
				continue
			}
			if f == nil {
				var err error
				if f, err = os.Open(filepath.Join(o.Game, filepath.FromSlash(r.Archives[i].Rel))); err != nil {
					break
				}
			}
			sha[[2]uint64{e.Hash, uint64(i)}] = modmap.ContentKey(f, e, segsOf[i])
		}
		if f != nil {
			f.Close()
		}
	}
	type pk struct{ w, l int }
	pairs := map[pk]*Pair{}
	same := map[[2]uint64]bool{}
	pairHashes := map[pk][]uint64{}
	for h, list := range byHash {
		if len(list) < 2 {
			continue
		}
		r.Contested++
		w := list[0]
		for _, a := range list {
			r.Archives[a].Shared++
		}
		for _, l := range list[1:] {
			r.Archives[l].Lost++
			k := pk{w, l}
			p := pairs[k]
			if p == nil {
				p = &Pair{Winner: w, Loser: l, Order: "sure"}
				pairs[k] = p
			}
			p.Files++
			if o.Loaded[h] {
				p.Loaded++
			}
			if a, b := sha[[2]uint64{h, uint64(w)}], sha[[2]uint64{h, uint64(l)}]; a != "" && a == b {
				p.Same++
				same[[2]uint64{h, uint64(l)}] = true
			}
			pairHashes[k] = append(pairHashes[k], h)
		}
	}
	// names for the samples (and the file kinds) in one pass over the name list
	want := map[uint64]bool{}
	for _, hs := range pairHashes {
		for _, h := range hs {
			want[h] = true
		}
	}
	names := map[uint64]string{}
	if o.Names != "" && o.Names.Exists() {
		if n, err := o.Names.Resolve(want); err == nil {
			names = n
		}
	}
	for k, p := range pairs {
		hs := pairHashes[k]
		// loaded first, then named, then by name
		sort.Slice(hs, func(i, j int) bool {
			li, lj := o.Loaded[hs[i]], o.Loaded[hs[j]]
			if li != lj {
				return li
			}
			ni, nj := names[hs[i]], names[hs[j]]
			if (ni != "") != (nj != "") {
				return ni != ""
			}
			return ni < nj
		})
		kinds := map[string]int{}
		for _, h := range hs {
			ext := "unknown"
			if n := names[h]; n != "" {
				ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(n)), ".")
			}
			kinds[ext]++
		}
		p.Kinds = kindList(kinds)
		for i := 0; i < len(hs) && i < 12; i++ {
			p.Sample = append(p.Sample, FileRef{Hash: fmt.Sprintf("%016x", hs[i]), Name: names[hs[i]], Loaded: o.Loaded[hs[i]],
				Same: same[[2]uint64{hs[i], uint64(k.l)}]})
		}
		wa, la := r.Archives[p.Winner], r.Archives[p.Loser]
		p.Patch = LooksLikePatch(wa.Label) || LooksLikePatch(wa.Rel)
		switch {
		case wa.REDmod && la.REDmod:
			p.Order = "both REDmod: the order REDmod deployed them with decides; by folder name this one wins"
		case wa.REDmod || la.REDmod:
			p.Order = "REDmod archives load after archive/pc/mod"
		}
		r.Pairs = append(r.Pairs, *p)
	}
	// frameworks handing out slots: a core mod losing a few files each to 4+ different mods
	winners := map[int]map[int]bool{}
	for _, p := range r.Pairs {
		if winners[p.Loser] == nil {
			winners[p.Loser] = map[int]bool{}
		}
		winners[p.Loser][p.Winner] = true
	}
	for i := range r.Pairs {
		p := &r.Pairs[i]
		la := r.Archives[p.Loser]
		if len(winners[p.Loser]) >= 4 && p.Files <= 3 && (LooksLikeFramework(la.Label) || LooksLikeFramework(la.Rel)) {
			p.Slot = true
		}
	}
	sort.Slice(r.Pairs, func(i, j int) bool {
		a, b := r.Pairs[i], r.Pairs[j]
		ha, hb := a.Patch || a.Slot || a.Same == a.Files, b.Patch || b.Slot || b.Same == b.Files
		if ha != hb {
			return !ha
		}
		if a.Loaded != b.Loaded {
			return a.Loaded > b.Loaded
		}
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Winner < b.Winner
	})
}

func kindList(m map[string]int) string {
	type kv struct {
		k string
		n int
	}
	var l []kv
	for k, n := range m {
		l = append(l, kv{k, n})
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].n != l[j].n {
			return l[i].n > l[j].n
		}
		return l[i].k < l[j].k
	})
	var parts []string
	for i, x := range l {
		if i == 5 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%d %s", x.n, x.k))
	}
	return strings.Join(parts, ", ")
}
