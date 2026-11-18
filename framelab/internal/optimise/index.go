// Package optimise is the Optimise tab (2.5): it reads a build's mod files, recognises the mods and
// lists what needs attention. Step 1 only reads; nothing in the game folder is changed.
package optimise

import (
	"compress/gzip"
	"encoding/json"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Index: what Frame Lab knows about Nexus mods without going online (built from downloaded Nexus
// files: names, which files each upload ships with their sizes, requirements, REDscript modules).
// Only names, paths and sizes are kept, no file contents.
type Index struct {
	Mods    map[string][]string `json:"mods"`    // nexus id -> [name, class]
	Files   [][]any             `json:"files"`   // upload -> [nexus id, file id, version]
	NSig    []int               `json:"nsig"`    // upload -> how many signature files it has
	Sig     map[string][]int    `json:"sig"`     // hash of "path|size" -> uploads that ship that exact file
	Reqs    map[string][]int    `json:"reqs"`    // nexus id -> nexus ids it lists as requirements
	Modules map[string][]int    `json:"modules"` // REDscript module -> nexus ids that declare it
	MPath   map[string][]string `json:"mpath"`   // nexus id -> hashes of a few of its file paths (any version)

	sig        map[uint64][]int
	mpath      map[uint64]int // path hash -> nexus id
	dependents map[int]int    // nexus id -> how many mods list it as a requirement
}

type upload struct {
	Nexus   int
	FileID  int
	Version string
}

func (x *Index) upload(i int) upload {
	if i < 0 || i >= len(x.Files) || len(x.Files[i]) < 3 {
		return upload{}
	}
	f := x.Files[i]
	n, _ := f[0].(float64)
	fid, _ := f[1].(float64)
	v, _ := f[2].(string)
	return upload{int(n), int(fid), v}
}

// ModName: the Nexus name of a mod, or "".
func (x *Index) ModName(id int) string {
	if x == nil {
		return ""
	}
	if m := x.Mods[strconv.Itoa(id)]; len(m) > 0 {
		return m[0]
	}
	return ""
}

var (
	idxMu    sync.Mutex
	idxCache = map[string]*Index{}
)

// LoadIndex reads the index file (gzipped JSON), once per path.
func LoadIndex(path string) (*Index, error) {
	idxMu.Lock()
	defer idxMu.Unlock()
	if x := idxCache[path]; x != nil {
		return x, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	x := &Index{}
	if err := json.NewDecoder(zr).Decode(x); err != nil {
		return nil, err
	}
	x.sig = make(map[uint64][]int, len(x.Sig))
	for k, v := range x.Sig {
		if h, err := strconv.ParseUint(k, 16, 64); err == nil {
			x.sig[h] = v
		}
	}
	x.Sig = nil
	x.mpath = map[uint64]int{}
	for id, hs := range x.MPath {
		n, _ := strconv.Atoi(id)
		for _, k := range hs {
			if h, err := strconv.ParseUint(k, 16, 64); err == nil {
				x.mpath[h] = n
			}
		}
	}
	x.MPath = nil
	x.dependents = map[int]int{}
	for _, reqs := range x.Reqs {
		for _, r := range reqs {
			x.dependents[r]++
		}
	}
	idxCache[path] = x
	return x, nil
}

func fnv64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// sigOf: the key a file is matched by (path relative to the game folder, lower case, forward slashes).
func sigOf(rel string, size int64) uint64 {
	return fnv64(strings.ToLower(rel) + "|" + strconv.FormatInt(size, 10))
}
