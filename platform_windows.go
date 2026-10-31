//go:build windows

package platform

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"framelab/internal/modmap"
	"framelab/internal/samples"
)

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procQPC              = kernel32.NewProc("QueryPerformanceCounter")
	procQPF              = kernel32.NewProc("QueryPerformanceFrequency")
	procOpenFileMappingW = kernel32.NewProc("OpenFileMappingW")
	procBeep             = kernel32.NewProc("Beep")
	user32               = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW      = user32.NewProc("MessageBoxW")
)

func QPC() int64 {
	var v int64
	procQPC.Call(uintptr(unsafe.Pointer(&v)))
	return v
}

func QPCFreq() int64 {
	var v int64
	procQPF.Call(uintptr(unsafe.Pointer(&v)))
	return v
}

func IsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// SingleInstance returns false if Frame Lab is already running.
func SingleInstance() bool {
	name, _ := windows.UTF16PtrFromString("Local\\NightCityFrameLab.Instance")
	_, err := windows.CreateMutex(nil, false, name)
	return !errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

func MessageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40)
}

func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

func OpenFolder(path string) {
	cmd := exec.Command("explorer.exe", path)
	cmd.Start()
}

// processes returns pid -> exe name.
func processes() map[uint32]string {
	out := map[uint32]string{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		out[e.ProcessID] = windows.UTF16ToString(e.ExeFile[:])
	}
	return out
}

func exePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// FindGame lists running Cyberpunk2077.exe processes.
func FindGame() []GameProc {
	var out []GameProc
	for pid, name := range processes() {
		if strings.EqualFold(name, "Cyberpunk2077.exe") {
			out = append(out, GameProc{Pid: pid, Path: exePath(pid)})
		}
	}
	return out
}

// ReadHitchState reads HitchRecorder's live block, if the game is running with it.
func ReadHitchState() *HitchState {
	name, _ := windows.UTF16PtrFromString("Local\\HitchRecorder.State")
	h, _, _ := procOpenFileMappingW.Call(uintptr(windows.FILE_MAP_READ), 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return &HitchState{}
	}
	defer windows.CloseHandle(windows.Handle(h))
	addr, err := windows.MapViewOfFile(windows.Handle(h), windows.FILE_MAP_READ, 0, 0, 640)
	if err != nil || addr == 0 {
		return &HitchState{}
	}
	defer windows.UnmapViewOfFile(addr)
	b := make([]byte, 640)
	copy(b, unsafe.Slice((*byte)(unsafe.Pointer(addr)), 640))
	return ParseHitchState(b)
}

// ---------------------------------------------------------------- NVML

type nvml struct {
	ok      bool
	dev     uintptr
	name    string
	clock   *windows.LazyProc
	power   *windows.LazyProc
	temp    *windows.LazyProc
	util    *windows.LazyProc
	enc     *windows.LazyProc
	mem     *windows.LazyProc
	reasons *windows.LazyProc
}

var (
	nvOnce sync.Once
	nv     nvml
)

