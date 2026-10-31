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
