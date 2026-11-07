// Package platform holds everything that talks to Windows: processes, the HitchRecorder live state,
// QPC, the graphics drivers (NVIDIA NVML, AMD ADLX), Windows performance counters and PresentMon.
package platform

import (
	"encoding/binary"
	"math"
	"regexp"
	"strings"
	"unicode/utf16"
)

// HitchState mirrors HitchRecorder's Local\HitchRecorder.State block (640 bytes, layout asserted in the plugin).
type HitchState struct {
	Valid           bool    `json:"valid"`
	Version         uint32  `json:"version"`
	Pid             uint32  `json:"pid"`
	PluginVersion   uint32  `json:"plugin_version"`
	Capturing       bool    `json:"capturing"`
	Frame           int32   `json:"frame"`
	Spikes          int32   `json:"spikes"`
	Markers         int32   `json:"markers"`
	QpcFreq         uint64  `json:"-"`
	Qpc0            uint64  `json:"-"`
	FileTime0       uint64  `json:"-"`
	LastFrameQpc    uint64  `json:"-"`
	LastFrameMs     float32 `json:"last_frame_ms"`
	AvgFrameMs      float32 `json:"avg_frame_ms"`
	VramUsage       uint64  `json:"vram_usage"`
	VramBudget      uint64  `json:"vram_budget"`
	SharedUsage     uint64  `json:"shared_usage"`
	Hooks           uint32  `json:"hooks"`
	CaptureMode     uint32  `json:"capture_mode"`
	CaptureStartQpc uint64  `json:"-"`
	CaptureStopQpc  uint64  `json:"-"`
	BinPath         string  `json:"bin_path"`
}

