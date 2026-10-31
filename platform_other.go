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

func GPUName() string { return "Test GPU (dev)" }

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
	return map[string]float64{"gpu_clock": 2790, "gpu_temp": 52, "gpu_power": 60, "gpu_util": 8, "vram_used": 2100, "vram_total": 16303}
}

func ExtractNames(wolvenKitCommon, oodleDll, outGz string) error {
	return os.ErrNotExist
}

func Beep(on bool) { println("beep", on) }

// Oodle: only on Windows (the game's oo2ext DLL).
func Oodle(oodleDll string) (func(src []byte, size int) ([]byte, error), error) {
	return nil, errors.New("Oodle only on Windows")
}
