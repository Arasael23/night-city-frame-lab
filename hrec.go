// Package hrec reads HitchRecorder .bin recordings (format 1, 32-byte records).
package hrec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

const (
	KFrame        = 0
	KLoad         = 1
	KLoaderUpdate = 2
	KDeser        = 3
	KSector       = 4
	KEntity       = 5
	KSerLoad      = 6
	KPsoGfx       = 10
	KPsoComp      = 11
	KPsoStream    = 12
	KStateObj     = 13
	KAddStateObj  = 14
	KPsoLib       = 15
	KResCommit    = 20
	KResPlaced    = 21
	KHeap         = 22
	KVram         = 23
	KMark         = 30
	// HitchRecorder 0.3: script time per mod, one record per 50 ms bucket and owner
	// (Hash = owner index from <recording>.owners.txt, DurUs = exclusive time in the bucket)
	KScript     = 40 // REDscript, all threads
	KScriptMain = 41 // REDscript, main thread only
	KCet        = 42 // CET Lua, all threads (owners "c<n>" in the owners file)
	KCetMain    = 43 // CET Lua, main thread only
)

// Mark kinds (Rec.Extra of a KMark record).
const (
	MarkRunning      = 1
	MarkD3DHooks     = 2
	MarkKey          = 3
	MarkCaptureStart = 4
	MarkCaptureStop  = 5
	MarkFocusStart   = 6 // session mode: F11 marks the part to look at
	MarkFocusStop    = 7
)

var KindName = map[uint8]string{
	KFrame: "frame", KLoad: "load request", KLoaderUpdate: "resource loader update", KDeser: "file read + deserialise",
	KSector: "world sector", KEntity: "entity spawn", KSerLoad: "file load start",
	KPsoGfx: "shader pipeline (graphics)", KPsoComp: "shader pipeline (compute)", KPsoStream: "shader pipeline (stream)",
	KStateObj: "ray tracing pipeline", KAddStateObj: "ray tracing pipeline (add)", KPsoLib: "pipeline cache lookup",
	KResCommit: "GPU texture/buffer", KResPlaced: "GPU texture/buffer (placed)", KHeap: "GPU memory heap",
	KVram: "VRAM sample", KMark: "marker", KScript: "REDscript (per mod)", KScriptMain: "REDscript, main thread (per mod)", KCet: "CET Lua (per mod)", KCetMain: "CET Lua, main thread (per mod)",
}

// IsShader reports kinds that build shader/raytracing pipelines.
func IsShader(k uint8) bool { return k >= KPsoGfx && k <= KAddStateObj }

// IsGpuMem reports GPU resource/heap creation kinds.
func IsGpuMem(k uint8) bool { return k == KResCommit || k == KResPlaced || k == KHeap }

// IsPath reports kinds whose Hash is a resource path hash.
func IsPath(k uint8) bool {
	return k == KLoad || k == KDeser || k == KSector || k == KEntity || k == KSerLoad
}

type Header struct {
	Version       uint32
	QpcFreq       uint64
	Qpc0          uint64
	FileTime0     uint64 // UTC FILETIME at Qpc0
	GameMajor     uint16
	GameMinor     uint16
	GamePatch     uint32
	Hooks         uint32
	RecSize       uint32
	SpikeMs       uint32
	PluginVersion uint32
	CaptureMode   uint8
}

type Rec struct {
	TUs   uint64
	Hash  uint64
	Frame uint32
	DurUs uint32
	Kind  uint8
	Flags uint8
	Extra uint16
	Tid   uint32
}

func (r *Rec) Main() bool   { return r.Flags&1 != 0 }
func (r *Rec) Failed() bool { return r.Flags&2 != 0 }

type File struct {
	Header Header
	Recs   []Rec
}

// UnixMs0 returns the Unix time in ms at Qpc0.
func (h *Header) UnixMs0() float64 {
	// FILETIME: 100 ns ticks since 1601-01-01
	return float64(h.FileTime0)/10000.0 - 11644473600000.0
}

// Qpc converts a record time (us since Qpc0) to a QPC value.
func (h *Header) Qpc(tUs uint64) int64 {
	return int64(h.Qpc0) + int64(float64(tUs)*float64(h.QpcFreq)/1e6)
}

func (h *Header) Version3() string {
	v := h.PluginVersion
	return fmt.Sprintf("%d.%d.%d", v/10000, v/100%100, v%100)
}

func Read(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*File, error) {
	if len(b) < 64 || string(b[0:4]) != "HREC" {
		return nil, errors.New("not a HitchRecorder recording")
	}
	le := binary.LittleEndian
	f := &File{}
	h := &f.Header
	h.Version = le.Uint32(b[4:])
	h.QpcFreq = le.Uint64(b[8:])
	h.Qpc0 = le.Uint64(b[16:])
	h.FileTime0 = le.Uint64(b[24:])
	h.GameMajor = le.Uint16(b[32:])
	h.GameMinor = le.Uint16(b[34:])
	h.GamePatch = le.Uint32(b[36:])
	h.Hooks = le.Uint32(b[40:])
	h.RecSize = le.Uint32(b[44:])
	h.SpikeMs = le.Uint32(b[48:])
	h.PluginVersion = le.Uint32(b[52:])
	h.CaptureMode = b[56]
	if h.RecSize != 32 || h.QpcFreq == 0 {
		return nil, fmt.Errorf("unsupported recording (record size %d)", h.RecSize)
	}
	n := (len(b) - 64) / 32
	f.Recs = make([]Rec, n)
	for i := 0; i < n; i++ {
		o := 64 + i*32
		r := &f.Recs[i]
		r.TUs = le.Uint64(b[o:])
		r.Hash = le.Uint64(b[o+8:])
		r.Frame = le.Uint32(b[o+16:])
		r.DurUs = le.Uint32(b[o+20:])
		r.Kind = b[o+24]
		r.Flags = b[o+25]
		r.Extra = le.Uint16(b[o+26:])
		r.Tid = le.Uint32(b[o+28:])
	}
	return f, nil
}

// LatestHooks returns the hook mask including D3D12 hooks added after the header was written.
func (f *File) LatestHooks() uint32 {
	h := f.Header.Hooks
	for i := range f.Recs {
		r := &f.Recs[i]
		if r.Kind == KMark && r.Extra == MarkD3DHooks {
			h = uint32(r.Hash)
		}
	}
	return h
}
