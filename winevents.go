package analysis

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Windows event recorder (1.4+): windows.csv holds 10 ms buckets from a kernel ETW session:
// driver DPC / interrupt time per driver, hard page faults per process (memory read back from
// disk) and disk I/O per process. It is what the slow frames nothing inside the game explains
// get checked against.

const (
	FileWindows      = "windows.csv"
	FileWindowsStats = "windows_stats.json"
	gameExe          = "cyberpunk2077.exe"
)

type winRow struct {
	q      int64
	kind   string
	name   string
	pid    uint32
	n      int
	sumUs  float64
	maxUs  float64
	bytes  float64
}

type winStats struct {
	Events      map[string]uint64 `json:"events"`
	Rows        uint64            `json:"rows"`
	EventsLost  uint32            `json:"events_lost"`
	BuffersLost uint32            `json:"buffers_lost"`
	Drivers     int               `json:"drivers_known"`
	Error       string            `json:"error"`
	DriverTotals []struct {
		Name    string  `json:"name"`
		TotalUs float64 `json:"total_us"`
		MaxUs   float64 `json:"max_us"`
		Count   int     `json:"count"`
	} `json:"driver_totals"`
	FirstQpc int64 `json:"first_qpc"`
	LastQpc  int64 `json:"last_qpc"`
}

// WinTop is one driver or process over the whole session (while playing).
type WinTop struct {
	Name     string  `json:"name"`
	What     string  `json:"what,omitempty"` // plain words for a driver
	MsPerS   float64 `json:"ms_per_s,omitempty"`
	MaxMs    float64 `json:"max_ms,omitempty"`
	Count    int     `json:"count,omitempty"`
	MB       float64 `json:"mb,omitempty"`
	Spikes   int     `json:"spikes,omitempty"` // stutters it was put down to
}

type WinSummary struct {
	Recorded   bool              `json:"recorded"`
	Events     map[string]uint64 `json:"events,omitempty"`
	EventsLost uint32            `json:"events_lost,omitempty"`
	Error      string            `json:"error,omitempty"`
	Drivers    []WinTop          `json:"drivers,omitempty"`
	Paging     []WinTop          `json:"paging,omitempty"`
	Disk       []WinTop          `json:"disk,omitempty"`
	DriverSpk  int               `json:"driver_spikes"`
	PagingSpk  int               `json:"paging_spikes"`
	Unexplained int              `json:"unexplained_left"`
}

// WinItem: what Windows was doing during one spike.
type WinItem struct {
	Kind  string  `json:"kind"` // driver, paging, disk
	Name  string  `json:"name"`
	What  string  `json:"what,omitempty"`
	Ms    float64 `json:"ms,omitempty"`
	MaxMs float64 `json:"max_ms,omitempty"`
	Count int     `json:"count,omitempty"`
	MB    float64 `json:"mb,omitempty"`
}

var driverWhat = map[string]string{
	"nvlddmkm.sys": "NVIDIA graphics driver", "amdkmdag.sys": "AMD graphics driver", "igdkmd64.sys": "Intel graphics driver",
	"dxgkrnl.sys": "DirectX graphics kernel", "ntoskrnl.exe": "Windows kernel (timers)", "hal.dll": "Windows hardware layer",
	"ndis.sys": "network", "tcpip.sys": "network (TCP/IP)", "netio.sys": "network", "wdf01000.sys": "driver framework (USB and other devices)",
	"usbxhci.sys": "USB controller", "usbport.sys": "USB", "ucx01000.sys": "USB", "hidclass.sys": "keyboard / mouse / controller (HID)",
	"hdaudbus.sys": "audio", "portcls.sys": "audio", "ks.sys": "audio / video streaming", "acpi.sys": "ACPI (power / board)",
	"storport.sys": "storage", "stornvme.sys": "NVMe storage", "storahci.sys": "SATA storage", "iastorvd.sys": "Intel storage",
	"ataport.sys": "storage", "classpnp.sys": "storage", "volmgr.sys": "storage", "fltmgr.sys": "file system filters (antivirus etc.)",
	"wdfilter.sys": "Microsoft Defender", "ntfs.sys": "file system", "bthport.sys": "Bluetooth", "bthusb.sys": "Bluetooth",
	"rt640x64.sys": "Realtek network", "e1d68x64.sys": "Intel network", "e2f68.sys": "Intel network", "rtwlane.sys": "Realtek Wi-Fi",
	"netwtw10.sys": "Intel Wi-Fi", "netwtw14.sys": "Intel Wi-Fi", "netwtw08.sys": "Intel Wi-Fi", "mt7921e.sys": "MediaTek Wi-Fi",
	"rtkvhd64.sys": "Realtek audio", "nvhda64v.sys": "NVIDIA HDMI audio", "amdacpbus.sys": "AMD audio", "amdppm.sys": "AMD processor power",
	"intelppm.sys": "Intel processor power", "nvvad64v.sys": "NVIDIA virtual audio",
}

