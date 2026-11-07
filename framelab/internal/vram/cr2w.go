package vram

import (
	"encoding/binary"
	"errors"
)

// texDims reads width and height of a texture (CBitmapTexture) from its CR2W header part:
// header, then 10 tables of (offset, count, crc); table 0 = strings, 1 = names (string offset +
// hash), 4 = exports (24 bytes each: class name index, flags, parent, data size, data offset, ...).
// An export's data is a 0 byte, then properties: name index u16, type index u16, size u32
// (counting itself), value; a name index of 0 ends the list.
func texDims(b []byte) (w, h uint32, err error) {
	le := binary.LittleEndian
	if len(b) < 0x28+120 || string(b[:4]) != "CR2W" {
		return 0, 0, errors.New("not CR2W")
	}
	tab := func(i int) (off, cnt uint32) {
		p := 0x28 + 12*i
		return le.Uint32(b[p:]), le.Uint32(b[p+4:])
	}
	so, sc := tab(0)
	no, nc := tab(1)
	eo, ec := tab(4)
	if int(so+sc) > len(b) || int(no+nc*8) > len(b) || int(eo+ec*24) > len(b) || ec == 0 {
		return 0, 0, errors.New("bad tables")
	}
	name := func(i uint16) string {
		if uint32(i) >= nc {
			return ""
		}
		o := so + le.Uint32(b[no+8*uint32(i):])
		e := o
		for e < so+sc && b[e] != 0 {
			e++
		}
		return string(b[o:e])
	}
	cls := le.Uint16(b[eo:])
	if name(cls) != "CBitmapTexture" {
		return 0, 0, errors.New("not a bitmap texture")
	}
	dsz, doff := le.Uint32(b[eo+8:]), le.Uint32(b[eo+12:])
	if int(doff+dsz) > len(b) || dsz < 1 {
		return 0, 0, errors.New("bad export")
	}
	d := b[doff : doff+dsz]
	p := 1
	for p+8 <= len(d) {
		ni := le.Uint16(d[p:])
		if ni == 0 {
			break
		}
		size := le.Uint32(d[p+4:])
		if size < 4 || p+4+int(size) > len(d) {
			break
		}
		val := d[p+8 : p+4+int(size)]
		switch name(ni) {
		case "width":
			if len(val) >= 4 {
				w = le.Uint32(val)
			}
		case "height":
			if len(val) >= 4 {
				h = le.Uint32(val)
			}
		}
		p += 4 + int(size)
	}
	if w == 0 || h == 0 {
		return 0, 0, errors.New("no size")
	}
	return w, h, nil
}

// oneLevel: the data is exactly one level of a w x h texture in a usual format (BC blocks of 8 or
// 16 bytes, or 1/2/4/8/16 bytes per pixel) = no mip levels.
func oneLevel(w, h uint32, size uint32) bool {
	blocks := uint64((w+3)/4) * uint64((h+3)/4)
	px := uint64(w) * uint64(h)
	s := uint64(size)
	return s == blocks*8 || s == blocks*16 || s == px || s == px*2 || s == px*4 || s == px*8 || s == px*16
}

// meshLods (2.0): how many LOD levels a mesh has (CMesh.lodLevelInfo: one distance per level).
// -1 = not a mesh / not readable.
func meshLods(b []byte) int {
	le := binary.LittleEndian
	if len(b) < 0x28+120 || string(b[:4]) != "CR2W" {
		return -1
	}
	tab := func(i int) (off, cnt uint32) {
		p := 0x28 + 12*i
		return le.Uint32(b[p:]), le.Uint32(b[p+4:])
	}
	so, sc := tab(0)
	no, nc := tab(1)
	eo, ec := tab(4)
	if int(so+sc) > len(b) || int(no+nc*8) > len(b) || int(eo+ec*24) > len(b) || ec == 0 {
		return -1
	}
	name := func(i uint16) string {
		if uint32(i) >= nc {
			return ""
		}
		o := so + le.Uint32(b[no+8*uint32(i):])
		e := o
		for e < so+sc && b[e] != 0 {
			e++
		}
		return string(b[o:e])
	}
	if name(le.Uint16(b[eo:])) != "CMesh" {
		return -1
	}
	dsz, doff := le.Uint32(b[eo+8:]), le.Uint32(b[eo+12:])
	if int(doff+dsz) > len(b) || dsz < 1 {
		return -1
	}
	d := b[doff : doff+dsz]
	for p := 1; p+8 <= len(d); {
		ni := le.Uint16(d[p:])
		if ni == 0 {
			break
		}
		size := le.Uint32(d[p+4:])
		if size < 4 || p+4+int(size) > len(d) {
			break
		}
		val := d[p+8 : p+4+int(size)]
		if name(ni) == "lodLevelInfo" && len(val) >= 4 {
			return int(le.Uint32(val))
		}
		p += 4 + int(size)
	}
	return 1 // no lodLevelInfo: one level
}

// mip0Bytes (2.0): the size of the top (biggest) level in a texture's data, worked out from its
// size in pixels and the data size: the level sizes of a BC (8 / 16 bytes per 4x4 block) or an
// uncompressed (1-16 bytes per pixel) mip chain are added up until they match. 0 = no match.
func mip0Bytes(w, h, size uint32) uint64 {
	if w == 0 || h == 0 {
		return 0
	}
	type fmtT struct {
		block bool
		bytes uint64
	}
	// exact first; then allow a little padding after the chain (at most 0.5%), never less data
	for pass := 0; pass < 2; pass++ {
		tol := uint64(0)
		if pass == 1 {
			tol = uint64(size) / 200
		}
		for _, f := range []fmtT{{true, 8}, {true, 16}, {false, 1}, {false, 2}, {false, 4}, {false, 8}, {false, 16}} {
			lvl := func(i uint) uint64 {
				ww, hh := uint64(w)>>i, uint64(h)>>i
				if ww < 1 {
					ww = 1
				}
				if hh < 1 {
					hh = 1
				}
				if f.block {
					return ((ww + 3) / 4) * ((hh + 3) / 4) * f.bytes
				}
				return ww * hh * f.bytes
			}
			var sum uint64
			for i := uint(0); i < 16; i++ {
				sum += lvl(i)
				if sum > uint64(size) {
					break
				}
				if uint64(size)-sum <= tol {
					if i == 0 {
						return 0 // only one level: nothing to drop
					}
					return lvl(0)
				}
			}
		}
	}
	return 0
}
