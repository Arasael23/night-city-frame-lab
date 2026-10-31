// Package modmap ties recorded file hashes and script owners back to mods:
// which .archive holds a file, which library zip holds that archive, and the file's real path.
package modmap

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PathHash is the game's resource path hash: FNV-1a 64 of the lower-case path with backslashes.
func PathHash(p string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' {
			c = '\\'
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// ArchiveHashes reads the file table of a Cyberpunk .archive (RDAR) and returns the path hashes it contains.
func ArchiveHashes(path string) ([]uint64, error) {
	es, err := ArchiveEntries(path)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, len(es))
	for i, e := range es {
		out[i] = e.Hash
	}
	return out, nil
}

// Entry: one file in an archive: its path hash and where its data is (segment range in Segs).
// (The table also has a SHA-1 field, but mod tools write the same value for every file, so it can't
// tell files apart; ContentKey reads the data itself.)
type Entry struct {
	Hash             uint64
	SegStart, SegEnd uint32
}

// Seg: one stored piece of a file (raw, usually compressed, bytes at Offset).
type Seg struct {
	Offset      uint64
	ZSize, Size uint32
}

// ArchiveEntries reads the file table of a Cyberpunk .archive (RDAR): 56-byte records of
// hash u64, timestamp u64, inline segments u32, segments start/end u32, dependencies start/end u32, SHA-1.
func ArchiveEntries(path string) ([]Entry, error) {
	es, _, err := ArchiveIndex(path)
	return es, err
}

// ArchiveIndex also returns the segment table (16-byte records after the file records).
func ArchiveIndex(path string) ([]Entry, []Seg, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var hdr [40]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return nil, nil, err
	}
	if string(hdr[:4]) != "RDAR" {
		return nil, nil, errors.New("not an archive")
	}
	le := binary.LittleEndian
	indexPos := int64(le.Uint64(hdr[8:]))
	indexSize := int64(le.Uint32(hdr[16:]))
	if indexSize < 28 || indexSize > 512<<20 {
		return nil, nil, errors.New("bad index size")
	}
	idx := make([]byte, indexSize)
	if _, err := f.ReadAt(idx, indexPos); err != nil {
		return nil, nil, err
	}
	// index: tableOffset u32, tableSize u32, crc u64, fileCount u32, segmentCount u32, depCount u32, then 56-byte file records
	count := int(le.Uint32(idx[16:]))
	off := 28
	if off+count*56 > len(idx) {
		return nil, nil, errors.New("truncated index")
	}
	out := make([]Entry, count)
	for i := 0; i < count; i++ {
		rec := idx[off+i*56:]
		out[i].Hash = le.Uint64(rec)
		out[i].SegStart = le.Uint32(rec[20:])
		out[i].SegEnd = le.Uint32(rec[24:])
	}
	segCount := int(le.Uint32(idx[20:]))
	soff := off + count*56
	var segs []Seg
	if segCount > 0 && soff+segCount*16 <= len(idx) {
		segs = make([]Seg, segCount)
		for i := range segs {
			rec := idx[soff+i*16:]
			segs[i] = Seg{Offset: le.Uint64(rec), ZSize: le.Uint32(rec[8:]), Size: le.Uint32(rec[12:])}
		}
	}
	return out, segs, nil
}

// ContentKey: a SHA-1 over a file's stored bytes (all its segments), to see whether two archives carry
// the same file byte for byte. Empty when it can't be read.
func ContentKey(f *os.File, e Entry, segs []Seg) string {
	if e.SegEnd <= e.SegStart || int(e.SegEnd) > len(segs) {
		return ""
	}
	h := sha1.New()
	var n [8]byte
	for i := e.SegStart; i < e.SegEnd; i++ {
		s := segs[i]
		if s.ZSize > 256<<20 {
			return ""
		}
		binary.LittleEndian.PutUint32(n[:], s.Size)
		h.Write(n[:4])
		if _, err := io.Copy(h, io.NewSectionReader(f, int64(s.Offset), int64(s.ZSize))); err != nil {
			return ""
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BuildArchives maps every file hash in a build's mod archives to the archive's relative path.
// Base-game archives (archive/pc/content, ep1) are skipped: anything not found is base game.
type BuildArchives struct {
	Game     string
	ByHash   map[uint64]string // hash -> "archive/pc/mod/X.archive"
	Size     map[uint64]uint32 // hash -> unpacked size of the copy that is used
	Archives []string
}

func ScanBuild(game string) *BuildArchives {
	b := &BuildArchives{Game: game, ByHash: map[uint64]string{}, Size: map[uint64]uint32{}}
	var files []string
	if m, _ := filepath.Glob(filepath.Join(game, "archive", "pc", "mod", "*.archive")); m != nil {
		files = append(files, m...)
	}
	if m, _ := filepath.Glob(filepath.Join(game, "mods", "*", "archives", "*.archive")); m != nil {
		files = append(files, m...)
	}
	sort.Strings(files)
	for _, p := range files {
		rel, _ := filepath.Rel(game, p)
		rel = filepath.ToSlash(rel)
		es, segs, err := ArchiveIndex(p)
		if err != nil {
			continue
		}
		b.Archives = append(b.Archives, rel)
		for _, e := range es {
			if _, seen := b.ByHash[e.Hash]; !seen { // first archive in load order wins
				b.ByHash[e.Hash] = rel
				var n uint32
				for i := e.SegStart; i < e.SegEnd && int(i) < len(segs); i++ {
					n += segs[i].Size
				}
				b.Size[e.Hash] = n
			}
		}
	}
	return b
}

// ArchiveLabel turns "archive/pc/mod/#Better_Trees_2K.archive" into "Better_Trees_2K".
func ArchiveLabel(rel string) string {
	base := filepath.Base(rel)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimLeft(base, "#!_ ")
	if base == "" {
		base = filepath.Base(rel)
	}
	return base
}