func ParseHitchState(b []byte) *HitchState {
	if len(b) < 640 {
		return &HitchState{}
	}
	le := binary.LittleEndian
	s := &HitchState{}
	if le.Uint32(b[0:]) != 0x54535248 {
		return s
	}
	s.Valid = true
	s.Version = le.Uint32(b[4:])
	s.Pid = le.Uint32(b[8:])
	s.PluginVersion = le.Uint32(b[12:])
	s.Capturing = le.Uint32(b[16:]) != 0
	s.Frame = int32(le.Uint32(b[20:]))
	s.Spikes = int32(le.Uint32(b[24:]))
	s.Markers = int32(le.Uint32(b[28:]))
	s.QpcFreq = le.Uint64(b[32:])
	s.Qpc0 = le.Uint64(b[40:])
	s.FileTime0 = le.Uint64(b[48:])
	s.LastFrameQpc = le.Uint64(b[56:])
	s.LastFrameMs = math.Float32frombits(le.Uint32(b[64:]))
	s.AvgFrameMs = math.Float32frombits(le.Uint32(b[68:]))
	s.VramUsage = le.Uint64(b[72:])
	s.VramBudget = le.Uint64(b[80:])
	s.SharedUsage = le.Uint64(b[88:])
	s.Hooks = le.Uint32(b[96:])
	s.CaptureMode = le.Uint32(b[100:])
	s.CaptureStartQpc = le.Uint64(b[104:])
	s.CaptureStopQpc = le.Uint64(b[112:])
	u := make([]uint16, 0, 260)
	for i := 0; i < 260; i++ {
		c := le.Uint16(b[120+i*2:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	s.BinPath = string(utf16.Decode(u))
	return s
}

// dataDir: Frame Lab's data folder (crash guard files), set at start.
var dataDir string

func SetDataDir(d string) { dataDir = d }

// CPUInfo: the processor and its kinds of cores (two kinds on Intel 12th to 14th gen and Core Ultra).
type CPUInfo struct {
	Name   string `json:"name"`
	Cores  int    `json:"cores"`
	PCores int    `json:"p_cores"`
	ECores int    `json:"e_cores"`
	Hybrid bool   `json:"hybrid"`
}

// cpuName: the registry's processor name without the trademark signs and the "8-Core Processor" tail.
func cpuName(n string) string {
	for _, x := range []string{"(R)", "(r)", "(TM)", "(tm)"} {
		n = strings.ReplaceAll(n, x, "")
	}
	n = reCoreTail.ReplaceAllString(n, "")
	return strings.Join(strings.Fields(n), " ")
}

var reCoreTail = regexp.MustCompile(`(?i)\s+(\d+|dual|quad|six|eight|twelve|sixteen)-core processor\s*$|\s+processor\s*$|\s+cpu\s*@.*$`)

// NvSetting: one setting of the NVIDIA driver profile for the game, read only.
type NvSetting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	From  string `json:"from"` // "game profile", "global" or "driver default"
	Note  string `json:"note,omitempty"`
}

// GameProc is a running Cyberpunk2077.exe.
type GameProc struct {
	Pid  uint32 `json:"pid"`
	Path string `json:"path"` // full exe path ("" if not readable)
}

// Sampler takes the outside-the-game readings every 100 ms.
type Sampler interface {
	Stop()
	Last() map[string]float64
}

// GpuAdapter is one graphics adapter Windows has (from DXGI) and its memory in use now.
type GpuAdapter struct {
	Luid         string  `json:"luid"` // as in Windows' GPU counters: "0x00000000_0x0000d1a6"
	Name         string  `json:"name"`
	Vendor       uint32  `json:"vendor"`
	MemMB        float64 `json:"mem_mb"` // its own memory (integrated graphics: the part of RAM set aside for it)
	Integrated   bool    `json:"integrated"`
	UsedMB       float64 `json:"used_mb"`        // its own memory in use now
	SharedUsedMB float64 `json:"shared_used_mb"` // system RAM it uses now
}

// GpuProc: what one process holds on one graphics adapter.
type GpuProc struct {
	Pid         uint32  `json:"pid"`
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	Luid        string  `json:"luid"`
	DedicatedMB float64 `json:"dedicated_mb"`
	SharedMB    float64 `json:"shared_mb"`
}

// integrated graphics: Intel or AMD with a small carve-out of RAM, or named like one
func looksIntegrated(name string, vendor uint32, memMB float64) bool {
	if vendor != 0x1002 && vendor != 0x8086 {
		return false
	}
	if memMB > 0 && memMB <= 1024 {
		return true
	}
	n := strings.ToLower(name)
	if strings.Contains(n, " rx ") || strings.Contains(n, "radeon pro") || strings.Contains(n, "arc(tm) a") || strings.Contains(n, "arc(tm) b") {
		return false
	}
	return strings.Contains(n, "graphics") || strings.Contains(n, "vega")
}

// GpuPrefData returns the value data with GpuPreference set to pref ("" removes it), keeping
// whatever else Windows keeps in the same value (e.g. "SwapEffectUpgradeEnable=1;").
func GpuPrefData(old, pref string) string {
	var parts []string
	for _, kv := range strings.Split(old, ";") {
		kv = strings.TrimSpace(kv)
		l := strings.ToLower(kv)
		// SpecificAdapter only goes with Windows 11's "specific GPU" choice, so it goes with it
		if kv == "" || strings.HasPrefix(l, "gpupreference=") || strings.HasPrefix(l, "specificadapter=") {
			continue
		}
		parts = append(parts, kv)
	}
	if pref != "" {
		parts = append([]string{"GpuPreference=" + pref}, parts...)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ";") + ";"
}

// GpuPrefOf reads GpuPreference out of a value's data: "1" power saving, "2" high performance, "0" Windows decides, "" not set.
func GpuPrefOf(data string) string {
	for _, kv := range strings.Split(data, ";") {
		kv = strings.TrimSpace(kv)
		if strings.HasPrefix(strings.ToLower(kv), "gpupreference=") {
			return strings.TrimSpace(kv[len("gpupreference="):])
		}
	}
	return ""
}

// luidOf: "pid_1234_luid_0x00000000_0x0000D1A6_phys_0" -> "0x00000000_0x0000d1a6"
func luidOf(instance string) string {
	i := strings.Index(instance, "luid_")
	if i < 0 || len(instance) < i+5+21 {
		return ""
	}
	return strings.ToLower(instance[i+5 : i+5+21])
}

// GpuPrefSpecific: Windows 11 24H2's "specific GPU" choice (GpuPreference=1073741824 with
// SpecificAdapter=VEN&DEV&SUBSYS, e.g. "1002&164E&164E1002").
const GpuPrefSpecific = "1073741824"

// GpuPrefVendor: the vendor id of a "specific GPU" choice (0 if none).
func GpuPrefVendor(data string) uint32 {
	for _, kv := range strings.Split(data, ";") {
		kv = strings.TrimSpace(kv)
		if strings.HasPrefix(strings.ToLower(kv), "specificadapter=") {
			v := kv[len("specificadapter="):]
			if i := strings.IndexByte(v, '&'); i > 0 {
				v = v[:i]
			}
			var n uint32
			for _, c := range strings.ToLower(v) {
				switch {
				case c >= '0' && c <= '9':
					n = n*16 + uint32(c-'0')
				case c >= 'a' && c <= 'f':
					n = n*16 + uint32(c-'a'+10)
				default:
					return 0
				}
			}
			return n
		}
	}
	return 0
}

// InstallHint: a place a store or the running game says Cyberpunk 2077 is installed.
type InstallHint struct {
	Store string `json:"store"` // "Steam", "GOG", "Epic" or "" (found another way)
	Path  string `json:"path"`  // the game folder (the one with bin\x64)
}

// PawnIOModules: the folder with the PawnIO modules shipped with Frame Lab (components\\PawnIO), set at start.
var PawnIOModules string
