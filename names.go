package modmap

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"os"
)

// NamesFile is a gzip'd text list of game file paths (one per line). Frame Lab builds it once from
// WolvenKit's own path list (WolvenKit.Common.dll, Oodle-compressed) using the game's oo2ext DLL.
type NamesFile string

func (n NamesFile) Exists() bool {
	st, err := os.Stat(string(n))
	return err == nil && st.Size() > 1000
}

// Resolve finds the paths for the wanted hashes by hashing every listed path once (about a second).
func (n NamesFile) Resolve(want map[uint64]bool) (map[uint64]string, error) {
	out := map[uint64]string{}
	if len(want) == 0 {
		return out, nil
	}
	f, err := os.Open(string(n))
	if err != nil {
		return out, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return out, err
	}
	sc := bufio.NewScanner(bufio.NewReaderSize(zr, 1<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := bytes.TrimRight(sc.Bytes(), "\r")
		if len(line) == 0 {
			continue
		}
		h := PathHash(string(line))
		if want[h] {
			out[h] = string(line)
			if len(out) == len(want) {
				break
			}
		}
	}
	return out, sc.Err()
}

// KarkBlocks finds the embedded KARK resources in WolvenKit.Common.dll: each is "KARK" + u32 size + Oodle data,
// preceded by the .NET resource length.
type KarkBlock struct {
	Data  []byte // Oodle stream (after the 8-byte KARK header)
	USize int
}

func KarkBlocks(dll []byte) []KarkBlock {
	var out []KarkBlock
	le := binary.LittleEndian
	for i := 4; i+8 < len(dll); {
		j := bytes.Index(dll[i:], []byte("KARK"))
		if j < 0 {
			break
		}
		o := i + j
		i = o + 4
		resLen := int(le.Uint32(dll[o-4:]))
		usz := int(le.Uint32(dll[o+4:]))
		if resLen < 16 || o+resLen > len(dll) || usz <= 0 || usz > 600<<20 {
			continue
		}
		out = append(out, KarkBlock{Data: dll[o+8 : o+resLen], USize: usz})
	}
	return out
}

// WriteNames gzips a decompressed path list ("base\\...\r\n" lines) into the names file.
func WriteNames(path string, list []byte) error {
	if !bytes.HasPrefix(list, []byte("base\\")) {
		return errors.New("not a path list")
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if _, err := zw.Write(list); err != nil {
		f.Close()
		return err
	}
	zw.Close()
	f.Close()
	return os.Rename(tmp, path)
}
