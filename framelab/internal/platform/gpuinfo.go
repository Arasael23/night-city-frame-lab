package platform

import (
	"regexp"
	"strconv"
	"strings"
)

// GpuInfo: the graphics card Frame Lab reads during a session, where the readings come from and
// which of them that card's driver gives.
type GpuInfo struct {
	Name   string   `json:"name"`
	Vendor string   `json:"vendor"` // "nvidia", "amd", "intel" or "" (unknown)
	Source string   `json:"source"` // e.g. "NVIDIA driver (NVML)", "AMD driver (ADLX)", "Windows"
	Reads  []string `json:"reads"`  // of ReadClock, ReadTemp, ReadPower, ReadUsage, ReadVram, ReadSlowdown
	Note   string   `json:"note,omitempty"`
}

// what a card's driver can give Frame Lab
const (
	ReadClock    = "clock"
	ReadTemp     = "temperature"
	ReadPower    = "power"
	ReadUsage    = "usage"
	ReadVram     = "VRAM"
	ReadSlowdown = "slowdown reasons" // NVIDIA only: why the card lowered its clocks (power limit, heat)
)

func (g GpuInfo) Has(r string) bool {
	for _, x := range g.Reads {
		if x == r {
			return true
		}
	}
	return false
}

func vendorOf(id uint32) string {
	switch id {
	case 0x10de:
		return "nvidia"
	case 0x1002, 0x1022:
		return "amd"
	case 0x8086:
		return "intel"
	}
	return ""
}

// pickMain chooses the card games run on: the one with the most memory of its own, integrated
// graphics only when there is nothing else (laptops without a graphics card, handhelds).
func pickMain(ads []GpuAdapter) (GpuAdapter, bool) {
	best := -1
	for i, a := range ads {
		if best < 0 {
			best = i
			continue
		}
		b := ads[best]
		if a.Integrated != b.Integrated {
			if !a.Integrated {
				best = i
			}
			continue
		}
		if a.MemMB > b.MemMB {
			best = i
		}
	}
	if best < 0 {
		return GpuAdapter{}, false
	}
	return ads[best], true
}

// sameCard: a vendor tool's name for a card against Windows' (DXGI) name, ignoring (TM)/(R) marks,
// case and spacing.
func sameCard(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(s)
		for _, m := range []string{"(tm)", "(r)", "™", "®"} {
			s = strings.ReplaceAll(s, m, "")
		}
		return strings.Join(strings.Fields(s), " ")
	}
	return a != "" && norm(a) == norm(b)
}

// parseLuid: "0x00000000_0x0000d1a6" -> high, low
func parseLuid(s string) (uint32, uint32, bool) {
	p := strings.Split(s, "_")
	if len(p) != 2 {
		return 0, 0, false
	}
	h, e1 := strconv.ParseUint(strings.TrimPrefix(p[0], "0x"), 16, 32)
	l, e2 := strconv.ParseUint(strings.TrimPrefix(p[1], "0x"), 16, 32)
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return uint32(h), uint32(l), true
}

// gpuUsage: Task Manager's GPU number for one card: per engine, what every process uses of it
// added up; the busiest engine.
func gpuUsage(eng map[string]float64, luid string) (float64, bool) {
	per := map[string]float64{}
	for inst, v := range eng {
		if luidOf(inst) != luid {
			continue
		}
		if i := strings.Index(inst, "_phys_"); i >= 0 {
			per[inst[i:]] += v
		}
	}
	if len(per) == 0 {
		return 0, false
	}
	best := 0.0
	for _, v := range per {
		if v > best {
			best = v
		}
	}
	if best > 100 {
		best = 100
	}
	return best, true
}

// adapterMemMB: the card's own memory in use (GPU Adapter Memory counters).
func adapterMemMB(ad map[string]float64, luid string) (float64, bool) {
	sum, ok := 0.0, false
	for inst, v := range ad {
		if luidOf(inst) == luid {
			sum += v / 1048576
			ok = true
		}
	}
	return sum, ok
}

var reEng3D = regexp.MustCompile(`(?i)_eng_(\d+)_engtype_3d$`)

// node3DFromEngines: the 3D engine's node number on one card, from the GPU Engine counter names
// ("pid_1234_luid_0x..._phys_0_eng_0_engtype_3D"); -1 if none.
func node3DFromEngines(eng map[string]float64, luid string) int32 {
	for inst := range eng {
		if luidOf(inst) != luid {
			continue
		}
		if m := reEng3D.FindStringSubmatch(inst); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n < 64 {
				return int32(n)
			}
		}
	}
	return -1
}