func loadNVML() *nvml {
	nvOnce.Do(func() {
		dll := windows.NewLazySystemDLL("nvml.dll")
		if dll.Load() != nil {
			return
		}
		if r, _, _ := dll.NewProc("nvmlInit_v2").Call(); r != 0 {
			return
		}
		var count uint32
		dll.NewProc("nvmlDeviceGetCount_v2").Call(uintptr(unsafe.Pointer(&count)))
		if count == 0 {
			return
		}
		var best uintptr
		var bestMem uint64
		for i := uint32(0); i < count; i++ {
			var d uintptr
			if r, _, _ := dll.NewProc("nvmlDeviceGetHandleByIndex_v2").Call(uintptr(i), uintptr(unsafe.Pointer(&d))); r != 0 {
				continue
			}
			var m [3]uint64
			dll.NewProc("nvmlDeviceGetMemoryInfo").Call(d, uintptr(unsafe.Pointer(&m[0])))
			if best == 0 || m[0] > bestMem {
				best, bestMem = d, m[0]
			}
		}
		if best == 0 {
			return
		}
		nv.dev = best
		buf := make([]byte, 96)
		dll.NewProc("nvmlDeviceGetName").Call(best, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		nv.name = string(bytes.TrimRight(buf, "\x00"))
		nv.clock = dll.NewProc("nvmlDeviceGetClockInfo")
		nv.power = dll.NewProc("nvmlDeviceGetPowerUsage")
		nv.temp = dll.NewProc("nvmlDeviceGetTemperature")
		nv.util = dll.NewProc("nvmlDeviceGetUtilizationRates")
		nv.enc = dll.NewProc("nvmlDeviceGetEncoderUtilization")
		nv.mem = dll.NewProc("nvmlDeviceGetMemoryInfo")
		nv.reasons = dll.NewProc("nvmlDeviceGetCurrentClocksEventReasons")
		if nv.reasons.Find() != nil {
			nv.reasons = dll.NewProc("nvmlDeviceGetCurrentClocksThrottleReasons")
		}
		nv.ok = true
	})
	return &nv
}

// GPUName returns the NVIDIA GPU name, or "".
func GPUName() string { return loadNVML().name }

func (n *nvml) read(s *samples.Sample) {
	if !n.ok {
		return
	}
	var u32 uint32
	if r, _, _ := n.clock.Call(n.dev, 0, uintptr(unsafe.Pointer(&u32))); r == 0 {
		s.GpuClockMHz = float64(u32)
	}
	if r, _, _ := n.clock.Call(n.dev, 2, uintptr(unsafe.Pointer(&u32))); r == 0 {
		s.MemClockMHz = float64(u32)
	}
	if r, _, _ := n.power.Call(n.dev, uintptr(unsafe.Pointer(&u32))); r == 0 {
		s.GpuPowerW = float64(u32) / 1000
	}
	if r, _, _ := n.temp.Call(n.dev, 0, uintptr(unsafe.Pointer(&u32))); r == 0 {
		s.GpuTempC = float64(u32)
	}
	var ut [2]uint32
	if r, _, _ := n.util.Call(n.dev, uintptr(unsafe.Pointer(&ut[0]))); r == 0 {
		s.GpuUtilPct = float64(ut[0])
	}
	var e, per uint32
	if r, _, _ := n.enc.Call(n.dev, uintptr(unsafe.Pointer(&e)), uintptr(unsafe.Pointer(&per))); r == 0 {
		s.EncUtilPct = float64(e)
	}
	var m [3]uint64
	if r, _, _ := n.mem.Call(n.dev, uintptr(unsafe.Pointer(&m[0]))); r == 0 {
		s.VramTotalMB = float64(m[0]) / 1048576
		s.VramUsedMB = float64(m[2]) / 1048576
	}
	var reasons uint64
	if r, _, _ := n.reasons.Call(n.dev, uintptr(unsafe.Pointer(&reasons))); r == 0 {
		s.Throttle = reasons
	}
}

// ---------------------------------------------------------------- PDH

var (
	pdh               = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery  = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglish = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollect    = pdh.NewProc("PdhCollectQueryData")
	procPdhGetValue   = pdh.NewProc("PdhGetFormattedCounterValue")
	procPdhGetArray   = pdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery = pdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble   = 0x00000200
	pdhFmtNoCap100 = 0x00008000
	pdhMoreData    = 0x800007D2
)

type pdhQuery struct {
	q        uintptr
	counters map[string]uintptr
}

func newPdh() *pdhQuery {
	var q uintptr
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q))); r != 0 {
		return nil
	}
	return &pdhQuery{q: q, counters: map[string]uintptr{}}
}

func (p *pdhQuery) add(key, path string) bool {
	ps, _ := windows.UTF16PtrFromString(path)
	var c uintptr
	if r, _, _ := procPdhAddEnglish.Call(p.q, uintptr(unsafe.Pointer(ps)), 0, uintptr(unsafe.Pointer(&c))); r != 0 {
		return false
	}
	p.counters[key] = c
	return true
}

func (p *pdhQuery) collect() { procPdhCollect.Call(p.q) }

func (p *pdhQuery) value(key string) (float64, bool) {
	c, ok := p.counters[key]
	if !ok {
		return 0, false
	}
	var v struct {
		Status uint32
		_      uint32
		Double float64
	}
	if r, _, _ := procPdhGetValue.Call(c, pdhFmtDouble|pdhFmtNoCap100, 0, uintptr(unsafe.Pointer(&v))); r != 0 || v.Status > 1 {
		return 0, false
	}
	return v.Double, true
}

// array returns instance name -> value for a wildcard counter.
func (p *pdhQuery) array(key string) map[string]float64 {
	out := map[string]float64{}
	c, ok := p.counters[key]
	if !ok {
		return out
	}
	var size, count uint32
	r, _, _ := procPdhGetArray.Call(c, pdhFmtDouble|pdhFmtNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if uint32(r) != pdhMoreData || size == 0 {
		return out
	}
	buf := make([]byte, size)
	r, _, _ = procPdhGetArray.Call(c, pdhFmtDouble|pdhFmtNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return out
	}
	type item struct {
		Name   *uint16
		Status uint32
		_      uint32
		Double float64
	}
	items := unsafe.Slice((*item)(unsafe.Pointer(&buf[0])), count)
	for _, it := range items {
		if it.Status > 1 {
			continue
		}
		out[windows.UTF16PtrToString(it.Name)] += it.Double
	}
	runtime.KeepAlive(buf)
	return out
}

func (p *pdhQuery) close() { procPdhCloseQuery.Call(p.q) }

// ---------------------------------------------------------------- sampler

type winSampler struct {
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	last map[string]float64
}

func (s *winSampler) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
}

