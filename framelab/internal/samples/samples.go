// Package samples defines the once-every-100-ms PC/GPU readings Frame Lab takes from outside the game
// (NVIDIA driver + Windows performance counters) and their CSV file.
package samples

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Sample struct {
	Qpc             int64
	GpuClockMHz     float64
	MemClockMHz     float64
	GpuPowerW       float64
	GpuTempC        float64
	GpuUtilPct      float64 // whole GPU (NVML)
	EncUtilPct      float64 // NVENC (OBS recording/streaming)
	VramUsedMB      float64 // whole GPU (NVML)
	VramTotalMB     float64
	Throttle        uint64  // NVML clock event reasons bitmask
	GameCpuPct      float64 // game process CPU, % of the whole CPU
	MaxCorePct      float64 // busiest logical core
	GameReadMBs     float64 // game disk reads
	GameWorkingMB   float64
	GameGpu3DPct    float64 // game's share of the 3D engine
	OtherGpuPct     float64 // busiest other process on the GPU (3D+compute+encode)
	OtherGpuName    string
	GameDedicatedMB float64 // game dedicated GPU memory (Windows counter)
	AvailRamMB      float64
	CpuClockMHz     float64 // average effective clock of all cores (base clock x % Processor Performance)
	CpuTotalPct     float64 // whole CPU busy
	CoresBusy       float64 // logical cores at 90% or more
	DpcPct          float64 // CPU time in driver DPCs (all cores, % of one core's time x cores / cores)
	IntPct          float64 // CPU time in hardware interrupts
	PagesSec        float64 // hard page faults per second (memory read back from disk)
	GameSharedMB    float64 // game's GPU memory kept in system RAM (VRAM spilled over)
	CommitMB        float64 // memory Windows has promised to programs (RAM + page file), 1.3.4+
	CommitLimitMB   float64 // the most it can promise; past it, memory requests fail
	CpuTempC        float64 // CPU temperature (HWiNFO / PawnIO), 0 = not read (2.0)
}

var Header = []string{"qpc", "gpu_clock_mhz", "mem_clock_mhz", "gpu_power_w", "gpu_temp_c", "gpu_util_pct", "enc_util_pct",
	"vram_used_mb", "vram_total_mb", "throttle", "game_cpu_pct", "max_core_pct", "game_read_mbs", "game_working_mb",
	"game_gpu3d_pct", "other_gpu_pct", "other_gpu_name", "game_dedicated_mb", "avail_ram_mb",
	"cpu_clock_mhz", "cpu_total_pct", "cores_busy", "dpc_pct", "int_pct", "pages_sec", "game_shared_mb",
	"commit_mb", "commit_limit_mb", "cpu_temp_c"}

type Writer struct {
	f  *os.File
	bw *bufio.Writer
	w  *csv.Writer
}

func Create(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	bw := bufio.NewWriterSize(f, 1<<16)
	w := csv.NewWriter(bw)
	if err := w.Write(Header); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, bw: bw, w: w}, nil
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func (w *Writer) Write(s *Sample) error {
	return w.w.Write([]string{strconv.FormatInt(s.Qpc, 10), ff(s.GpuClockMHz), ff(s.MemClockMHz), ff(s.GpuPowerW), ff(s.GpuTempC),
		ff(s.GpuUtilPct), ff(s.EncUtilPct), ff(s.VramUsedMB), ff(s.VramTotalMB), fmt.Sprintf("%d", s.Throttle), ff(s.GameCpuPct),
		ff(s.MaxCorePct), ff(s.GameReadMBs), ff(s.GameWorkingMB), ff(s.GameGpu3DPct), ff(s.OtherGpuPct), s.OtherGpuName,
		ff(s.GameDedicatedMB), ff(s.AvailRamMB),
		ff(s.CpuClockMHz), ff(s.CpuTotalPct), ff(s.CoresBusy), ff(s.DpcPct), ff(s.IntPct), ff(s.PagesSec), ff(s.GameSharedMB),
		ff(s.CommitMB), ff(s.CommitLimitMB), ff(s.CpuTempC)})
}

func (w *Writer) Flush() { w.w.Flush(); w.bw.Flush() }

func (w *Writer) Close() error {
	w.Flush()
	return w.f.Close()
}

func Read(path string) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range head {
		idx[h] = i
	}
	var out []Sample
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		g := func(n string) float64 {
			i, ok := idx[n]
			if !ok || i >= len(rec) {
				return 0
			}
			v, _ := strconv.ParseFloat(strings.TrimSpace(rec[i]), 64)
			return v
		}
		s := Sample{}
		if i, ok := idx["qpc"]; ok {
			s.Qpc, _ = strconv.ParseInt(rec[i], 10, 64)
		}
		s.GpuClockMHz, s.MemClockMHz, s.GpuPowerW, s.GpuTempC = g("gpu_clock_mhz"), g("mem_clock_mhz"), g("gpu_power_w"), g("gpu_temp_c")
		s.GpuUtilPct, s.EncUtilPct, s.VramUsedMB, s.VramTotalMB = g("gpu_util_pct"), g("enc_util_pct"), g("vram_used_mb"), g("vram_total_mb")
		s.Throttle = uint64(g("throttle"))
		s.GameCpuPct, s.MaxCorePct, s.GameReadMBs, s.GameWorkingMB = g("game_cpu_pct"), g("max_core_pct"), g("game_read_mbs"), g("game_working_mb")
		s.GameGpu3DPct, s.OtherGpuPct = g("game_gpu3d_pct"), g("other_gpu_pct")
		if i, ok := idx["other_gpu_name"]; ok && i < len(rec) {
			s.OtherGpuName = rec[i]
		}
		s.GameDedicatedMB, s.AvailRamMB = g("game_dedicated_mb"), g("avail_ram_mb")
		s.CpuClockMHz, s.CpuTotalPct, s.CoresBusy = g("cpu_clock_mhz"), g("cpu_total_pct"), g("cores_busy")
		s.DpcPct, s.IntPct, s.PagesSec, s.GameSharedMB = g("dpc_pct"), g("int_pct"), g("pages_sec"), g("game_shared_mb")
		s.CommitMB, s.CommitLimitMB = g("commit_mb"), g("commit_limit_mb")
		s.CpuTempC = g("cpu_temp_c")
		out = append(out, s)
	}
	return out, nil
}

// Throttle bits (NVML clocks event reasons) that mean the GPU slowed itself down.
const (
	ThrottleSwPowerCap   = 0x4
	ThrottleHwSlowdown   = 0x8
	ThrottleSwThermal    = 0x20
	ThrottleHwThermal    = 0x40
	ThrottleHwPowerBrake = 0x80
	ThrottleSlowdownMask = ThrottleSwPowerCap | ThrottleHwSlowdown | ThrottleSwThermal | ThrottleHwThermal | ThrottleHwPowerBrake
)

// Serious reports slowdowns beyond the normal power limit (heat or hardware slowdown).
func Serious(bits uint64) bool {
	return bits&(ThrottleSwThermal|ThrottleHwThermal|ThrottleHwSlowdown|ThrottleHwPowerBrake) != 0
}

func ThrottleText(bits uint64) string {
	var p []string
	if bits&ThrottleSwPowerCap != 0 {
		p = append(p, "power limit")
	}
	if bits&(ThrottleSwThermal|ThrottleHwThermal) != 0 {
		p = append(p, "temperature")
	}
	if bits&(ThrottleHwSlowdown|ThrottleHwPowerBrake) != 0 {
		p = append(p, "hardware slowdown")
	}
	return strings.Join(p, ", ")
}
