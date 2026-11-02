//go:build windows

package platform

// The game's graphics card and where its readings come from:
//   NVIDIA  the NVIDIA driver (NVML), as in 1.0
//   AMD     AMD's ADLX (comes with the Adrenalin driver), gaps filled from Windows
//   other   Windows only
// Windows' own readings work for any card: GPU usage and VRAM from the GPU performance counters
// (the numbers Task Manager shows), temperature and clocks from the driver's WDDM performance data
// (D3DKMTQueryAdapterInfo), when the driver fills them in.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"framelab/internal/samples"
)

var (
	gdi32               = windows.NewLazySystemDLL("gdi32.dll")
	procKmtOpenFromLuid = gdi32.NewProc("D3DKMTOpenAdapterFromLuid")
	procKmtQueryInfo    = gdi32.NewProc("D3DKMTQueryAdapterInfo")
)

// KMTQUERYADAPTERINFOTYPE values and DXGK_ENGINE_TYPE_3D (d3dkmthk.h, WDDM 2.4+)
const (
	kmtNodeMetadata = 25
	kmtNodePerf     = 61
	kmtAdapterPerf  = 62
	dxgkEngine3D    = 1
)

// D3DKMT_QUERYADAPTERINFO
type kmtQuery struct {
	hAdapter uint32
	typ      int32
	data     unsafe.Pointer
	size     uint32
}

// D3DKMT_ADAPTER_PERFDATA (64 bytes)
type kmtAdapterPerfData struct {
	PhysIdx        uint32
	_              uint32
	MemFreq        uint64 // Hz
	MaxMemFreq     uint64
	MaxMemFreqOC   uint64
	MemBandwidth   uint64
	PCIeBandwidth  uint64
	FanRPM         uint32
	Power          uint32 // tenths of a percent
	Temperature    uint32 // tenths of a degree C
	PowerStateOver uint8
	_              [3]byte
}

// D3DKMT_NODE_PERFDATA (56 bytes)
type kmtNodePerfData struct {
	Node         uint32
	PhysIdx      uint32
	Freq         uint64 // Hz
	MaxFreq      uint64
	MaxFreqOC    uint64
	Voltage      uint32
	VoltageMax   uint32
	VoltageMaxOC uint32
	_            uint32
	MaxLatency   uint64
}

// D3DKMT_NODEMETADATA (80 bytes)
type kmtNodeMeta struct {
	NodeAndAdapter uint32 // adapter index << 16 | node ordinal
	EngineType     int32
	FriendlyName   [32]uint16
	Flags          uint32
	GpuMmu, IoMmu  uint8
	_              [2]byte
}

// sizes as in d3dkmthk.h (64-bit); a mismatch stops the build
var (
	_ [unsafe.Sizeof(kmtQuery{}) - 24]byte
	_ [24 - unsafe.Sizeof(kmtQuery{})]byte
	_ [unsafe.Sizeof(kmtAdapterPerfData{}) - 64]byte
	_ [64 - unsafe.Sizeof(kmtAdapterPerfData{})]byte
	_ [unsafe.Sizeof(kmtNodePerfData{}) - 56]byte
	_ [56 - unsafe.Sizeof(kmtNodePerfData{})]byte
	_ [unsafe.Sizeof(kmtNodeMeta{}) - 80]byte
	_ [80 - unsafe.Sizeof(kmtNodeMeta{})]byte
)

type kmtAdapter struct {
	h          uint32
	node3D     atomic.Int32 // the 3D engine's node, -1 until known
	metaStatus uint32       // NTSTATUS of the node list query (0 = fine)
}

func openKmt(luid string) *kmtAdapter {
	high, low, ok := parseLuid(luid)
	if !ok || procKmtOpenFromLuid.Find() != nil || procKmtQueryInfo.Find() != nil {
		return nil
	}
	var o struct {
		low  uint32
		high int32
		h    uint32
	}
	o.low, o.high = low, int32(high)
	if r, _, _ := procKmtOpenFromLuid.Call(uintptr(unsafe.Pointer(&o))); r != 0 || o.h == 0 {
		return nil
	}
	k := &kmtAdapter{h: o.h}
	k.node3D.Store(-1)
	for n := uint32(0); n < 64; n++ {
		m := kmtNodeMeta{NodeAndAdapter: n}
		if st := k.status(kmtNodeMetadata, unsafe.Pointer(&m), unsafe.Sizeof(m)); st != 0 {
			if n == 0 {
				k.metaStatus = st
			}
			break
		}
		if m.EngineType == dxgkEngine3D {
			k.node3D.Store(int32(n))
			break
		}
	}
	return k
}

func (k *kmtAdapter) status(typ int32, p unsafe.Pointer, size uintptr) uint32 {
	q := kmtQuery{hAdapter: k.h, typ: typ, data: p, size: uint32(size)}
	r, _, _ := procKmtQueryInfo.Call(uintptr(unsafe.Pointer(&q)))
	return uint32(r)
}

