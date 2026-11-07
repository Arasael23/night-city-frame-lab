//go:build !windows

// Development stand-ins so the app and its UI can run and be tested on Linux.
// FRAMELAB_FAKE_GAME=1 pretends the game runs; FRAMELAB_FAKE_STATE=<file> supplies a 640-byte HitchRecorder block.
package platform

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"framelab/internal/samples"
)

var start = time.Now()

func QPC() int64     { return int64(time.Since(start).Nanoseconds() / 100) }
func QPCFreq() int64 { return 10_000_000 }
func IsAdmin() bool  { return os.Getenv("FRAMELAB_FAKE_ADMIN") != "0" }

func SingleInstance() bool          { return true }
func MessageBox(title, text string) { println(title + ": " + text) }
func HideWindow(cmd *exec.Cmd)      {}
func OpenFolder(path string)        { println("open folder:", path) }
func OpenURL(url string)            { println("open url:", url) }

func FindGame() []GameProc {
	if f := os.Getenv("FRAMELAB_FAKE_GAME_FILE"); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			return []GameProc{{Pid: 4242, Path: string(b)}}
		}
	}
	return nil
}

func ReadHitchState() *HitchState {
	p := os.Getenv("FRAMELAB_FAKE_STATE")
	if p == "" {
		return &HitchState{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return &HitchState{}
	}
	return ParseHitchState(b)
}

// fake cards for the Linux dev server: FRAMELAB_FAKE_GPU=amd or intel (default NVIDIA)
func GPU() GpuInfo {
	switch os.Getenv("FRAMELAB_FAKE_GPU") {
	case "amd":
		return GpuInfo{Name: "AMD Radeon RX 7800 XT", Vendor: "amd", Source: "AMD driver (ADLX) + Windows",
			Reads: []string{ReadClock, ReadTemp, ReadPower, ReadUsage, ReadVram}}
	case "intel":
		return GpuInfo{Name: "Intel(R) Arc(TM) B580 Graphics", Vendor: "intel", Source: "Windows",
			Reads: []string{ReadTemp, ReadUsage, ReadVram}}
	}
	return GpuInfo{Name: "Test GPU (dev)", Vendor: "nvidia", Source: "NVIDIA driver (NVML)",
		Reads: []string{ReadClock, ReadTemp, ReadPower, ReadUsage, ReadVram, ReadSlowdown}}
}

func GPUName() string { return GPU().Name }

// fakeRead keeps only what the fake card gives.
func fakeRead(s *samples.Sample) {
	g := GPU()
	if !g.Has(ReadClock) {
		s.GpuClockMHz, s.MemClockMHz = 0, 0
	}
	if !g.Has(ReadTemp) {
		s.GpuTempC = 0
	}
	if !g.Has(ReadPower) {
		s.GpuPowerW = 0
	}
	if !g.Has(ReadSlowdown) {
		s.Throttle = 0
	}
}

type fakeSampler struct {
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	last map[string]float64
}

func (s *fakeSampler) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
}
func (s *fakeSampler) Last() map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func StartSampler(gamePid uint32, out string) (Sampler, error) {
	w, err := samples.Create(out)
	if err != nil {
		return nil, err
	}
	s := &fakeSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go fakeGpuApps(filepath.Dir(out), s.stop, QPC)
	go func() {
		defer close(s.done)
		defer w.Close()
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
			x := float64(i)
			smp := samples.Sample{Qpc: QPC(), GpuClockMHz: 2790 - 30*math.Sin(x/40), GpuTempC: 66, GpuPowerW: 310, GpuUtilPct: 97,
				VramUsedMB: 14000, VramTotalMB: 16303, GameCpuPct: 40, MaxCorePct: 80, GameReadMBs: 20, OtherGpuPct: 9, OtherGpuName: "obs64.exe",
				CpuClockMHz: 5200 - 120*math.Sin(x/25), CpuTotalPct: 35, CoresBusy: 2, DpcPct: 0.8, IntPct: 0.4, PagesSec: 12, GameSharedMB: 420, AvailRamMB: 9000, CommitMB: 30000, CommitLimitMB: 51800}
			fakeRead(&smp)
			w.Write(&smp)
			s.mu.Lock()
			s.last = map[string]float64{"gpu_clock": smp.GpuClockMHz, "gpu_temp": smp.GpuTempC, "gpu_power": smp.GpuPowerW,
				"gpu_util": smp.GpuUtilPct, "vram_used": smp.VramUsedMB, "vram_total": smp.VramTotalMB, "game_cpu": smp.GameCpuPct,
				"disk": smp.GameReadMBs, "other_gpu": smp.OtherGpuPct}
			s.mu.Unlock()
		}
	}()
	return s, nil
}

func LiveGPU() map[string]float64 {
	s := samples.Sample{GpuClockMHz: 2790, GpuTempC: 52, GpuPowerW: 60}
	fakeRead(&s)
	out := map[string]float64{"gpu_util": 8, "vram_used": 2100, "vram_total": 16303}
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

func ExtractNames(wolvenKitCommon, oodleDll, outGz string) error {
	return os.ErrNotExist
}

func Beep(on bool) { println("beep", on) }

// Oodle: only on Windows (the game's oo2ext DLL).
func Oodle(oodleDll string) (func(src []byte, size int) ([]byte, error), error) {
	return nil, errors.New("Oodle only on Windows")
}

// KeyHit: no keyboard state outside Windows.
func KeyHit(vk int) bool { return false }

// SystemEvents: the Windows event log only exists on Windows.
func SystemEvents(since time.Time) []SysEvent { return nil }

// SystemVersions: dev stand-in.
func SystemVersions(gpuName string) (win, driver string) {
	return "Windows 11 24H2 (build 26100.1)", "621.01"
}

// CPUTemp: dev stand-in (a gentle curve).
type CPUTemp struct{ Source string }

func OpenCPUTemp(modDir string) *CPUTemp { return &CPUTemp{Source: "dev"} }
func (c *CPUTemp) Read() float64         { return 62 + 6*math.Sin(float64(time.Now().Unix())/20) }
func (c *CPUTemp) Close()                {}