func (s *winSampler) Last() map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]float64{}
	for k, v := range s.last {
		out[k] = v
	}
	return out
}

func pidOf(instance string) uint32 {
	// "pid_12345_luid_0x..._engtype_3D"
	if !strings.HasPrefix(instance, "pid_") {
		return 0
	}
	rest := instance[4:]
	if i := strings.IndexByte(rest, '_'); i > 0 {
		v, _ := strconv.ParseUint(rest[:i], 10, 32)
		return uint32(v)
	}
	return 0
}

// StartSampler writes one row every 100 ms to out until Stop.
func StartSampler(gamePid uint32, out string) (Sampler, error) {
	w, err := samples.Create(out)
	if err != nil {
		return nil, err
	}
	s := &winSampler{stop: make(chan struct{}), done: make(chan struct{}), last: map[string]float64{}}
	go func() {
		defer close(s.done)
		defer w.Close()
		runtime.LockOSThread()
		n := loadNVML()
		q := newPdh()
		ncpu := float64(runtime.NumCPU())
		gameCounters := false
		if q != nil {
			defer q.close()
			q.add("cores", `\Processor(*)\% Processor Time`)
			q.add("gpueng", `\GPU Engine(*)\Utilization Percentage`)
			q.add("gpumem", `\GPU Process Memory(*)\Dedicated Usage`)
			q.add("avail", `\Memory\Available MBytes`)
			q.add("perf", `\Processor Information(_Total)\% Processor Performance`)
			q.add("basefreq", `\Processor Information(_Total)\Processor Frequency`)
			q.add("dpc", `\Processor(_Total)\% DPC Time`)
			q.add("int", `\Processor(_Total)\% Interrupt Time`)
			q.add("pages", `\Memory\Pages Input/sec`)
			q.add("gpushared", `\GPU Process Memory(*)\Shared Usage`)
			q.add("commit", `\Memory\Committed Bytes`)
			q.add("commitlim", `\Memory\Commit Limit`)
			q.collect()
		}
		names := processes()
		lastNames := time.Now()
		apps := newGpuAppsWriter(filepath.Dir(out)) // other programs on the game's card (1.4+)
		defer apps.close()
		lastApps := time.Now()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		flushAt := time.Now()
		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
			}
			smp := samples.Sample{Qpc: QPC()}
			n.read(&smp)
			if q != nil {
				if !gameCounters {
					a := q.add("gcpu", `\Process(Cyberpunk2077)\% Processor Time`)
					b := q.add("gread", `\Process(Cyberpunk2077)\IO Read Bytes/sec`)
					c := q.add("gws", `\Process(Cyberpunk2077)\Working Set - Private`)
					gameCounters = a && b && c
				}
				q.collect()
				if v, ok := q.value("gcpu"); ok {
					smp.GameCpuPct = v / ncpu
				}
				if v, ok := q.value("gread"); ok {
					smp.GameReadMBs = v / 1048576
				}
				if v, ok := q.value("gws"); ok {
					smp.GameWorkingMB = v / 1048576
				}
				if v, ok := q.value("avail"); ok {
					smp.AvailRamMB = v
				}
				for inst, v := range q.array("cores") {
					if inst == "_Total" {
						smp.CpuTotalPct = v
						continue
					}
					if v > smp.MaxCorePct {
						smp.MaxCorePct = v
					}
					if v >= 90 {
						smp.CoresBusy++
					}
				}
				if perf, ok := q.value("perf"); ok {
					if base, ok := q.value("basefreq"); ok {
						smp.CpuClockMHz = base * perf / 100
					}
				}
				if v, ok := q.value("dpc"); ok {
					smp.DpcPct = v
				}
				if v, ok := q.value("int"); ok {
					smp.IntPct = v
				}
				if v, ok := q.value("pages"); ok {
					smp.PagesSec = v
				}
				if v, ok := q.value("commit"); ok {
					smp.CommitMB = v / 1048576
				}
				if v, ok := q.value("commitlim"); ok {
					smp.CommitLimitMB = v / 1048576
				}
				for inst, v := range q.array("gpushared") {
					if pidOf(inst) == gamePid {
						smp.GameSharedMB += v / 1048576
					}
				}
				perPid := map[uint32]float64{}
				for inst, v := range q.array("gpueng") {
					pid := pidOf(inst)
					if pid == 0 {
						continue
					}
					if pid == gamePid {
						if strings.HasSuffix(inst, "engtype_3D") {
							smp.GameGpu3DPct += v
						}
						continue
					}
					perPid[pid] += v
				}
				if time.Since(lastNames) > 2*time.Second {
					names, lastNames = processes(), time.Now()
				}
				for pid, v := range perPid {
					if v > smp.OtherGpuPct {
						smp.OtherGpuPct = v
						smp.OtherGpuName = names[pid]
						if smp.OtherGpuName == "" {
							smp.OtherGpuName = fmt.Sprintf("pid %d", pid)
						}
					}
				}
				gpumem := q.array("gpumem")
				for inst, v := range gpumem {
					if pidOf(inst) == gamePid {
						smp.GameDedicatedMB += v / 1048576
					}
				}
				if time.Since(lastApps) >= 2*time.Second {
					lastApps = time.Now()
					apps.write(smp.Qpc, gpumem, q.array("gpushared"), gamePid, names)
				}
			}
			w.Write(&smp)
			s.mu.Lock()
			s.last = map[string]float64{"gpu_clock": smp.GpuClockMHz, "gpu_temp": smp.GpuTempC, "gpu_power": smp.GpuPowerW,
				"gpu_util": smp.GpuUtilPct, "enc_util": smp.EncUtilPct, "vram_used": smp.VramUsedMB, "vram_total": smp.VramTotalMB,
				"game_cpu": smp.GameCpuPct, "disk": smp.GameReadMBs, "other_gpu": smp.OtherGpuPct, "throttle": float64(smp.Throttle & samples.ThrottleSlowdownMask)}
			s.mu.Unlock()
			if time.Since(flushAt) > time.Second {
				w.Flush()
				flushAt = time.Now()
			}
		}
	}()
	return s, nil
}

