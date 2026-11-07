//go:build windows

package platform

// Frame Lab 2.0 Experiments: Game Mode, 3D V-Cache cores, timer resolution, CPU idle states,
// PCIe link power, the graphics card's MSI mode, memory compression, memory integrity (guided),
// and the quiet session (background services stopped for a capture).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	gameModeKey = `Software\Microsoft\GameBar` // per user (HKCU)
)

var (
	procNtQueryTimerResolution    = ntdll.NewProc("NtQueryTimerResolution")
	procNtSetTimerResolution      = ntdll.NewProc("NtSetTimerResolution")
	procGetLogicalProcessorInfoEx = kernel32.NewProc("GetLogicalProcessorInformationEx")
	reHex8                        = regexp.MustCompile(`0x[0-9a-fA-F]{8}`)
)

// ---------------------------------------------------------------- powercfg settings

// powerIndex: the active plan's AC and DC value of a power setting (-1 = could not read)
func powerIndex(sub, setting string) (ac, dc int) {
	out, err := run("powercfg", "/q", "scheme_current", sub, setting)
	if err != nil {
		return -1, -1
	}
	// the last two 0x........ values are the current AC and DC index (labels are translated)
	h := reHex8.FindAllString(out, -1)
	if len(h) < 2 {
		return -1, -1
	}
	a, _ := strconv.ParseInt(h[len(h)-2][2:], 16, 64)
	d, _ := strconv.ParseInt(h[len(h)-1][2:], 16, 64)
	return int(a), int(d)
}

func setPowerIndex(sub, setting string, ac, dc int) error {
	if _, err := run("powercfg", "/setacvalueindex", "scheme_current", sub, setting, strconv.Itoa(ac)); err != nil {
		return fmt.Errorf("powercfg refused %s", setting)
	}
	if dc >= 0 {
		run("powercfg", "/setdcvalueindex", "scheme_current", sub, setting, strconv.Itoa(dc))
	}
	_, err := run("powercfg", "/setactive", "scheme_current")
	return err
}

// ---------------------------------------------------------------- timer resolution

// TimerResolution: Windows' current timer resolution in milliseconds (0 = unknown)
func TimerResolution() float64 {
	var min, max, cur uint32
	r, _, _ := procNtQueryTimerResolution.Call(uintptr(unsafe.Pointer(&min)), uintptr(unsafe.Pointer(&max)), uintptr(unsafe.Pointer(&cur)))
	if r != 0 {
		return 0
	}
	return float64(cur) / 10000
}

// HoldTimer asks for (on) or gives back (off) a 0.5 ms timer for as long as Frame Lab runs.
func HoldTimer(on bool) {
	var actual uint32
	set := uintptr(0)
	if on {
		set = 1
	}
	procNtSetTimerResolution.Call(5000, set, uintptr(unsafe.Pointer(&actual)))
}

func windows11() bool {
	maj, _, build := windows.RtlGetNtVersionNumbers()
	return maj >= 10 && build >= 22000
}

// ---------------------------------------------------------------- 3D V-Cache cores

// x3dSets: the CPU set ids of the cores that share the biggest L3 cache, on CPUs with two L3s of
// different size (Ryzen 7900X3D / 7950X3D / 9900X3D / 9950X3D). nil = not that kind of CPU.
func x3dSets() []uint32 {
	const relationCache = 2
	var need uint32
	procGetLogicalProcessorInfoEx.Call(relationCache, 0, uintptr(unsafe.Pointer(&need)))
	if need == 0 || need > 1<<20 {
		return nil
	}
	buf := make([]byte, need)
	r, _, _ := procGetLogicalProcessorInfoEx.Call(relationCache, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&need)))
	if r == 0 {
		return nil
	}
	le := binary.LittleEndian
	type l3 struct {
		size  uint32
		group uint16
		mask  uint64
	}
	var caches []l3
	// SYSTEM_LOGICAL_PROCESSOR_INFORMATION_EX: Relationship u32, Size u32, then CACHE_RELATIONSHIP:
	// Level u8, Associativity u8, LineSize u16, CacheSize u32, Type u32, Reserved[18], GroupCount u16,
	// GroupMask (GROUP_AFFINITY: Mask u64, Group u16, Reserved u16[3]) at 40
	for off := 0; off+56 <= int(need); {
		rel := le.Uint32(buf[off:])
		size := int(le.Uint32(buf[off+4:]))
		if size < 8 || off+size > int(need) {
			break
		}
		if rel == relationCache && buf[off+8] == 3 && size >= 56 {
			caches = append(caches, l3{size: le.Uint32(buf[off+12:]), mask: le.Uint64(buf[off+40:]), group: le.Uint16(buf[off+48:])})
		}
		off += size
	}
	if len(caches) < 2 {
		return nil
	}
	big, small := caches[0], caches[0]
	for _, c := range caches {
		if c.size > big.size {
			big = c
		}
		if c.size < small.size {
			small = c
		}
	}
	if big.size <= small.size {
		return nil // same size: not a one-V-Cache-CCD part
	}
	var ids []uint32
	for _, s := range cpuSetsFull() {
		if s.group == big.group && s.lp < 64 && big.mask&(1<<s.lp) != 0 {
			ids = append(ids, s.id)
		}
	}
	return ids
}