func DriverWhat(name string) string { return driverWhat[strings.ToLower(name)] }

func readWinRows(dir string) ([]winRow, error) {
	f, err := os.Open(filepath.Join(dir, FileWindows))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []winRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		p := strings.Split(sc.Text(), ",")
		if len(p) < 8 {
			continue
		}
		q, err := strconv.ParseInt(p[0], 10, 64)
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(p[3])
		n, _ := strconv.Atoi(p[4])
		s, _ := strconv.ParseFloat(p[5], 64)
		m, _ := strconv.ParseFloat(p[6], 64)
		b, _ := strconv.ParseFloat(p[7], 64)
		out = append(out, winRow{q, p[1], p[2], uint32(pid), n, s, m, b})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].q < out[j].q })
	return out, sc.Err()
}

type winLayer struct {
	rows    []winRow
	stats   *winStats
	bucket  int64
}

func loadWinLayer(dir string, freq float64) *winLayer {
	rows, err := readWinRows(dir)
	if err != nil {
		return nil
	}
	l := &winLayer{rows: rows, bucket: int64(freq / 100)}
	if b, err := os.ReadFile(filepath.Join(dir, FileWindowsStats)); err == nil {
		var st winStats
		if json.Unmarshal(b, &st) == nil {
			l.stats = &st
		}
	}
	return l
}

// in returns what Windows did during [q0, q1].
func (l *winLayer) in(q0, q1 int64) (items []WinItem, driverMs, driverMax, pagingMs float64, topDriver string) {
	lo := sort.Search(len(l.rows), func(i int) bool { return l.rows[i].q >= q0-l.bucket })
	type acc struct {
		ms, max, mb float64
		n           int
	}
	drv := map[string]*acc{}
	pg := map[string]*acc{}
	dk := map[string]*acc{}
	get := func(m map[string]*acc, k string) *acc {
		a := m[k]
		if a == nil {
			a = &acc{}
			m[k] = a
		}
		return a
	}
	for i := lo; i < len(l.rows) && l.rows[i].q <= q1; i++ {
		r := &l.rows[i]
		switch r.kind {
		case "dpc", "isr":
			a := get(drv, r.name)
			a.ms += r.sumUs / 1000
			a.max = math.Max(a.max, r.maxUs/1000)
			a.n += r.n
		case "hardfault":
			a := get(pg, r.name)
			a.ms += r.sumUs / 1000
			a.mb += r.bytes / (1 << 20)
			a.n += r.n
		case "diskread", "diskwrite":
			a := get(dk, r.name)
			a.mb += r.bytes / (1 << 20)
			a.max = math.Max(a.max, r.maxUs/1000)
			a.n += r.n
		}
	}
	for name, a := range drv {
		if a.ms >= 0.5 || a.max >= 0.5 {
			items = append(items, WinItem{Kind: "driver", Name: name, What: DriverWhat(name), Ms: round2(a.ms), MaxMs: round2(a.max), Count: a.n})
		}
		v := math.Max(a.ms, a.max)
		if v > driverMs {
			driverMs, driverMax, topDriver = v, a.max, name
		}
	}
	for name, a := range pg {
		items = append(items, WinItem{Kind: "paging", Name: name, Ms: round2(a.ms), Count: a.n, MB: round2(a.mb)})
		if strings.EqualFold(name, gameExe) {
			pagingMs += a.ms
		}
	}
	for name, a := range dk {
		if a.mb >= 1 || a.max >= 20 {
			items = append(items, WinItem{Kind: "disk", Name: name, MaxMs: round2(a.max), Count: a.n, MB: round2(a.mb)})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Ms+items[i].MB > items[j].Ms+items[j].MB })
	if len(items) > 8 {
		items = items[:8]
	}
	return
}

