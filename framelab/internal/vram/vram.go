// Package vram works out how much GPU memory each mod's files take, from the archives alone
// (no game, nothing unpacked): in a Cyberpunk archive every file is stored as a header part plus
// "buffers", and for textures and meshes the buffers are exactly the data that goes to VRAM
// (the texture with its mip levels, the mesh's vertex and index data). The archive's file table
// gives each buffer's unpacked size, so the totals are real sizes, not guesses.
//
// Textures saved without mip levels have a buffer of exactly width x height x bytes per pixel
// (no smaller copies after it). Those are always loaded at full size, even far away, so they are
// listed: a texture made with mips is about 1.33 times that size and never matches exactly.
package vram

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"framelab/internal/conflicts"
	"framelab/internal/modmap"
)

type File struct {
	Name   string  `json:"name"` // path, or the hash when the name isn't known
	MB     float64 `json:"mb"`
	Kind   string  `json:"kind"`           // "texture", "mesh", "other"
	Size   string  `json:"size,omitempty"` // "≈4K" etc. (textures)
	Dims   string  `json:"dims,omitempty"` // "3728 x 3680" when read from the texture's header
	NoMips bool    `json:"no_mips,omitempty"`
	Loaded bool    `json:"loaded,omitempty"` // the game loaded it in the last session
	Used   bool    `json:"used"`             // this copy is the one the game uses (first in load order)
}

type Mod struct {
	Label    string   `json:"label"`
	Archives []string `json:"archives"`
	Files    int      `json:"files"`
	GpuFiles int      `json:"gpu_files"`
	MB       float64  `json:"mb"` // GPU data of the copies the game uses
	TexMB    float64  `json:"tex_mb"`
	MeshMB   float64  `json:"mesh_mb"`
	OtherMB  float64  `json:"other_mb"`
	ShadowMB float64  `json:"shadowed_mb"` // copies another archive wins over (never used)
	LoadedMB float64  `json:"loaded_mb"`   // used in the last session
	NoMips   int      `json:"no_mips"`
	NoMipsMB float64  `json:"no_mips_mb"`
	Tex4K    int      `json:"tex_4k"` // textures of 4K class or bigger
	Big      []File   `json:"big"`    // biggest files
}

type Report struct {
	Build     string    `json:"build"`
	Game      string    `json:"game"`
	Generated time.Time `json:"generated"`
	TookS     float64   `json:"took_s"`
	Session   string    `json:"session,omitempty"`
	Archives  int       `json:"archives"`
	TotalMB   float64   `json:"total_mb"`
	LoadedMB  float64   `json:"loaded_mb"`
	NoMips    int       `json:"no_mips"`
	NoMipsMB  float64   `json:"no_mips_mb"`
	Mods      []Mod     `json:"mods"`
}

// exact sizes of one mip level: width x height x bytes per pixel, powers of two from 256 to 16384.
// When a size fits several shapes, the likeliest wins: square before 2:1 before longer, and 1 byte
// per pixel (BC3/BC5/BC7) before 0.5 (BC1/BC4) before the uncompressed formats.
var topOnly = func() map[uint32]string {
	type cand struct {
		side  float64
		score int
	}
	best := map[uint32]cand{}
	sides := []uint32{256, 512, 1024, 2048, 4096, 8192, 16384}
	bppRank := map[uint32]int{2: 0, 1: 1, 8: 2, 4: 3, 16: 4} // bytes per pixel x2
	for _, w := range sides {
		for _, h := range sides {
			for b2, br := range bppRank {
				v := uint64(w) * uint64(h) * uint64(b2) / 2
				if v < 1<<20 || v >= 1<<32 {
					continue
				}
				aspect := 0
				for x, y := w, h; x != y; aspect++ {
					if x > y {
						x /= 2
					} else {
						y /= 2
					}
				}
				sc := aspect*10 + br
				if c, ok := best[uint32(v)]; !ok || sc < c.score {
					best[uint32(v)] = cand{math.Max(float64(w), float64(h)), sc}
				}
			}
		}
	}
	m := map[uint32]string{}
	for v, c := range best {
		m[v] = sizeClass(c.side)
	}
	return m
}()