func (k *kmtAdapter) query(typ int32, p unsafe.Pointer, size uintptr) bool {
	return k.status(typ, p, size) == 0
}

// nodeFreq: the clock of one engine node (MHz), with the query's NTSTATUS.
func (k *kmtAdapter) nodeFreq(node uint32) (float64, uint32) {
	n := kmtNodePerfData{Node: node}
	st := k.status(kmtNodePerf, unsafe.Pointer(&n), unsafe.Sizeof(n))
	return float64(n.Freq) / 1e6, st
}

type kmtReading struct {
	TempC, ClockMHz, MemClockMHz, FanRPM, PowerPct float64
	AdapterOK, NodeOK                              bool
}

func (k *kmtAdapter) read() (r kmtReading) {
	if k == nil {
		return
	}
	var a kmtAdapterPerfData
	if k.query(kmtAdapterPerf, unsafe.Pointer(&a), unsafe.Sizeof(a)) {
		r.AdapterOK = true
		if t := float64(a.Temperature) / 10; t > 0 && t < 150 {
			r.TempC = t
		}
		if f := float64(a.MemFreq) / 1e6; f > 0 && f < 50000 {
			r.MemClockMHz = f
		}
		if a.FanRPM > 0 && a.FanRPM < 20000 {
			r.FanRPM = float64(a.FanRPM)
		}
		if p := float64(a.Power) / 10; p > 0 && p <= 200 {
			r.PowerPct = p
		}
	}
	if node := k.node3D.Load(); node >= 0 {
		n := kmtNodePerfData{Node: uint32(node)}
		if k.query(kmtNodePerf, unsafe.Pointer(&n), unsafe.Sizeof(n)) {
			r.NodeOK = true
			if f := float64(n.Freq) / 1e6; f > 0 && f < 10000 {
				r.ClockMHz = f
			}
		}
	}
	return
}

// ---------------------------------------------------------------- the game's card

type gpuSource struct {
	info GpuInfo
	main GpuAdapter
	nv   *nvml
	ad   *adlxGpu
	kmt  *kmtAdapter
}

var (
	srcOnce sync.Once
	src     *gpuSource
)

func mainSource() *gpuSource {
	srcOnce.Do(func() { src = buildSource() })
	return src
}

func buildSource() *gpuSource {
	ads, _ := cachedAdapters()
	main, haveMain := pickMain(ads)
	return sourceFor(main, haveMain, true)
}

// sourceFor sets up the readings for one card (guarded: ADLX behind the crash guard).
func sourceFor(main GpuAdapter, haveMain, guarded bool) *gpuSource {
	g := &gpuSource{}
	n := loadNVML()
	vendor := vendorOf(main.Vendor)
	if n.ok && (vendor == "nvidia" || !haveMain) { // exactly as 1.0
		g.nv = n
		g.main = main
		g.info = GpuInfo{Name: n.name, Vendor: "nvidia", Source: "NVIDIA driver (NVML)",
			Reads: []string{ReadClock, ReadTemp, ReadPower, ReadUsage, ReadVram, ReadSlowdown}}
		return g
	}
	if !haveMain {
		g.info.Note = "No graphics card found (Windows didn't list one)."
		return g
	}
	g.main = main
	g.info = GpuInfo{Name: main.Name, Vendor: vendor, Source: "Windows"}
	if vendor == "nvidia" {
		g.info.Note = "The NVIDIA driver's NVML didn't start, so only Windows' readings are used."
	}
	if vendor == "amd" {
		ad, note := openADLX(main, guarded)
		if ad != nil {
			g.ad = ad
			g.info.Source = "AMD driver (ADLX)"
			if ad.name != "" {
				g.info.Name = ad.name
			}
		} else {
			g.info.Note = note
		}
	}
	g.kmt = openKmt(main.Luid)
	if g.ad != nil && g.kmt != nil {
		g.info.Source += " + Windows"
	}
	// which readings this card gives: one reading now, with Windows' GPU counters (usage, VRAM,
	// and the 3D engine's number for the clock)
	var eng, admem map[string]float64
	hasEng, hasMem := false, false
	if q := newPdh(); q != nil {
		hasEng = q.add("e", `\GPU Engine(*)\Utilization Percentage`)
		hasMem = q.add("m", `\GPU Adapter Memory(*)\Dedicated Usage`)
		q.collect()
		time.Sleep(150 * time.Millisecond)
		q.collect()
		eng, admem = q.array("e"), q.array("m")
		q.close()
	}
	var s samples.Sample
	g.read(&s, eng, admem)
	if g.ad != nil {
		g.ad.started() // ADLX made it through a full reading: no crash guard next time
	}
	if s.GpuClockMHz > 0 {
		g.info.Reads = append(g.info.Reads, ReadClock)
	}
	if s.GpuTempC > 0 {
		g.info.Reads = append(g.info.Reads, ReadTemp)
	}
	if s.GpuPowerW > 0 {
		g.info.Reads = append(g.info.Reads, ReadPower)
	}
	if hasEng || g.ad != nil {
		g.info.Reads = append(g.info.Reads, ReadUsage)
	}
	if hasMem || g.ad != nil {
		g.info.Reads = append(g.info.Reads, ReadVram)
	}
	return g
}

