//go:build windows

package platform

// AMD Radeon readings through ADLX (AMD Device Library eXtra, amdadlx64.dll, installed with the
// AMD Software: Adrenalin Edition driver). ADLX is a C interface of objects with function tables;
// the table slots used here follow the ADLX SDK headers (ISystem.h, IPerformanceMonitoring.h,
// ICollections.h; AMD, MIT licence). Nothing of the SDK is included.
//
// A wrong call into a driver library can't be caught in Go and closes Frame Lab, so ADLX is
// started behind a guard file in data\: if Frame Lab closes before ADLX gave a full reading, the
// next start skips ADLX and uses Windows' readings instead.

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"framelab/internal/samples"
)

// ADLX 1.2.0.72, the oldest interface version whose slots are used here (newer drivers keep them)
const adlxHeaderVersion = uint64(1)<<48 | uint64(2)<<32 | 72

// function table slots
const (
	// IADLXInterface (every object below except IADLXSystem starts with these)
	adlxRelease = 1
	// IADLXSystem
	adlxSysGetGPUs = 1
	adlxSysGetPerf = 9
	// IADLXList / IADLXGPUList
	adlxListSize  = 3
	adlxListBegin = 5
	adlxListAtGPU = 11
	// IADLXGPU
	adlxGpuVendorID  = 3
	adlxGpuType      = 5
	adlxGpuName      = 7
	adlxGpuTotalVRAM = 11
	// IADLXPerformanceMonitoringServices
	adlxPerfCurrentGPU   = 18
	adlxPerfSupportedGPU = 21
	// IADLXGPUMetricsSupport: IsSupportedGPU...
	adlxSupUsage  = 3
	adlxSupClock  = 4
	adlxSupVClock = 5
	adlxSupTemp   = 6
	adlxSupHot    = 7
	adlxSupPower  = 8
	adlxSupTBP    = 9
	adlxSupFan    = 10
	adlxSupVram   = 11
	// IADLXGPUMetrics
	adlxMUsage  = 4
	adlxMClock  = 5
	adlxMVClock = 6
	adlxMTemp   = 7
	adlxMHot    = 8
	adlxMPower  = 9
	adlxMTBP    = 10
	adlxMFan    = 11
	adlxMVram   = 12
	// ADLX_GPU_TYPE
	adlxGpuIntegrated = 1
)

var (
	adlxDll  = windows.NewLazySystemDLL("amdadlx64.dll")
	adlxMu   sync.Mutex // one ADLX start per process, calls one at a time
	adlxSys  uintptr
	adlxPerf uintptr
	adlxErr  string
)