func sizeClass(side float64) string {
	switch {
	case side >= 8192:
		return "≈8K"
	case side >= 4096:
		return "≈4K"
	case side >= 2048:
		return "≈2K"
	case side >= 1024:
		return "≈1K"
	}
	return "small"
}

// texture size class from its data size, assuming 1 byte per pixel (BC3/BC5/BC7, the usual) and mips
func texClass(bytes float64) string {
	return sizeClass(math.Sqrt(bytes / 1.3333))
}

type Options struct {
	// Decompress unpacks an Oodle/Kraken stream to size bytes (the game's oo2ext DLL on Windows).
	// With it, textures are checked against their real width and height; without it, only
	// power-of-two sizes are recognised.
	Decompress func(src []byte, size int) ([]byte, error)
	Build      string
	Game       string
	Library    *modmap.Library
	Names      modmap.NamesFile
	Loaded     map[uint64]bool
	Session    string
}

func Scan(o Options) *Report {
	start := time.Now()
	r := &Report{Build: o.Build, Game: o.Game, Generated: time.Now(), Session: o.Session, Mods: []Mod{}}
	order, _ := conflicts.LoadOrder(o.Game)
	r.Archives = len(order)
	type fileAt struct {
		mod  int
		mb   float64
		buf  []uint32
		used bool
		w, h uint32 // texture size from its header (0 = not read)
	}
	mods := map[string]*Mod{}
	var modList []*Mod
	winner := map[uint64]bool{}
	perMod := map[*Mod][]struct {
		hash uint64
		f    fileAt
	}{}
	wantNames := map[uint64]bool{}
	for _, rel := range order {
		es, segs, err := modmap.ArchiveIndex(filepath.Join(o.Game, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		lab := modmap.ArchiveLabel(rel)
		if o.Library != nil {
			if l, _ := o.Library.Find("archive:" + strings.ToLower(filepath.Base(rel))); l != "" {
				lab = l
			}
		}
		m := mods[lab]
		if m == nil {
			m = &Mod{Label: lab}
			mods[lab] = m
			modList = append(modList, m)
		}
		m.Archives = append(m.Archives, rel)
		var fh *os.File
		if o.Decompress != nil {
			fh, _ = os.Open(filepath.Join(o.Game, filepath.FromSlash(rel)))
		}
		for _, e := range es {
			m.Files++
			if e.SegEnd <= e.SegStart+1 || int(e.SegEnd) > len(segs) {
				continue // no buffers: nothing for the GPU
			}
			var bufs []uint32
			var b float64
			for i := e.SegStart + 1; i < e.SegEnd; i++ {
				bufs = append(bufs, segs[i].Size)
				b += float64(segs[i].Size)
			}
			used := !winner[e.Hash]
			winner[e.Hash] = true
			fa := fileAt{mb: b / (1 << 20), buf: bufs, used: used}
			if fh != nil && len(bufs) == 1 && bufs[0] >= 64<<10 {
				fa.w, fa.h = readDims(fh, segs[e.SegStart], o.Decompress)
			}
			perMod[m] = append(perMod[m], struct {
				hash uint64
				f    fileAt
			}{e.Hash, fa})
			wantNames[e.Hash] = true
		}
		if fh != nil {
			fh.Close()
		}
	}
	names := map[uint64]string{}
	if o.Names.Exists() {
		names, _ = o.Names.Resolve(wantNames)
	}
	for _, m := range modList {
		var files []File
		for _, x := range perMod[m] {
			nm := names[x.hash]
			kind := "other"
			switch strings.ToLower(filepath.Ext(nm)) {
			case ".xbm", ".mlmask", ".texarray", ".cubemap", ".envprobe":
				kind = "texture"
			case ".mesh", ".morphtarget":
				kind = "mesh"
			case "":
				// unknown name: one big buffer is how textures are stored
				if x.f.w > 0 || (len(x.f.buf) == 1 && x.f.mb >= 0.25) {
					kind = "texture"
				}
			}
			f := File{Name: nm, MB: round2(x.f.mb), Kind: kind, Used: x.f.used, Loaded: o.Loaded != nil && o.Loaded[x.hash]}
			if f.Name == "" {
				f.Name = "file " + hex16(x.hash) + " (name not in the list)"
			}
			if kind == "texture" && x.f.w > 0 {
				// real size from the texture's header
				f.Size = sizeClass(math.Max(float64(x.f.w), float64(x.f.h)))
				f.Dims = fmtDims(x.f.w, x.f.h)
				f.NoMips = len(x.f.buf) == 1 && oneLevel(x.f.w, x.f.h, x.f.buf[0]) && !mipsNotNeeded(nm) && !strings.Contains(strings.ToLower(m.Label), "lut")
			} else if kind == "texture" {
				f.Size = texClass(x.f.mb * (1 << 20))
				if len(x.f.buf) == 1 {
					if c, ok := topOnly[x.f.buf[0]]; ok {
						f.Size = c
						f.NoMips = !mipsNotNeeded(nm) && !strings.Contains(strings.ToLower(lab0(m.Label)), "lut")
					}
				}
			}
			if !x.f.used {
				m.ShadowMB += x.f.mb
				continue
			}
			m.GpuFiles++
			m.MB += x.f.mb
			switch kind {
			case "texture":
				m.TexMB += x.f.mb
				if f.NoMips {
					m.NoMips++
					m.NoMipsMB += x.f.mb
				}
				if f.Size == "≈4K" || f.Size == "≈8K" {
					m.Tex4K++
				}
			case "mesh":
				m.MeshMB += x.f.mb
			default:
				m.OtherMB += x.f.mb
			}
			if f.Loaded {
				m.LoadedMB += x.f.mb
			}
			files = append(files, f)
		}
		sort.Slice(files, func(i, j int) bool { return files[i].MB > files[j].MB })
		if len(files) > 25 {
			files = files[:25]
		}
		m.Big = files
		m.MB, m.TexMB, m.MeshMB, m.OtherMB = round2(m.MB), round2(m.TexMB), round2(m.MeshMB), round2(m.OtherMB)
		m.ShadowMB, m.LoadedMB, m.NoMipsMB = round2(m.ShadowMB), round2(m.LoadedMB), round2(m.NoMipsMB)
		if m.MB < 0.05 && m.ShadowMB < 0.05 {
			continue
		}
		r.TotalMB += m.MB
		r.LoadedMB += m.LoadedMB
		r.NoMips += m.NoMips
		r.NoMipsMB += m.NoMipsMB
		r.Mods = append(r.Mods, *m)
	}
	sort.Slice(r.Mods, func(i, j int) bool { return r.Mods[i].MB > r.Mods[j].MB })
	r.TotalMB, r.LoadedMB, r.NoMipsMB = round2(r.TotalMB), round2(r.LoadedMB), round2(r.NoMipsMB)
	r.TookS = math.Round(time.Since(start).Seconds()*10) / 10
	return r
}

// textures that are always drawn at one size don't need mip levels: UI, icons, fonts, colour LUTs
func mipsNotNeeded(name string) bool {
	n := strings.ToLower(filepath.ToSlash(name))
	for _, w := range []string{"/ui/", "/gui/", "/hud/", "icon", "atlas", "font", "lut", "/fullscreen/", "/inkwidget", "loading_screen", "/menu"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

func lab0(s string) string { return s }

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func hex16(h uint64) string {
	const d = "0123456789abcdef"
	b := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		b[i] = d[h&15]
		h >>= 4
	}
	return string(b)
}

func fmtDims(w, h uint32) string { return fmt.Sprintf("%d x %d", w, h) }

// readDims reads a file's header part (its first segment) and the texture size in it.
func readDims(f *os.File, s modmap.Seg, dec func([]byte, int) ([]byte, error)) (w, h uint32) {
	if s.ZSize == 0 || s.ZSize > 4<<20 || s.Size > 16<<20 {
		return 0, 0
	}
	raw := make([]byte, s.ZSize)
	if _, err := f.ReadAt(raw, int64(s.Offset)); err != nil && err != io.EOF {
		return 0, 0
	}
	b := raw
	if s.ZSize < s.Size { // "KARK" + unpacked size + Oodle data
		if len(raw) < 8 || string(raw[:4]) != "KARK" {
			return 0, 0
		}
		n := int(binary.LittleEndian.Uint32(raw[4:]))
		out, err := dec(raw[8:], n)
		if err != nil {
			return 0, 0
		}
		b = out
	}
	w, h, err := texDims(b)
	if err != nil {
		return 0, 0
	}
	return w, h
}
