package platform

// CPU temperature (2.0): read from HWiNFO's shared memory when HWiNFO runs with "Shared Memory
// Support" on, otherwise through PawnIO (the signed driver LibreHardwareMonitor and Fan Control
// use) with its AMD Family 17h+ module, when both are present. Nothing is installed from here.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32ct          = windows.NewLazySystemDLL("kernel32.dll")
	procMapViewOfFileCT = kernel32ct.NewProc("MapViewOfFile")
	procUnmapViewCT     = kernel32ct.NewProc("UnmapViewOfFile")
)

type CPUTemp struct {
	mu     sync.Mutex
	Source string // "HWiNFO", "PawnIO" or ""
	hwMap  windows.Handle
	hwView uintptr
	hwSize uintptr // bytes mapped: every read stays inside
	pio    *pawnio
}

// OpenCPUTemp: modDir holds PawnIO modules shipped with Frame Lab (components\PawnIO).
func OpenCPUTemp(modDir string) *CPUTemp {
	c := &CPUTemp{}
	if c.openHWiNFO() {
		c.Source = "HWiNFO"
		return c
	}
	if p := openPawnIO(modDir); p != nil {
		c.pio, c.Source = p, "PawnIO"
	}
	return c
}

// Read: degrees C, or NaN when there is no source / no reading.
func (c *CPUTemp) Read() float64 {
	if c == nil {
		return math.NaN()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.hwView != 0:
		return hwinfoCPUTemp(c.hwView, c.hwSize)
	case c.pio != nil:
		return c.pio.amdTctl()
	}
	return math.NaN()
}

func (c *CPUTemp) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hwView != 0 {
		procUnmapViewCT.Call(c.hwView)
		c.hwView = 0
	}
	if c.hwMap != 0 {
		windows.CloseHandle(c.hwMap)
		c.hwMap = 0
	}
	if c.pio != nil {
		c.pio.close()
		c.pio = nil
	}
}

// ---------------------------------------------------------------- HWiNFO shared memory (SM2 layout)

func (c *CPUTemp) openHWiNFO() bool {
	n, _ := windows.UTF16PtrFromString(`Global\HWiNFO_SENS_SM2`)
	h, _, _ := procOpenFileMappingW.Call(uintptr(windows.FILE_MAP_READ), 0, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return false
	}
	v, _, _ := procMapViewOfFileCT.Call(h, uintptr(windows.FILE_MAP_READ), 0, 0, 0)
	if v == 0 {
		windows.CloseHandle(windows.Handle(h))
		return false
	}
	var mbi windows.MemoryBasicInformation
	if windows.VirtualQuery(v, &mbi, unsafe.Sizeof(mbi)) != nil || mbi.RegionSize < 64 {
		procUnmapViewCT.Call(v)
		windows.CloseHandle(windows.Handle(h))
		return false
	}
	c.hwMap, c.hwView, c.hwSize = windows.Handle(h), v, mbi.RegionSize
	if math.IsNaN(hwinfoCPUTemp(v, c.hwSize)) {
		c.Close()
		return false
	}
	return true
}

// header (packed): sig u32, ver u32, rev u32, poll i64, offSensors u32, sizeSensor u32, nSensors u32,
// offReadings u32 (at 32), sizeReading u32 (36), nReadings u32 (40).
// reading: type u32, sensorIdx u32, id u32, labelOrig [128], labelUser [128], unit [16], value f64 (at 284).
func hwinfoCPUTemp(base, mapped uintptr) float64 {
	rd := func(off uintptr) uint32 { return *(*uint32)(unsafe.Pointer(base + off)) }
	if mapped < 44 || rd(0) != 0x53695748 { // "HWiS"
		return math.NaN()
	}
	off, size, n := uintptr(rd(32)), uintptr(rd(36)), int(rd(40))
	if size < 292 || n <= 0 || n > 20000 || off+uintptr(n)*size > mapped {
		return math.NaN()
	}
	best, rank := math.NaN(), 99
	for i := 0; i < n; i++ {
		r := base + off + uintptr(i)*size
		if *(*uint32)(unsafe.Pointer(r)) != 1 { // temperature
			continue
		}
		lb := (*[128]byte)(unsafe.Pointer(r + 12))
		label := string(lb[:clen(lb[:])])
		v := *(*float64)(unsafe.Pointer(r + 284))
		k := cpuLabelRank(label)
		if k < rank && v > 0 && v < 130 {
			best, rank = v, k
		}
	}
	return best
}

func clen(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}

// which HWiNFO reading is "the CPU temperature" (lower = better match)
func cpuLabelRank(l string) int {
	l = strings.ToLower(l)
	switch {
	case strings.HasPrefix(l, "cpu (tctl/tdie)"):
		return 0
	case strings.HasPrefix(l, "cpu package"):
		return 1
	case strings.HasPrefix(l, "cpu (tdie)"):
		return 2
	case strings.HasPrefix(l, "core max"):
		return 3
	case strings.HasPrefix(l, "cpu die (average)"):
		return 4
	}
	return 99
}

// ---------------------------------------------------------------- PawnIO (AMD Zen: SMN 0x59800)

type pawnio struct {
	dll     *windows.LazyDLL
	h       uintptr
	execute *windows.LazyProc
	closeP  *windows.LazyProc
}

func openPawnIO(modDir string) *pawnio {
	if PawnIOVersion() == "" {
		return nil
	}
	lib := filepath.Join(pawnIODir(), "PawnIOLib.dll")
	mod := filepath.Join(modDir, "AMDFamily17.bin")
	blob, err := os.ReadFile(mod)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(lib); err != nil {
		return nil
	}
	d := windows.NewLazyDLL(lib)
	open, load := d.NewProc("pawnio_open"), d.NewProc("pawnio_load")
	p := &pawnio{dll: d, execute: d.NewProc("pawnio_execute"), closeP: d.NewProc("pawnio_close")}
	if open.Find() != nil || load.Find() != nil || p.execute.Find() != nil {
		return nil
	}
	if r, _, _ := open.Call(uintptr(unsafe.Pointer(&p.h))); r != 0 || p.h == 0 {
		return nil
	}
	if r, _, _ := load.Call(p.h, uintptr(unsafe.Pointer(&blob[0])), uintptr(len(blob))); r != 0 {
		p.close()
		return nil
	}
	if math.IsNaN(p.amdTctl()) {
		p.close()
		return nil
	}
	return p
}

func (p *pawnio) amdTctl() float64 {
	name, _ := syscall.BytePtrFromString("ioctl_read_smn")
	in := [1]uint64{0x00059800}
	var out [1]uint64
	var got uintptr
	r, _, _ := p.execute.Call(p.h, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&in[0])), 1, uintptr(unsafe.Pointer(&out[0])), 1, uintptr(unsafe.Pointer(&got)))
	if r != 0 || got < 1 {
		return math.NaN()
	}
	v := uint32(out[0])
	t := float64((v>>21)&0x7FF) / 8
	if v&0x80000 != 0 {
		t -= 49
	}
	if t <= 0 || t > 130 {
		return math.NaN()
	}
	return t
}

func (p *pawnio) close() {
	if p.h != 0 && p.closeP.Find() == nil {
		p.closeP.Call(p.h)
	}
	p.h = 0
}
