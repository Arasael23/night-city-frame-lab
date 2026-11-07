//go:build windows

package platform

// The CPU's kinds of cores (Windows CPU sets), and keeping the game on the performance cores of
// CPUs that have two kinds (Intel 12th to 14th gen, Core Ultra).

import (
	"encoding/binary"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	procGetSystemCpuSetInformation = kernel32.NewProc("GetSystemCpuSetInformation")
	procSetProcessDefaultCpuSets   = kernel32.NewProc("SetProcessDefaultCpuSets")
)

type cpuSet struct {
	id    uint32
	core  uint16 // group << 8 | core index
	class uint8  // efficiency class: higher = faster cores
}

func cpuSets() []cpuSet {
	var need uint32
	procGetSystemCpuSetInformation.Call(0, 0, uintptr(unsafe.Pointer(&need)), 0, 0)
	if need == 0 || need > 1<<20 {
		return nil
	}
	buf := make([]byte, need)
	r, _, _ := procGetSystemCpuSetInformation.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(need), uintptr(unsafe.Pointer(&need)), 0, 0)
	if r == 0 {
		return nil
	}
	var out []cpuSet
	le := binary.LittleEndian
	// SYSTEM_CPU_SET_INFORMATION: Size u32, Type u32, then Id u32, Group u16, LogicalProcessorIndex u8,
	// CoreIndex u8, LastLevelCacheIndex u8, NumaNodeIndex u8, EfficiencyClass u8, ...
	for off := 0; off+20 <= int(need); {
		size := int(le.Uint32(buf[off:]))
		if size < 20 || off+size > int(need) {
			break
		}
		if le.Uint32(buf[off+4:]) == 0 { // CpuSetInformation
			out = append(out, cpuSet{id: le.Uint32(buf[off+8:]), core: le.Uint16(buf[off+12:])<<8 | uint16(buf[off+15]), class: buf[off+18]})
		}
		off += size
	}
	return out
}

var (
	cpuOnce sync.Once
	cpuInfo CPUInfo
)

// CPU: the processor's name and its kinds of cores (read once).
func CPU() CPUInfo {
	cpuOnce.Do(func() {
		c := CPUInfo{Name: "CPU"}
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
			if n, _, err := k.GetStringValue("ProcessorNameString"); err == nil && strings.TrimSpace(n) != "" {
				c.Name = cpuName(n)
			}
			k.Close()
		}
		sets := cpuSets()
		var top uint8
		for _, s := range sets {
			if s.class > top {
				top = s.class
			}
		}
		cores := map[uint16]uint8{}
		for _, s := range sets {
			cores[s.core] = s.class
		}
		c.Cores = len(cores)
		for _, cl := range cores {
			if cl == top {
				c.PCores++
			} else {
				c.ECores++
			}
		}
		c.Hybrid = c.PCores > 0 && c.ECores > 0
		cpuInfo = c
	})
	return cpuInfo
}

var (
	pinMu  sync.Mutex
	pinned = map[uint32]bool{}
)

// PinGame keeps every running Cyberpunk2077.exe on the performance cores (once per process).
// Returns how many processes it pinned now.
func PinGame() int {
	if !CPU().Hybrid {
		return 0
	}
	var ids []uint32
	var top uint8
	sets := cpuSets()
	for _, s := range sets {
		if s.class > top {
			top = s.class
		}
	}
	for _, s := range sets {
		if s.class == top {
			ids = append(ids, s.id)
		}
	}
	if len(ids) == 0 {
		return 0
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	n := 0
	for _, g := range FindGame() {
		if pinned[g.Pid] {
			continue
		}
		if setCpuSets(g.Pid, ids) {
			pinned[g.Pid] = true
			n++
		}
	}
	return n
}

// UnpinGame gives running game processes all cores again.
func UnpinGame() {
	pinMu.Lock()
	defer pinMu.Unlock()
	for _, g := range FindGame() {
		setCpuSets(g.Pid, nil)
	}
	pinned = map[uint32]bool{}
}

func setCpuSets(pid uint32, ids []uint32) bool {
	const processSetLimitedInformation = 0x2000
	h, err := windows.OpenProcess(processSetLimitedInformation, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var p uintptr
	if len(ids) > 0 {
		p = uintptr(unsafe.Pointer(&ids[0]))
	}
	r, _, _ := procSetProcessDefaultCpuSets.Call(uintptr(h), p, uintptr(len(ids)))
	return r != 0
}