// LiveGPU reads one NVML sample (for the status page while not capturing).
func LiveGPU() map[string]float64 {
	var s samples.Sample
	n := loadNVML()
	if !n.ok {
		return nil
	}
	n.read(&s)
	return map[string]float64{"gpu_clock": s.GpuClockMHz, "gpu_temp": s.GpuTempC, "gpu_power": s.GpuPowerW, "gpu_util": s.GpuUtilPct,
		"vram_used": s.VramUsedMB, "vram_total": s.VramTotalMB}
}

// ---------------------------------------------------------------- file names from WolvenKit

// ExtractNames builds the path list from WolvenKit.Common.dll, decompressing with the game's Oodle DLL.
func ExtractNames(wolvenKitCommon, oodleDll, outGz string) error {
	dll, err := os.ReadFile(wolvenKitCommon)
	if err != nil {
		return err
	}
	oo := windows.NewLazyDLL(oodleDll)
	if err := oo.Load(); err != nil {
		return fmt.Errorf("could not load %s: %w", filepath.Base(oodleDll), err)
	}
	dec := oo.NewProc("OodleLZ_Decompress")
	if err := dec.Find(); err != nil {
		return err
	}
	for _, b := range modmap.KarkBlocks(dll) {
		if b.USize < 20<<20 {
			continue
		}
		dst := make([]byte, b.USize)
		r, _, _ := dec.Call(uintptr(unsafe.Pointer(&b.Data[0])), uintptr(len(b.Data)), uintptr(unsafe.Pointer(&dst[0])), uintptr(b.USize),
			1, 0, 0, 0, 0, 0, 0, 0, 0, 3)
		if int(r) == b.USize && bytes.HasPrefix(dst, []byte("base\\")) {
			return modmap.WriteNames(outGz, dst)
		}
	}
	return errors.New("no path list found in " + filepath.Base(wolvenKitCommon))
}

// Beep: two short tones through the default sound device when an F11 mark starts (rising) or ends (falling).
func Beep(on bool) {
	tones := [][2]uintptr{{880, 90}, {1320, 120}}
	if !on {
		tones = [][2]uintptr{{1320, 90}, {660, 160}}
	}
	for _, t := range tones {
		procBeep.Call(t[0], t[1])
	}
}

// Oodle returns a decompressor for the archives' Kraken streams, using the game's oo2ext DLL.
func Oodle(oodleDll string) (func(src []byte, size int) ([]byte, error), error) {
	oo := windows.NewLazyDLL(oodleDll)
	if err := oo.Load(); err != nil {
		return nil, err
	}
	dec := oo.NewProc("OodleLZ_Decompress")
	if err := dec.Find(); err != nil {
		return nil, err
	}
	return func(src []byte, size int) ([]byte, error) {
		if len(src) == 0 || size <= 0 {
			return nil, errors.New("empty")
		}
		dst := make([]byte, size)
		r, _, _ := dec.Call(uintptr(unsafe.Pointer(&src[0])), uintptr(len(src)), uintptr(unsafe.Pointer(&dst[0])), uintptr(size),
			1, 0, 0, 0, 0, 0, 0, 0, 0, 3)
		if int(r) != size {
			return nil, errors.New("decompress failed")
		}
		return dst, nil
	}, nil
}
