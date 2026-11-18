package optimise

// What Frame Lab knows from the ~6,200 downloaded Nexus mods besides the mod index (2.5 and 2.5E, read
// only). Two files in components\Optimise, made from the corpus database by tools/export.py:
//
//	archives.bin.gz  for every mod archive (Nexus mod + archive name): the game files it carries, as the
//	                 archive's own path hashes. Recognises a renamed archive, and shows which installed
//	                 archives a mod would clash with before it is installed.
//	corpus.json.gz   per mod: the TweakXL values it sets, the game functions its scripts replace or wrap,
//	                 its CET onUpdate handlers that work every frame, script calls that change the save
//	                 (items, teleports, quest facts), and its download count.
//
// Only names, hashes and numbers: no mod file contents.

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type CorpusArchive struct {
	Nexus int
	Name  string
	N     int // files in it
}

type corpusHit struct {
	h uint64
	a int32
}

// Corpus: the loaded data.
type Corpus struct {
	Tweaks   map[string]map[string]string `json:"tweaks"`
	Replaces map[string][]string          `json:"replaces"`
	Wraps    map[string][]string          `json:"wraps"`
	Lua      map[string][][]any           `json:"lua"`
	Save     map[string]map[string]int    `json:"save"`
	DL       map[string]int               `json:"dl"`
	Variants map[string][][]any           `json:"variants"` // nexus -> [file id, name, "2K", download bytes, [archive names]]
	Archives []CorpusArchive              `json:"-"`
	flat     []corpusHit                  // every (hash, archive), sorted by hash
}

var (
	corpusMu    sync.Mutex
	corpusCache = map[string]*Corpus{}
)

// LoadCorpus reads the two files from dir (components\Optimise); cached. nil when they aren't there.
func LoadCorpus(dir string) *Corpus {
	corpusMu.Lock()
	defer corpusMu.Unlock()
	if c, ok := corpusCache[dir]; ok {
		return c
	}
	c, err := loadCorpus(dir)
	if err != nil {
		c = nil
	}
	corpusCache[dir] = c
	return c
}

