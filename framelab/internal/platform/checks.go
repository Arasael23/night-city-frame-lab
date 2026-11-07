package platform

// Facts for the Checks tab: things that quietly cost frames or add stutter. Everything here is
// read only; the few fixes (Fault Tolerant Heap, debug logging) are separate calls that keep what
// was there for Undo.

import (
	"encoding/binary"
	"strings"
)

type DisplayFact struct {
	Name    string `json:"name"`    // adapter output, e.g. \\.\DISPLAY1
	Monitor string `json:"monitor"` // monitor name when Windows knows it
	Primary bool   `json:"primary"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Hz      int    `json:"hz"`     // current refresh rate
	MaxHz   int    `json:"max_hz"` // highest refresh rate offered at this resolution
}

type RAMFact struct {
	Known    bool   `json:"known"`
	Type     string `json:"type"`      // DDR4, DDR5 ...
	Modules  int    `json:"modules"`   // filled slots
	SizeGB   int    `json:"size_gb"`   // total
	MTs      int    `json:"mts"`       // configured (running) speed
	RatedMTs int    `json:"rated_mts"` // what the modules report as their speed (usually the JEDEC stock speed)
}

type DriveFact struct {
	Root    string  `json:"root"`  // C:\
	Bus     string  `json:"bus"`   // NVMe, SATA, USB, RAID ...
	SSD     bool    `json:"ssd"`   // no seek penalty
	Known   bool    `json:"known"` // bus/SSD read
	Model   string  `json:"model"`
	FreeGB  float64 `json:"free_gb"`
	TotalGB float64 `json:"total_gb"`
}

type PCIeFact struct {
	Known    bool `json:"known"`
	CurGen   int  `json:"cur_gen"`
	CurWidth int  `json:"cur_width"`
	MaxGen   int  `json:"max_gen"`
	MaxWidth int  `json:"max_width"`
}

type MSIFact struct {
	Device string `json:"device"` // adapter name
	MSI    bool   `json:"msi"`    // message-signalled interrupts on
	Known  bool   `json:"known"`
}

type CheckFacts struct {
	Displays        []DisplayFact     `json:"displays"`
	RTSS            bool              `json:"rtss"`   // RivaTuner Statistics Server running
	HWiNFO          bool              `json:"hwinfo"` // HWiNFO shared memory open
	PawnIO          string            `json:"pawnio"` // installed version, "" = not installed
	RAM             RAMFact           `json:"ram"`
	PCIe            PCIeFact          `json:"pcie"`
	MSI             []MSIFact         `json:"msi"`
	VCacheSvc       string            `json:"vcache_svc"`   // "running", "stopped", "" = not installed
	BootTimer       map[string]string `json:"boot_timer"`   // bcdedit values that change the timer (useplatformclock ...)
	GlobalTimer     bool              `json:"global_timer"` // GlobalTimerResolutionRequests = 1
	ReservedCpuSets bool              `json:"reserved_cpu_sets"`
	FTH             []string          `json:"fth"`  // Fault Tolerant Heap entries for the game
	HVCI            bool              `json:"hvci"` // memory integrity running
	VBS             bool              `json:"vbs"`  // virtualisation-based security switched on
}

// ---------------------------------------------------------------- SMBIOS memory (type 17)

// ParseSMBIOSMemory reads the memory devices (type 17) from a raw SMBIOS table as Windows returns
// it (GetSystemFirmwareTable 'RSMB': an 8-byte header, then the structures).
func ParseSMBIOSMemory(raw []byte) RAMFact {
	var f RAMFact
	if len(raw) < 8 {
		return f
	}
	n := int(binary.LittleEndian.Uint32(raw[4:8]))
	t := raw[8:]
	if n < len(t) {
		t = t[:n]
	}
	speeds := map[int]int{}
	for i := 0; i+4 <= len(t); {
		typ, ln := t[i], int(t[i+1])
		if ln < 4 || i+ln > len(t) {
			break
		}
		s := t[i : i+ln]
		if typ == 17 && ln >= 0x17 {
			size := int(binary.LittleEndian.Uint16(s[0x0C:]))
			if size != 0 && size != 0xFFFF {
				mb := size & 0x7FFF
				if size&0x8000 != 0 {
					mb = mb / 1024 // size given in KB
				}
				if size == 0x7FFF && ln >= 0x20 {
					mb = int(binary.LittleEndian.Uint32(s[0x1C:]))
				}
				f.Modules++
				f.SizeGB += mb / 1024
				switch s[0x12] {
				case 0x1A:
					f.Type = "DDR4"
				case 0x22:
					f.Type = "DDR5"
				case 0x18:
					f.Type = "DDR3"
				case 0x1E:
					f.Type = "LPDDR4"
				case 0x23:
					f.Type = "LPDDR5"
				}
				rated := int(binary.LittleEndian.Uint16(s[0x15:]))
				if rated == 0xFFFF && ln >= 0x58 {
					rated = int(binary.LittleEndian.Uint32(s[0x54:]))
				}
				conf := 0
				if ln >= 0x22 {
					conf = int(binary.LittleEndian.Uint16(s[0x20:]))
					if conf == 0xFFFF && ln >= 0x5C {
						conf = int(binary.LittleEndian.Uint32(s[0x58:]))
					}
				}
				if rated > f.RatedMTs {
					f.RatedMTs = rated
				}
				if conf > 0 {
					speeds[conf]++
				}
			}
		}
		// skip the string set after the formatted part (ends with a double zero)
		j := i + ln
		for j+1 < len(t) && !(t[j] == 0 && t[j+1] == 0) {
			j++
		}
		i = j + 2
		if typ == 127 {
			break
		}
	}
	best := 0
	for sp, c := range speeds {
		if c > speeds[best] || (c == speeds[best] && sp > best) {
			best = sp
		}
	}
	f.MTs = best
	f.Known = f.Modules > 0
	return f
}

// RAMStock: the speed reads like a stock JEDEC speed (no XMP / EXPO profile loaded)
func RAMStock(f RAMFact) bool {
	if !f.Known || f.MTs == 0 {
		return false
	}
	if f.RatedMTs > 0 && f.MTs > f.RatedMTs {
		return false
	}
	jedec := map[string][]int{"DDR4": {1600, 1866, 2133, 2400, 2666, 2933, 3200}, "DDR5": {4000, 4400, 4800, 5200, 5600}}
	for _, v := range jedec[f.Type] {
		if f.MTs == v || f.MTs == v-1 || f.MTs == v+1 {
			return true
		}
	}
	return false
}

// CPU names with two core groups where only one has the extra cache
func TwoGroupX3D(cpu string) bool {
	u := strings.ToUpper(cpu)
	for _, m := range []string{"7900X3D", "7950X3D", "9900X3D", "9950X3D"} {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// BusName for a STORAGE_BUS_TYPE value
func BusName(t uint32) string {
	switch t {
	case 0x11:
		return "NVMe"
	case 0x0B:
		return "SATA"
	case 0x07:
		return "USB"
	case 0x08:
		return "RAID"
	case 0x0A:
		return "SAS"
	case 0x03:
		return "ATA"
	case 0x0E:
		return "Virtual"
	case 0x0F:
		return "Storage Spaces"
	}
	return "other"
}

func verString(ms, ls uint32) string {
	return itoa(int(ms>>16)) + "." + itoa(int(ms&0xFFFF)) + "." + itoa(int(ls>>16)) + "." + itoa(int(ls&0xFFFF))
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	s := ""
	for v > 0 {
		s = string(rune('0'+v%10)) + s
		v /= 10
	}
	return s
}