// vcall calls slot idx of an ADLX object's function table; ^0 if the object or slot is missing.
func vcall(obj uintptr, idx int, args ...uintptr) uintptr {
	if obj == 0 {
		return ^uintptr(0)
	}
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	if vtbl == 0 {
		return ^uintptr(0)
	}
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	if fn == 0 {
		return ^uintptr(0)
	}
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func adlxOK(r uintptr) bool { return int32(r) == 0 }

// cstr reads a C string ADLX owns.
func cstr(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); i < 256; i++ {
		c := *(*byte)(unsafe.Pointer(p + i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func adlxGuard() string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "adlx_starting")
}

// adlxStart starts ADLX once (caller holds adlxMu).
func adlxStart(guarded bool) bool {
	if adlxSys != 0 {
		return true
	}
	if adlxErr != "" {
		return false
	}
	if g := adlxGuard(); guarded && g != "" {
		if _, err := os.Stat(g); err == nil {
			adlxErr = "AMD's ADLX was skipped: Frame Lab closed while starting it last time (delete data\\adlx_starting to try again)."
			return false
		}
	}
	if adlxDll.Load() != nil {
		adlxErr = "AMD's ADLX isn't installed (it comes with the AMD Software: Adrenalin Edition driver)."
		return false
	}
	init := adlxDll.NewProc("ADLXInitialize")
	if init.Find() != nil {
		adlxErr = "amdadlx64.dll has no ADLXInitialize."
		return false
	}
	if g := adlxGuard(); guarded && g != "" {
		os.WriteFile(g, []byte("Frame Lab is starting AMD's ADLX. If this file stays, ADLX closed Frame Lab and is skipped next time.\r\n"), 0o644)
	}
	var sys uintptr
	r, _, _ := init.Call(uintptr(adlxHeaderVersion), uintptr(unsafe.Pointer(&sys)))
	if !adlxOK(r) || sys == 0 {
		var full uint64
		if q := adlxDll.NewProc("ADLXQueryFullVersion"); q.Find() == nil {
			q.Call(uintptr(unsafe.Pointer(&full)))
		}
		if full != 0 {
			sys = 0
			r, _, _ = init.Call(uintptr(full), uintptr(unsafe.Pointer(&sys)))
		}
	}
	if !adlxOK(r) || sys == 0 {
		adlxErr = fmt.Sprintf("AMD's ADLX didn't start (code %d).", int32(r))
		return false
	}
	var perf uintptr
	if !adlxOK(vcall(sys, adlxSysGetPerf, uintptr(unsafe.Pointer(&perf)))) || perf == 0 {
		adlxErr = "AMD's ADLX has no performance monitoring on this driver."
		return false
	}
	adlxSys, adlxPerf = sys, perf
	return true
}

type adlxGpu struct {
	gpu        uintptr
	name       string
	vendorID   string
	integrated bool
	vramMB     float64
	sup        map[int]bool // supported metric slots (adlxSup...)
	guarded    bool
}

type adlxCard struct {
	gpu        uintptr
	name       string
	vendorID   string
	integrated bool
	vramMB     float64
}

// adlxCards lists the cards ADLX sees (caller holds adlxMu; the caller releases the gpu objects).
func adlxCards() []adlxCard {
	var list uintptr
	if !adlxOK(vcall(adlxSys, adlxSysGetGPUs, uintptr(unsafe.Pointer(&list)))) || list == 0 {
		return nil
	}
	defer vcall(list, adlxRelease)
	n := uint32(vcall(list, adlxListSize))
	begin := uint32(vcall(list, adlxListBegin))
	var out []adlxCard
	for i := uint32(0); i < n && i < 16; i++ {
		var g uintptr
		if !adlxOK(vcall(list, adlxListAtGPU, uintptr(begin+i), uintptr(unsafe.Pointer(&g)))) || g == 0 {
			continue
		}
		c := adlxCard{gpu: g}
		var p uintptr
		if adlxOK(vcall(g, adlxGpuName, uintptr(unsafe.Pointer(&p)))) {
			c.name = cstr(p)
		}
		p = 0
		if adlxOK(vcall(g, adlxGpuVendorID, uintptr(unsafe.Pointer(&p)))) {
			c.vendorID = cstr(p)
		}
		var typ int32
		if adlxOK(vcall(g, adlxGpuType, uintptr(unsafe.Pointer(&typ)))) {
			c.integrated = typ == adlxGpuIntegrated
		}
		var mb uint32
		if adlxOK(vcall(g, adlxGpuTotalVRAM, uintptr(unsafe.Pointer(&mb)))) {
			c.vramMB = float64(mb)
		}
		out = append(out, c)
	}
	return out
}

// openADLX finds the card in ADLX that Windows calls target (by name, else the same kind of card
// with the closest memory size).
func openADLX(target GpuAdapter, guarded bool) (*adlxGpu, string) {
	adlxMu.Lock()
	defer adlxMu.Unlock()
	if !adlxStart(guarded) {
		adlxUnguard(guarded) // failed without closing Frame Lab
		return nil, adlxErr
	}
	cards := adlxCards()
	best, bestScore := -1, -1e18
	for i, c := range cards {
		score := 0.0
		if sameCard(c.name, target.Name) {
			score += 1e6
		}
		if c.integrated == target.Integrated {
			score += 1e4
		}
		d := c.vramMB - target.MemMB
		if d < 0 {
			d = -d
		}
		score -= d
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	for i, c := range cards {
		if i != best {
			vcall(c.gpu, adlxRelease)
		}
	}
	if best < 0 {
		adlxUnguard(guarded)
		return nil, "AMD's ADLX lists no graphics card."
	}
	c := cards[best]
	a := &adlxGpu{gpu: c.gpu, name: c.name, vendorID: c.vendorID, integrated: c.integrated, vramMB: c.vramMB, sup: map[int]bool{}, guarded: guarded}
	var sup uintptr
	if adlxOK(vcall(adlxPerf, adlxPerfSupportedGPU, a.gpu, uintptr(unsafe.Pointer(&sup)))) && sup != 0 {
		for _, slot := range []int{adlxSupUsage, adlxSupClock, adlxSupVClock, adlxSupTemp, adlxSupHot, adlxSupPower, adlxSupTBP, adlxSupFan, adlxSupVram} {
			var b uint8
			if adlxOK(vcall(sup, slot, uintptr(unsafe.Pointer(&b)))) && b != 0 {
				a.sup[slot] = true
			}
		}
		vcall(sup, adlxRelease)
	}
	return a, ""
}

func adlxUnguard(guarded bool) {
	if g := adlxGuard(); guarded && g != "" {
		os.Remove(g)
	}
}

// started: ADLX gave a full reading, so the crash guard goes.
func (a *adlxGpu) started() { adlxUnguard(a.guarded) }

type adlxReading struct {
	Usage, Clock, VClock, Temp, Hot, Power, TBP, Fan, Vram float64
	OK                                                     bool
}

func (a *adlxGpu) current() (r adlxReading) {
	adlxMu.Lock()
	defer adlxMu.Unlock()
	var m uintptr
	if !adlxOK(vcall(adlxPerf, adlxPerfCurrentGPU, a.gpu, uintptr(unsafe.Pointer(&m)))) || m == 0 {
		return
	}
	defer vcall(m, adlxRelease)
	r.OK = true
	dbl := func(sup, slot int) float64 {
		var d float64
		if a.sup[sup] && adlxOK(vcall(m, slot, uintptr(unsafe.Pointer(&d)))) {
			return d
		}
		return 0
	}
	itg := func(sup, slot int) float64 {
		var i int32
		if a.sup[sup] && adlxOK(vcall(m, slot, uintptr(unsafe.Pointer(&i)))) {
			return float64(i)
		}
		return 0
	}
	r.Usage = dbl(adlxSupUsage, adlxMUsage)
	r.Clock = itg(adlxSupClock, adlxMClock)
	r.VClock = itg(adlxSupVClock, adlxMVClock)
	r.Temp = dbl(adlxSupTemp, adlxMTemp)
	r.Hot = dbl(adlxSupHot, adlxMHot)
	r.Power = dbl(adlxSupPower, adlxMPower)
	r.TBP = dbl(adlxSupTBP, adlxMTBP)
	r.Fan = itg(adlxSupFan, adlxMFan)
	r.Vram = itg(adlxSupVram, adlxMVram)
	return
}

func (a *adlxGpu) read(s *samples.Sample) {
	r := a.current()
	if !r.OK {
		return
	}
	s.GpuUtilPct = r.Usage
	s.GpuClockMHz = r.Clock
	s.MemClockMHz = r.VClock
	s.GpuTempC = r.Temp
	s.GpuPowerW = r.TBP                    // whole card, like NVIDIA's number
	if s.GpuPowerW == 0 && !a.integrated { // on integrated graphics ADLX's "GPU power" is the whole processor's (50 W idle on a 7800X3D)
		s.GpuPowerW = r.Power
	}
	s.VramUsedMB = r.Vram
	s.VramTotalMB = a.vramMB
}

// supportedText lists the metrics ADLX says this card has (GPU check).
func (a *adlxGpu) supportedText() string {
	names := []struct {
		slot int
		n    string
	}{{adlxSupUsage, "usage"}, {adlxSupClock, "clock"}, {adlxSupVClock, "memory clock"}, {adlxSupTemp, "temperature"},
		{adlxSupHot, "hotspot"}, {adlxSupPower, "GPU power"}, {adlxSupTBP, "board power"}, {adlxSupFan, "fan"}, {adlxSupVram, "VRAM"}}
	var out []string
	for _, x := range names {
		if a.sup[x.slot] {
			out = append(out, x.n)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return fmt.Sprint(out)
}