func loadCorpus(dir string) (*Corpus, error) {
	c := &Corpus{}
	f, err := os.Open(filepath.Join(dir, "corpus.json.gz"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	if err := json.NewDecoder(zr).Decode(c); err != nil {
		return nil, err
	}
	a, err := os.Open(filepath.Join(dir, "archives.bin.gz"))
	if err != nil {
		return c, nil // the json part alone still helps
	}
	defer a.Close()
	az, err := gzip.NewReader(a)
	if err != nil {
		return c, nil
	}
	r := bufio.NewReaderSize(az, 1<<20)
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil || string(head[:4]) != "FLAR" {
		return c, nil
	}
	n := int(binary.LittleEndian.Uint32(head[8:]))
	for i := 0; i < n; i++ {
		var h6 [6]byte
		if _, err := io.ReadFull(r, h6[:]); err != nil {
			return c, errors.New("archives.bin.gz is cut short")
		}
		nexus := int(binary.LittleEndian.Uint32(h6[:4]))
		name := make([]byte, binary.LittleEndian.Uint16(h6[4:]))
		var cnt [4]byte
		if _, err := io.ReadFull(r, name); err != nil {
			return c, err
		}
		if _, err := io.ReadFull(r, cnt[:]); err != nil {
			return c, err
		}
		k := int(binary.LittleEndian.Uint32(cnt[:]))
		c.Archives = append(c.Archives, CorpusArchive{Nexus: nexus, Name: string(name), N: k})
		var hb [8]byte
		for j := 0; j < k; j++ {
			if _, err := io.ReadFull(r, hb[:]); err != nil {
				return c, err
			}
			c.flat = append(c.flat, corpusHit{binary.LittleEndian.Uint64(hb[:]), int32(i)})
		}
	}
	sort.Slice(c.flat, func(i, j int) bool { return c.flat[i].h < c.flat[j].h })
	return c, nil
}

// archivesWith: the corpus archives that carry a game file (by path hash)
func (c *Corpus) archivesWith(h uint64) []int32 {
	i := sort.Search(len(c.flat), func(i int) bool { return c.flat[i].h >= h })
	var out []int32
	for ; i < len(c.flat) && c.flat[i].h == h; i++ {
		out = append(out, c.flat[i].a)
	}
	return out
}

// RecognizeArchive: the Nexus mod whose archive carries most of these game files. share = part of the
// installed archive's files that archive has (0..1).
func (c *Corpus) RecognizeArchive(hashes []uint64) (a CorpusArchive, share float64) {
	if c == nil || len(c.flat) == 0 || len(hashes) == 0 {
		return CorpusArchive{}, 0
	}
	count := map[int32]int{}
	for _, h := range hashes {
		for _, x := range c.archivesWith(h) {
			count[x]++
		}
	}
	best, bn := int32(-1), 0
	for x, n := range count {
		// the same files in several mods (a mod and its re-uploads): the archive with fewer extra files fits better
		if n > bn || n == bn && best >= 0 && c.Archives[x].N < c.Archives[best].N {
			best, bn = x, n
		}
	}
	if best < 0 {
		return CorpusArchive{}, 0
	}
	return c.Archives[best], float64(bn) / float64(len(hashes))
}

// ArchivesOf: the archives of one mod (all versions, by name)
func (c *Corpus) ArchivesOf(nexus int) []int {
	var out []int
	for i, a := range c.Archives {
		if a.Nexus == nexus {
			out = append(out, i)
		}
	}
	return out
}

// HashesOfMany: the game files of several corpus archives, in one pass over the data
func (c *Corpus) HashesOfMany(want map[int]bool) map[int][]uint64 {
	out := map[int][]uint64{}
	for _, x := range c.flat {
		if want[int(x.a)] {
			out[int(x.a)] = append(out[int(x.a)], x.h)
		}
	}
	return out
}

// TweakOthers: what the downloaded mods set a TweakDB value to: value -> Nexus ids (most common first)
type TweakOther struct {
	Value string `json:"value"`
	Mods  []int  `json:"mods"`
}

func (c *Corpus) TweakOthers(key string) []TweakOther {
	if c == nil {
		return nil
	}
	by := map[string][]int{}
	for n, kv := range c.Tweaks {
		if v, ok := kv[key]; ok {
			id, _ := strconv.Atoi(n)
			by[v] = append(by[v], id)
		}
	}
	var out []TweakOther
	for v, ids := range by {
		sort.Ints(ids)
		out = append(out, TweakOther{Value: v, Mods: ids})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Mods) != len(out[j].Mods) {
			return len(out[i].Mods) > len(out[j].Mods)
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// reHotGame: game functions that run very often (every frame, every input, every map pin update)
var reHotGame = regexp.MustCompile(`(?i)\.(OnAction|OnUpdate\w*|Update\w*|Tick\w*|OnTick|OnFrame|OnEnter|OnEvaluate|SetData|Refresh\w*)$`)

// HotWraps: the frequently-run game functions a mod wraps or replaces (its code then runs that often)
func (c *Corpus) HotWraps(nexus int) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, t := range append(append([]string(nil), c.Wraps[strconv.Itoa(nexus)]...), c.Replaces[strconv.Itoa(nexus)]...) {
		if reHotGame.MatchString(t) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// LuaEveryFrame: a mod's CET onUpdate handlers that work every frame (file, lines, gated, calls the game)
type LuaFrame struct {
	File  string `json:"file"`
	Lines int    `json:"lines"`
	Gated bool   `json:"gated"` // returns early in most frames
	Game  bool   `json:"game"`  // calls into the game
}

func (c *Corpus) LuaEveryFrame(nexus int) []LuaFrame {
	if c == nil {
		return nil
	}
	var out []LuaFrame
	for _, e := range c.Lua[strconv.Itoa(nexus)] {
		if len(e) < 4 {
			continue
		}
		p, _ := e[0].(string)
		l, _ := e[1].(float64)
		g, _ := e[2].(float64)
		cg, _ := e[3].(float64)
		out = append(out, LuaFrame{File: p, Lines: int(l), Gated: g != 0, Game: cg != 0})
	}
	return out
}

// SaveChanges: what a mod's scripts do to the save (calls by kind: GiveItem, RemoveItem, Teleport, SetFactValue ...)
func (c *Corpus) SaveChanges(nexus int) map[string]int {
	if c == nil {
		return nil
	}
	return c.Save[strconv.Itoa(nexus)]
}

// Variant: one resolution of a texture mod that comes in several (ETO 1K / 2K / 4K ...)
type Variant struct {
	FileID   int      `json:"file_id"`
	Name     string   `json:"name"`
	Res      string   `json:"res"`
	Bytes    int64    `json:"bytes"` // the download's size
	Archives []string `json:"archives"`
}

// VariantsOf: the resolutions Frame Lab knows for a mod (smallest first), nil when it knows one or none
func (c *Corpus) VariantsOf(nexus int) []Variant {
	if c == nil {
		return nil
	}
	var out []Variant
	for _, e := range c.Variants[strconv.Itoa(nexus)] {
		if len(e) < 5 {
			continue
		}
		v := Variant{}
		if f, ok := e[0].(float64); ok {
			v.FileID = int(f)
		}
		v.Name, _ = e[1].(string)
		v.Res, _ = e[2].(string)
		if f, ok := e[3].(float64); ok {
			v.Bytes = int64(f)
		}
		if l, ok := e[4].([]any); ok {
			for _, x := range l {
				if s, ok := x.(string); ok {
					v.Archives = append(v.Archives, s)
				}
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return resNum(out[i].Res) < resNum(out[j].Res) })
	return out
}

func resNum(r string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.ToUpper(r), "K"))
	return n
}

// Downloads: a mod's download count on Nexus when the corpus was made
func (c *Corpus) Downloads(nexus int) int {
	if c == nil {
		return 0
	}
	return c.DL[strconv.Itoa(nexus)]
}

// FindMods: mods whose name has every word of q, most downloaded first (for "Before you install")
func FindMods(x *Index, c *Corpus, q string, max int) [][2]any {
	if x == nil {
		return nil
	}
	words := strings.Fields(strings.ToLower(q))
	type hit struct {
		id   int
		name string
		dl   int
	}
	var hits []hit
	for k, v := range x.Mods {
		if len(v) == 0 {
			continue
		}
		low := strings.ToLower(v[0])
		ok := len(words) > 0
		for _, w := range words {
			if !strings.Contains(low, w) && k != w {
				ok = false
				break
			}
		}
		if ok {
			id, _ := strconv.Atoi(k)
			hits = append(hits, hit{id, v[0], c.Downloads(id)})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dl != hits[j].dl {
			return hits[i].dl > hits[j].dl
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([][2]any, len(hits))
	for i, h := range hits {
		out[i] = [2]any{h.id, h.name}
	}
	return out
}