// summary over the time played
func (l *winLayer) summary(playing func(int64) bool, playS float64) *WinSummary {
	s := &WinSummary{Recorded: true}
	if l.stats != nil {
		s.Events, s.EventsLost, s.Error = l.stats.Events, l.stats.EventsLost, l.stats.Error
	}
	type acc struct {
		ms, max, mb float64
		n           int
	}
	drv, pg, dk := map[string]*acc{}, map[string]*acc{}, map[string]*acc{}
	get := func(m map[string]*acc, k string) *acc {
		a := m[k]
		if a == nil {
			a = &acc{}
			m[k] = a
		}
		return a
	}
	for i := range l.rows {
		r := &l.rows[i]
		if !playing(r.q) {
			continue
		}
		switch r.kind {
		case "dpc", "isr":
			a := get(drv, r.name)
			a.ms += r.sumUs / 1000
			a.max = math.Max(a.max, r.maxUs/1000)
			a.n += r.n
		case "hardfault":
			a := get(pg, r.name)
			a.ms += r.sumUs / 1000
			a.mb += r.bytes / (1 << 20)
			a.n += r.n
		case "diskread", "diskwrite":
			a := get(dk, r.name)
			a.mb += r.bytes / (1 << 20)
			a.max = math.Max(a.max, r.maxUs/1000)
			a.n += r.n
		}
	}
	ps := math.Max(playS, 1)
	if st := l.stats; st != nil && len(st.DriverTotals) > 0 && st.LastQpc > st.FirstQpc {
		// every DPC / interrupt of the recording (the 10 ms rows only keep the busier moments)
		recS := float64(st.LastQpc-st.FirstQpc) / (float64(l.bucket) * 100)
		for _, d := range st.DriverTotals {
			s.Drivers = append(s.Drivers, WinTop{Name: d.Name, What: DriverWhat(d.Name), MsPerS: round2(d.TotalUs / 1000 / recS), MaxMs: round2(d.MaxUs / 1000), Count: d.Count})
		}
	} else {
		for name, a := range drv {
			s.Drivers = append(s.Drivers, WinTop{Name: name, What: DriverWhat(name), MsPerS: round2(a.ms / ps), MaxMs: round2(a.max), Count: a.n})
		}
	}
	sort.Slice(s.Drivers, func(i, j int) bool { return s.Drivers[i].MsPerS > s.Drivers[j].MsPerS })
	for name, a := range pg {
		s.Paging = append(s.Paging, WinTop{Name: name, MsPerS: round2(a.ms / ps), Count: a.n, MB: round2(a.mb)})
	}
	sort.Slice(s.Paging, func(i, j int) bool { return s.Paging[i].MB > s.Paging[j].MB })
	for name, a := range dk {
		s.Disk = append(s.Disk, WinTop{Name: name, MaxMs: round2(a.max), Count: a.n, MB: round2(a.mb)})
	}
	sort.Slice(s.Disk, func(i, j int) bool { return s.Disk[i].MB > s.Disk[j].MB })
	for _, list := range []*[]WinTop{&s.Drivers, &s.Paging, &s.Disk} {
		if len(*list) > 10 {
			*list = (*list)[:10]
		}
	}
	return s
}