type cpuSetF struct {
	id    uint32
	group uint16
	lp    uint8
}

func cpuSetsFull() []cpuSetF {
	var need uint32
	procGetSystemCpuSetInformation.Call(0, 0, uintptr(unsafe.Pointer(&need)), 0, 0)
	if need == 0 || need > 1<<20 {
		return nil
	}
	buf := make([]byte, need)
	r, _, _ := procGetSystemCpuSetInformation.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(need), uintptr(unsafe.Pointer(&need)), 0, 0)
	if r == 0 {
		return nil
	}
	le := binary.LittleEndian
	var out []cpuSetF
	for off := 0; off+20 <= int(need); {
		size := int(le.Uint32(buf[off:]))
		if size < 20 || off+size > int(need) {
			break
		}
		if le.Uint32(buf[off+4:]) == 0 {
			out = append(out, cpuSetF{id: le.Uint32(buf[off+8:]), group: le.Uint16(buf[off+12:]), lp: buf[off+14]})
		}
		off += size
	}
	return out
}

// PinGameX3D keeps every running Cyberpunk2077.exe on the V-Cache cores (once per process).
func PinGameX3D() int {
	ids := x3dSets()
	if len(ids) == 0 {
		return 0
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	n := 0
	for _, g := range FindGame() {
		if pinned[g.Pid] {
			continue
		}
		if setCpuSets(g.Pid, ids) {
			pinned[g.Pid] = true
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- the graphics card's MSI mode

// gpuInstances: Enum keys (under ...\Enum\PCI) of the display adapters present in the PC
func gpuInstances() []string {
	var out []string
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\PCI`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return out
	}
	defer root.Close()
	devs, _ := root.ReadSubKeyNames(-1)
	for _, d := range devs {
		u := strings.ToUpper(d)
		if !strings.HasPrefix(u, "VEN_10DE") && !strings.HasPrefix(u, "VEN_1002") && !strings.HasPrefix(u, "VEN_8086") {
			continue
		}
		dk, err := registry.OpenKey(root, d, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		insts, _ := dk.ReadSubKeyNames(-1)
		dk.Close()
		for _, in := range insts {
			ik, err := registry.OpenKey(root, d+`\`+in, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			cls, _, _ := ik.GetStringValue("ClassGUID")
			ik.Close()
			if !strings.EqualFold(cls, "{4d36e968-e325-11ce-bfc1-08002be10318}") {
				continue
			}
			if ck, err := registry.OpenKey(root, d+`\`+in+`\Control`, registry.QUERY_VALUE); err == nil {
				ck.Close()
				out = append(out, `SYSTEM\CurrentControlSet\Enum\PCI\`+d+`\`+in)
			}
		}
	}
	return out
}

const msiSub = `\Device Parameters\Interrupt Management\MessageSignaledInterruptProperties`

// ---------------------------------------------------------------- services for the quiet session

var quietServices = []string{"WSearch", "wuauserv", "SysMain"}

func svcControl(name string, stop bool) bool {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(m)
	n16, _ := windows.UTF16PtrFromString(name)
	s, err := windows.OpenService(m, n16, windows.SERVICE_QUERY_STATUS|windows.SERVICE_STOP|windows.SERVICE_START)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(s)
	var st windows.SERVICE_STATUS
	if windows.QueryServiceStatus(s, &st) != nil {
		return false
	}
	if stop {
		if st.CurrentState != windows.SERVICE_RUNNING {
			return false
		}
		if windows.ControlService(s, windows.SERVICE_CONTROL_STOP, &st) != nil {
			return false
		}
		for i := 0; i < 20 && st.CurrentState != windows.SERVICE_STOPPED; i++ {
			time.Sleep(250 * time.Millisecond)
			windows.QueryServiceStatus(s, &st)
		}
		return true
	}
	if st.CurrentState == windows.SERVICE_RUNNING {
		return true
	}
	return windows.StartService(s, 0, nil) == nil
}

// QuietStart stops Windows Search, Windows Update and SysMain if they run; returns the ones it stopped.
func QuietStart() []string {
	var out []string
	for _, n := range quietServices {
		if svcControl(n, true) {
			out = append(out, n)
		}
	}
	return out
}

// QuietEnd starts again the services QuietStart stopped.
func QuietEnd(names []string) {
	for _, n := range names {
		svcControl(n, false)
	}
}

// OpenCoreIsolation opens Windows Security at Core isolation (memory integrity).
func OpenCoreIsolation() error {
	_, err := run("explorer.exe", "windowsdefender://coreisolation")
	if err != nil && !strings.Contains(err.Error(), "exit status 1") { // explorer returns 1 for URIs
		return err
	}
	return nil
}

// ---------------------------------------------------------------- read / on / restore

func exp2Read(id, game string) (on bool, detail string, ok bool, handled bool) {
	switch id {
	case "gamemode":
		v, has := hkcuDword(gameModeKey, "AutoGameModeEnabled")
		if has && v == 0 {
			return true, "Game Mode is off", true, true
		}
		return false, "Game Mode is on (Windows default)", true, true
	case "x3d":
		c := CPU()
		if !TwoGroupX3D(c.Name) {
			return false, "only for Ryzen 7900X3D / 7950X3D / 9900X3D / 9950X3D (this PC: " + c.Name + ")", false, true
		}
		ids := x3dSets()
		if len(ids) == 0 {
			return false, "could not find which cores have the 3D V-Cache", false, true
		}
		return false, fmt.Sprintf("%d logical cores share the 3D V-Cache", len(ids)), true, true
	case "timer":
		cur := TimerResolution()
		d := fmt.Sprintf("Windows timer now %.2f ms", cur)
		if windows11() {
			if v, has := regDword(kernelKey, "GlobalTimerResolutionRequests"); has && v == 1 {
				d += " · Windows 11 global timer requests on"
			} else {
				d += " · Windows 11 global timer requests off (default)"
			}
		}
		return false, d, true, true
	case "idle":
		ac, _ := powerIndex("sub_processor", "IDLEDISABLE")
		if ac < 0 {
			return false, "could not read the power plan's idle setting", false, true
		}
		return ac == 1, map[bool]string{true: "CPU idle states disabled in the active plan", false: "CPU idle states allowed (default)"}[ac == 1], true, true
	case "aspm":
		ac, _ := powerIndex("sub_pciexpress", "ASPM")
		if ac < 0 {
			return false, "could not read the power plan's PCIe setting", false, true
		}
		names := []string{"Off", "Moderate power savings", "Maximum power savings"}
		n := "unknown"
		if ac >= 0 && ac < len(names) {
			n = names[ac]
		}
		return ac == 0, "Link State Power Management: " + n, true, true
	case "msi":
		gs := gpuInstances()
		if len(gs) == 0 {
			return false, "no graphics card found", false, true
		}
		all := true
		for _, g := range gs {
			if v, has := regDword(g+msiSub, "MSISupported"); !has || v != 1 {
				all = false
			}
		}
		if all {
			return true, "the graphics card uses MSI mode", true, true
		}
		return false, "the graphics card uses line-based interrupts", true, true
	case "memcomp":
		out, err := ps(`$ErrorActionPreference='Stop'; (Get-MMAgent).MemoryCompression`)
		if err != nil {
			return false, "could not read Windows' memory settings", false, true
		}
		if strings.EqualFold(strings.TrimSpace(out), "False") {
			return true, "memory compression is off", true, true
		}
		return false, "memory compression is on (Windows default)", true, true
	case "hvci":
		h, v := codeIntegrity()
		switch {
		case h:
			return true, "memory integrity is on", true, true
		case v:
			return false, "memory integrity is off (virtualization-based security is on)", true, true
		}
		return false, "memory integrity is off", true, true
	case "quiet":
		return false, "", true, true
	}
	return false, "", false, false
}

func exp2On(id, game string) (map[string]string, bool, error) {
	prev := map[string]string{}
	switch id {
	case "gamemode":
		prev["v"] = hkcuGet(gameModeKey, "AutoGameModeEnabled")
		return prev, true, hkcuSetDword(gameModeKey, "AutoGameModeEnabled", 0)
	case "x3d":
		if len(x3dSets()) == 0 {
			return nil, true, errors.New("not a CPU with one 3D V-Cache die")
		}
		return prev, true, nil // Frame Lab pins the game when it sees it start
	case "timer":
		if windows11() {
			prev["global"] = regGet(kernelKey, "GlobalTimerResolutionRequests")
			if err := regSetDword(kernelKey, "GlobalTimerResolutionRequests", 1); err != nil {
				return nil, true, err
			}
		}
		HoldTimer(true)
		return prev, true, nil
	case "idle":
		ac, dc := powerIndex("sub_processor", "IDLEDISABLE")
		if ac < 0 {
			return nil, true, errors.New("could not read the power plan's idle setting")
		}
		prev["ac"], prev["dc"] = strconv.Itoa(ac), strconv.Itoa(dc)
		return prev, true, setPowerIndex("sub_processor", "IDLEDISABLE", 1, 1)
	case "aspm":
		ac, dc := powerIndex("sub_pciexpress", "ASPM")
		if ac < 0 {
			return nil, true, errors.New("could not read the power plan's PCIe setting")
		}
		prev["ac"], prev["dc"] = strconv.Itoa(ac), strconv.Itoa(dc)
		return prev, true, setPowerIndex("sub_pciexpress", "ASPM", 0, 0)
	case "msi":
		gs := gpuInstances()
		if len(gs) == 0 {
			return nil, true, errors.New("no graphics card found")
		}
		for i, g := range gs {
			prev[fmt.Sprintf("key%d", i)] = g
			prev[fmt.Sprintf("val%d", i)] = regGet(g+msiSub, "MSISupported")
			if err := regSetDword(g+msiSub, "MSISupported", 1); err != nil {
				return nil, true, err
			}
		}
		return prev, true, nil
	case "memcomp":
		prev["was"] = "True"
		if _, err := ps(`$ErrorActionPreference='Stop'; Disable-MMAgent -MemoryCompression`); err != nil {
			return nil, true, errors.New("Windows refused to change memory compression")
		}
		return prev, true, nil
	case "hvci":
		return nil, true, errors.New("Frame Lab doesn't switch memory integrity: use Open Windows Security")
	case "quiet":
		return prev, true, nil
	}
	return nil, false, nil
}

func exp2Restore(id, game string, prev map[string]string) (bool, error) {
	switch id {
	case "gamemode":
		was := prev["v"]
		if was == "" {
			was = "absent"
		}
		return true, hkcuRestore(gameModeKey, "AutoGameModeEnabled", was)
	case "x3d":
		UnpinGame()
		return true, nil
	case "timer":
		HoldTimer(false)
		if windows11() {
			was := prev["global"]
			if was == "" {
				was = "absent"
			}
			return true, regRestore(kernelKey, "GlobalTimerResolutionRequests", was)
		}
		return true, nil
	case "idle", "aspm":
		sub, set := "sub_processor", "IDLEDISABLE"
		def := 0
		if id == "aspm" {
			sub, set, def = "sub_pciexpress", "ASPM", 1 // Windows default for ASPM: moderate savings
		}
		ac, err := strconv.Atoi(prev["ac"])
		if err != nil {
			ac = def
		}
		dc, err := strconv.Atoi(prev["dc"])
		if err != nil {
			dc = -1
		}
		return true, setPowerIndex(sub, set, ac, dc)
	case "msi":
		if _, ok := prev["key0"]; !ok {
			return true, errors.New("switched on outside Frame Lab: Frame Lab doesn't know the card's old setting, so it leaves it")
		}
		for i := 0; ; i++ {
			k, ok := prev[fmt.Sprintf("key%d", i)]
			if !ok {
				break
			}
			if err := regRestore(k+msiSub, "MSISupported", prev[fmt.Sprintf("val%d", i)]); err != nil {
				return true, err
			}
		}
		return true, nil
	case "memcomp":
		_, err := ps(`$ErrorActionPreference='Stop'; Enable-MMAgent -MemoryCompression`)
		return true, err
	case "quiet", "hvci":
		return true, nil
	}
	return false, nil
}

func exp2Run(id string) (string, bool, error) {
	if id == "hvci" {
		return "Windows Security opened at Core isolation. Restart after changing memory integrity.", true, OpenCoreIsolation()
	}
	return "", false, nil
}
