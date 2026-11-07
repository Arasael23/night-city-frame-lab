package vram

// The game's own files, to see what a mod's file really adds: the base-game archives
// (archive/pc/content and archive/pc/ep1) are read like the mods' ones, file tables only (a few
// seconds for about 550,000 files). A file's data is read only to check whether a mod file is a
// copy of the game's own.

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"framelab/internal/modmap"
)

type vanFile struct {
	arc        uint16 // index into vanilla.arcs
	seg0, seg1 uint32 // segment range in that archive's table
	buf        uint64 // unpacked size of the parts after the header (what goes to the GPU)
}

type vanilla struct {
	arcs  []string       // full paths
	segs  [][]modmap.Seg // segment table per archive
	files map[uint64]vanFile
	err   []Broken // base-game archives that could not be read
}

// Broken: an archive whose file table can't be read or whose data runs past the end of the file.
type Broken struct {
	Archive string `json:"archive"`
	Reason  string `json:"reason"`
}

func loadVanilla(game string) *vanilla {
	v := &vanilla{files: map[uint64]vanFile{}}
	for _, sub := range []string{filepath.Join("archive", "pc", "content"), filepath.Join("archive", "pc", "ep1")} {
		ents, err := os.ReadDir(filepath.Join(game, sub))
		if err != nil {
			continue
		}
		var names []string
		for _, e := range ents {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".archive") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			p := filepath.Join(game, sub, n)
			es, segs, err := modmap.ArchiveIndex(p)
			if err == nil {
				err = checkArchive(p, segs)
			}
			if err != nil {
				v.err = append(v.err, Broken{Archive: filepath.ToSlash(filepath.Join(sub, n)), Reason: err.Error()})
				continue
			}
			ai := uint16(len(v.arcs))
			v.arcs = append(v.arcs, p)
			v.segs = append(v.segs, segs)
			for _, e := range es {
				if _, dup := v.files[e.Hash]; dup {
					continue // the first archive that has it is the one the game uses
				}
				var buf uint64
				for i := e.SegStart + 1; i < e.SegEnd && int(i) < len(segs); i++ {
					buf += uint64(segs[i].Size)
				}
				v.files[e.Hash] = vanFile{arc: ai, seg0: e.SegStart, seg1: e.SegEnd, buf: buf}
			}
		}
	}
	return v
}

// checkArchive: every piece of data the table points at has to be inside the file. A download that
// was cut off or a disk error leaves an archive the game can't read (or crashes on).
func checkArchive(path string, segs []modmap.Seg) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	size := uint64(fi.Size())
	for _, s := range segs {
		if s.Offset+uint64(s.ZSize) > size {
			return fmt.Errorf("data runs past the end of the file (%s of %s): the file is cut off", mbText(s.Offset+uint64(s.ZSize)), mbText(size))
		}
	}
	return nil
}

func mbText(b uint64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }

// sameAsGame: is a mod's file a copy of the game's own? Same unpacked sizes first (cheap), then the
// stored bytes; when those differ (packed again by a mod tool), the unpacked bytes, with Oodle.
// budget limits how much data a scan unpacks for this.
func (v *vanilla) sameAsGame(modFile *os.File, e modmap.Entry, msegs []modmap.Seg, vf vanFile, dec func([]byte, int) ([]byte, error), budget *int64) bool {
	vs := v.segs[vf.arc]
	if e.SegEnd-e.SegStart != vf.seg1-vf.seg0 || int(e.SegEnd) > len(msegs) || int(vf.seg1) > len(vs) {
		return false
	}
	for i := uint32(0); i < e.SegEnd-e.SegStart; i++ {
		if msegs[e.SegStart+i].Size != vs[vf.seg0+i].Size {
			return false
		}
	}
	vfh, err := os.Open(v.arcs[vf.arc])
	if err != nil {
		return false
	}
	defer vfh.Close()
	if a := modmap.ContentKey(modFile, e, msegs); a != "" && a == modmap.ContentKey(vfh, modmap.Entry{SegStart: vf.seg0, SegEnd: vf.seg1}, vs) {
		return true
	}
	if dec == nil {
		return false
	}
	for i := uint32(0); i < e.SegEnd-e.SegStart; i++ {
		a, b := msegs[e.SegStart+i], vs[vf.seg0+i]
		if *budget < int64(a.Size) {
			return false
		}
		*budget -= int64(a.Size)
		x, err1 := unpack(modFile, a, dec)
		y, err2 := unpack(vfh, b, dec)
		if err1 != nil || err2 != nil || sha1.Sum(x) != sha1.Sum(y) {
			return false
		}
	}
	return true
}

// unpack one stored piece: raw, or "KARK" + unpacked size + Oodle data
func unpack(f *os.File, s modmap.Seg, dec func([]byte, int) ([]byte, error)) ([]byte, error) {
	if s.ZSize > 256<<20 || s.Size > 256<<20 {
		return nil, errors.New("too big")
	}
	raw := make([]byte, s.ZSize)
	if _, err := f.ReadAt(raw, int64(s.Offset)); err != nil && err != io.EOF {
		return nil, err
	}
	if s.ZSize >= s.Size {
		return raw, nil
	}
	if len(raw) < 8 || !bytes.Equal(raw[:4], []byte("KARK")) {
		return nil, errors.New("unknown packing")
	}
	return dec(raw[8:], int(binary.LittleEndian.Uint32(raw[4:])))
}