// read fills what the card gives; eng / admem are this tick's GPU Engine and GPU Adapter Memory
// counters (nil: not read).
func (g *gpuSource) read(s *samples.Sample, eng, admem map[string]float64) {
	if g.nv != nil {
		g.nv.read(s)
		return
	}
	if g.ad != nil {
		g.ad.read(s)
	}
	if g.kmt != nil && g.kmt.node3D.Load() < 0 && eng != nil { // the driver doesn't list its engines: take the 3D engine from the counters
		if n := node3DFromEngines(eng, g.main.Luid); n >= 0 {
			g.kmt.node3D.Store(n)
		}
	}
	if g.kmt != nil && (s.GpuTempC == 0 || s.GpuClockMHz == 0 || s.MemClockMHz == 0) {
		r := g.kmt.read()
		if s.GpuTempC == 0 {
			s.GpuTempC = r.TempC
		}
		if s.GpuClockMHz == 0 {
			s.GpuClockMHz = r.ClockMHz
		}
		if s.MemClockMHz == 0 {
			s.MemClockMHz = r.MemClockMHz
		}
	}
	if s.GpuUtilPct == 0 && eng != nil {
		if u, ok := gpuUsage(eng, g.main.Luid); ok {
			s.GpuUtilPct = u
		}
	}
	if s.VramUsedMB == 0 && admem != nil {
		if v, ok := adapterMemMB(admem, g.main.Luid); ok {
			s.VramUsedMB = v
		}
	}
	if s.VramTotalMB == 0 {
		s.VramTotalMB = g.main.MemMB
	}
}

// needsCounters: the sampler adds the GPU Adapter Memory counter only when Windows' readings are used.
func (g *gpuSource) needsCounters() bool { return g.nv == nil && g.main.Luid != "" }

// GPU: the game's graphics card and what Frame Lab can read from it.
func GPU() GpuInfo { return mainSource().info }

// GPUName returns the game's graphics card name, or "".
func GPUName() string { return mainSource().info.Name }

var (
	liveMu sync.Mutex
	liveQ  *pdhQuery
)

// LiveGPU reads the game's card once (for the status page while not capturing).
func LiveGPU() map[string]float64 {
	g := mainSource()
	if g.info.Name == "" {
		return nil
	}
	var s samples.Sample
	var eng, ad map[string]float64
	if g.needsCounters() {
		liveMu.Lock()
		if liveQ == nil {
			if liveQ = newPdh(); liveQ != nil {
				liveQ.add("eng", `\GPU Engine(*)\Utilization Percentage`)
				liveQ.add("ad", `\GPU Adapter Memory(*)\Dedicated Usage`)
			}
		}
		if liveQ != nil {
			liveQ.collect()
			eng, ad = liveQ.array("eng"), liveQ.array("ad")
		}
		liveMu.Unlock()
	}
	g.read(&s, eng, ad)
	out := map[string]float64{"gpu_util": s.GpuUtilPct, "vram_used": s.VramUsedMB, "vram_total": s.VramTotalMB}
	if s.GpuClockMHz > 0 {
		out["gpu_clock"] = s.GpuClockMHz
	}
	if s.GpuTempC > 0 {
		out["gpu_temp"] = s.GpuTempC
	}
	if s.GpuPowerW > 0 {
		out["gpu_power"] = s.GpuPowerW
	}
	return out
}

// kmtText: one line of what the WDDM performance data gives for an adapter (GPU check).
func kmtText(luid string) string {
	k := openKmt(luid)
	if k == nil {
		return "WDDM performance data: adapter could not be opened"
	}
	r := k.read()
	node := fmt.Sprintf("node list query status 0x%08x", k.metaStatus)
	if n := k.node3D.Load(); n >= 0 {
		node = fmt.Sprintf("3D engine = node %d", n)
	}
	return fmt.Sprintf("WDDM performance data: adapter query %v, node query %v (%s); temp %.1f °C, GPU clock %.0f MHz, memory clock %.0f MHz, fan %.0f rpm, power %.1f%%",
		r.AdapterOK, r.NodeOK, node, r.TempC, r.ClockMHz, r.MemClockMHz, r.FanRPM, r.PowerPct)
}
