//go:build windows

package platform

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32c  = windows.NewLazySystemDLL("user32.dll")
	enumDisp = user32c.NewProc("EnumDisplayDevicesW")
	enumSet  = user32c.NewProc("EnumDisplaySettingsW")
	k32c     = windows.NewLazySystemDLL("kernel32.dll")
	getFw    = k32c.NewProc("GetSystemFirmwareTable")
)

const (
	kernelKey = `SYSTEM\CurrentControlSet\Control\Session Manager\kernel`
	fthKey    = `SOFTWARE\Microsoft\FTH\State`
)

func ReadCheckFacts() CheckFacts {
	var f CheckFacts
	f.Displays = displays()
	for _, n := range processes() {
		if strings.EqualFold(n, "RTSS.exe") {
			f.RTSS = true
		}
	}
	f.HWiNFO = hwinfoOpen()
	f.PawnIO = PawnIOVersion()
	f.RAM = ramFact()
	f.PCIe = pcieFact()
	f.MSI = msiFacts()
	f.VCacheSvc = serviceState("amd3dvcacheSvc")
	if f.VCacheSvc == "" {
		f.VCacheSvc = serviceState("amd3dvcache")
	}
	f.BootTimer = bootTimer()
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, kernelKey, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("GlobalTimerResolutionRequests"); err == nil && v == 1 {
			f.GlobalTimer = true
		}
		if b, _, err := k.GetBinaryValue("ReservedCpuSets"); err == nil {
			for _, x := range b {
				if x != 0 {
					f.ReservedCpuSets = true
				}
			}
		}
		k.Close()
	}
	f.FTH = FTHEntries()
	f.HVCI, f.VBS = codeIntegrity()
	return f
}

// ---------------------------------------------------------------- displays

func displays() []DisplayFact {
	var out []DisplayFact
	for i := uint32(0); i < 16; i++ {
		dd := make([]byte, 840)
		binary.LittleEndian.PutUint32(dd, 840)
		if r, _, _ := enumDisp.Call(0, uintptr(i), uintptr(unsafe.Pointer(&dd[0])), 0); r == 0 {
			break
		}
		flags := binary.LittleEndian.Uint32(dd[324:])
		if flags&1 == 0 { // not attached to the desktop
			continue
		}
		name := utf16Bytes(dd[4:68])
		d := DisplayFact{Name: name, Primary: flags&4 != 0}
		// the monitor on this output
		md := make([]byte, 840)
		binary.LittleEndian.PutUint32(md, 840)
		n16, _ := windows.UTF16PtrFromString(name)
		if r, _, _ := enumDisp.Call(uintptr(unsafe.Pointer(n16)), 0, uintptr(unsafe.Pointer(&md[0])), 0); r != 0 {
			d.Monitor = utf16Bytes(md[68:324])
		}
		dm := make([]byte, 220)
		binary.LittleEndian.PutUint16(dm[68:], 220)
		if r, _, _ := enumSet.Call(uintptr(unsafe.Pointer(n16)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&dm[0]))); r != 0 {
			d.Width = int(binary.LittleEndian.Uint32(dm[172:]))
			d.Height = int(binary.LittleEndian.Uint32(dm[176:]))
			d.Hz = int(binary.LittleEndian.Uint32(dm[184:]))
		}
		for m := uint32(0); m < 4096; m++ {
			x := make([]byte, 220)
			binary.LittleEndian.PutUint16(x[68:], 220)
			if r, _, _ := enumSet.Call(uintptr(unsafe.Pointer(n16)), uintptr(m), uintptr(unsafe.Pointer(&x[0]))); r == 0 {
				break
			}
			w, h, hz := int(binary.LittleEndian.Uint32(x[172:])), int(binary.LittleEndian.Uint32(x[176:])), int(binary.LittleEndian.Uint32(x[184:]))
			if w == d.Width && h == d.Height && hz > d.MaxHz {
				d.MaxHz = hz
			}
		}
		out = append(out, d)
	}
	return out
}

func utf16Bytes(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return windows.UTF16ToString(u)
}

// ---------------------------------------------------------------- memory, PCIe, services

func ramFact() RAMFact {
	sig := uintptr(0x52534D42) // 'RSMB'
	n, _, _ := getFw.Call(sig, 0, 0, 0)
	if n == 0 || n > 1<<20 {
		return RAMFact{}
	}
	buf := make([]byte, n)
	if r, _, _ := getFw.Call(sig, 0, uintptr(unsafe.Pointer(&buf[0])), n); r == 0 {
		return RAMFact{}
	}
	return ParseSMBIOSMemory(buf)
}

func pcieFact() PCIeFact {
	n := loadNVML()
	if !n.ok {
		return PCIeFact{}
	}
	dll := windows.NewLazySystemDLL("nvml.dll")
	get := func(name string) int {
		var v uint32
		if r, _, _ := dll.NewProc(name).Call(n.dev, uintptr(unsafe.Pointer(&v))); r != 0 {
			return 0
		}
		return int(v)
	}
	f := PCIeFact{CurGen: get("nvmlDeviceGetCurrPcieLinkGeneration"), CurWidth: get("nvmlDeviceGetCurrPcieLinkWidth"),
		MaxGen: get("nvmlDeviceGetMaxPcieLinkGeneration"), MaxWidth: get("nvmlDeviceGetMaxPcieLinkWidth")}
	f.Known = f.MaxGen > 0 && f.MaxWidth > 0
	return f
}

// PCIeNow: the graphics card's link right now (read during a session, when the card is busy)
func PCIeNow() PCIeFact { return pcieFact() }

func serviceState(name string) string {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return ""
	}
	defer windows.CloseServiceHandle(m)
	n16, _ := windows.UTF16PtrFromString(name)
	s, err := windows.OpenService(m, n16, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return ""
	}
	defer windows.CloseServiceHandle(s)
	var st windows.SERVICE_STATUS
	if windows.QueryServiceStatus(s, &st) != nil {
		return "unknown"
	}
	if st.CurrentState == windows.SERVICE_RUNNING {
		return "running"
	}
	return "stopped"
}

// display adapters and whether they use message-signalled interrupts
func msiFacts() []MSIFact {
	var out []MSIFact
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
			desc, _, _ := ik.GetStringValue("DeviceDesc")
			ik.Close()
			if !strings.EqualFold(cls, "{4d36e968-e325-11ce-bfc1-08002be10318}") {
				continue
			}
			// cards no longer in the PC keep their Enum key; only a present device has a Control subkey
			if ck, err := registry.OpenKey(root, d+`\`+in+`\Control`, registry.QUERY_VALUE); err == nil {
				ck.Close()
			} else {
				continue
			}
			if i := strings.LastIndex(desc, ";"); i >= 0 {
				desc = desc[i+1:]
			}
			f := MSIFact{Device: desc}
			if mk, err := registry.OpenKey(root, d+`\`+in+`\Device Parameters\Interrupt Management\MessageSignaledInterruptProperties`, registry.QUERY_VALUE); err == nil {
				if v, _, err := mk.GetIntegerValue("MSISupported"); err == nil {
					f.MSI, f.Known = v == 1, true
				}
				mk.Close()
			}
			out = append(out, f)
		}
	}
	return out
}

// ---------------------------------------------------------------- timers, FTH, code integrity

var reBcd = regexp.MustCompile(`(?im)^(useplatformclock|useplatformtick|disabledynamictick|tscsyncpolicy)\s+(\S+)`)

func bootTimer() map[string]string {
	out := map[string]string{}
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "bcdedit.exe"), "/enum", "{current}")
	HideWindow(cmd)
	b, err := cmd.Output()
	if err != nil {
		out["error"] = "could not read the boot settings"
		return out
	}
	for _, m := range reBcd.FindAllStringSubmatch(string(b), -1) {
		out[strings.ToLower(m[1])] = strings.ToLower(m[2])
	}
	return out
}

func bcdExe() string { return filepath.Join(os.Getenv("SystemRoot"), "System32", "bcdedit.exe") }

// ClearBootTimer removes the timer options from the current boot entry and returns what was set, for Undo.
func ClearBootTimer() (map[string]string, error) {
	was := bootTimer()
	if was["error"] != "" {
		return nil, errors.New(was["error"])
	}
	done := map[string]string{}
	for k, v := range was {
		cmd := exec.Command(bcdExe(), "/deletevalue", "{current}", k)
		HideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return done, errors.New("bcdedit " + k + ": " + strings.TrimSpace(string(out)))
		}
		done[k] = v
	}
	return done, nil
}

func RestoreBootTimer(was map[string]string) error {
	for k, v := range was {
		if !reBcdName.MatchString(k) {
			continue
		}
		cmd := exec.Command(bcdExe(), "/set", "{current}", k, v)
		HideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return errors.New("bcdedit " + k + ": " + strings.TrimSpace(string(out)))
		}
	}
	return nil
}

var reBcdName = regexp.MustCompile(`^(useplatformclock|useplatformtick|disabledynamictick|tscsyncpolicy)$`)

// ClearReservedCpuSets removes the ReservedCpuSets value and returns it for Undo.
func ClearReservedCpuSets() ([]byte, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, kernelKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue("ReservedCpuSets")
	if err != nil {
		return nil, err
	}
	return b, k.DeleteValue("ReservedCpuSets")
}

func RestoreReservedCpuSets(b []byte) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, kernelKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetBinaryValue("ReservedCpuSets", b)
}

// FTHEntries: Fault Tolerant Heap entries for the game (Windows adds them after crashes)
func FTHEntries() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, fthKey, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadValueNames(-1)
	var out []string
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), "cyberpunk2077.exe") {
			out = append(out, n)
		}
	}
	return out
}

// ClearFTH removes the game's Fault Tolerant Heap entries and returns them (name -> data) for Undo.
func ClearFTH() (map[string][]byte, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, fthKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	saved := map[string][]byte{}
	for _, n := range FTHEntries() {
		b, _, err := k.GetBinaryValue(n)
		if err != nil {
			continue
		}
		if err := k.DeleteValue(n); err != nil {
			return saved, err
		}
		saved[n] = b
	}
	return saved, nil
}

func RestoreFTH(saved map[string][]byte) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, fthKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for n, b := range saved {
		if err := k.SetBinaryValue(n, b); err != nil {
			return err
		}
	}
	return nil
}

func codeIntegrity() (hvci, vbs bool) {
	var ci [2]uint32
	ci[0] = 8
	if windows.NtQuerySystemInformation(103, unsafe.Pointer(&ci[0]), 8, nil) == nil {
		hvci = ci[1]&0x400 != 0 // CODEINTEGRITY_OPTION_HVCI_KMCI_ENABLED
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\DeviceGuard`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("EnableVirtualizationBasedSecurity"); err == nil && v == 1 {
			vbs = true
		}
		k.Close()
	}
	return hvci, vbs || hvci
}

// ---------------------------------------------------------------- PawnIO, HWiNFO

// pawnIODir: where the PawnIO installer put PawnIOLib.dll (its uninstall entry, else Program Files\PawnIO).
func pawnIODir() string {
	for _, view := range []uint32{0, registry.WOW64_64KEY} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\PawnIO`, registry.QUERY_VALUE|view)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("InstallLocation")
		k.Close()
		if err == nil && strings.TrimSpace(v) != "" {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return filepath.Join(os.Getenv("ProgramFiles"), "PawnIO")
}

func PawnIOVersion() string {
	for _, view := range []uint32{0, registry.WOW64_64KEY} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\PawnIO`, registry.QUERY_VALUE|view)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("DisplayVersion")
		k.Close()
		if err == nil && v != "" {
			return v
		}
	}
	return ""
}

func hwinfoOpen() bool {
	n, _ := windows.UTF16PtrFromString(`Global\HWiNFO_SENS_SM2`)
	h, _, _ := procOpenFileMappingW.Call(uintptr(windows.FILE_MAP_READ), 0, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return false
	}
	windows.CloseHandle(windows.Handle(h))
	return true
}

// ---------------------------------------------------------------- files: version, signature, drives

// FileVersion: "1.2.3.4" and the product name from a DLL or exe's version resource
func FileVersion(path string) (string, string) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return "", ""
	}
	buf := make([]byte, size)
	if windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])) != nil {
		return "", ""
	}
	var fi *windows.VS_FIXEDFILEINFO
	var l uint32
	ver := ""
	if windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\`, unsafe.Pointer(&fi), &l) == nil && fi != nil {
		ver = verString(fi.FileVersionMS, fi.FileVersionLS)
	}
	product := ""
	var tr *[2]uint16
	if windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\VarFileInfo\Translation`, unsafe.Pointer(&tr), &l) == nil && tr != nil && l >= 4 {
		for _, key := range []string{"ProductName", "FileDescription"} {
			var p *uint16
			q := `\StringFileInfo\` + hex4(tr[0]) + hex4(tr[1]) + `\` + key
			if windows.VerQueryValue(unsafe.Pointer(&buf[0]), q, unsafe.Pointer(&p), &l) == nil && p != nil && l > 0 {
				product = windows.UTF16PtrToString(p)
				if product != "" {
					break
				}
			}
		}
	}
	return ver, product
}

func hex4(v uint16) string {
	const h = "0123456789abcdef"
	return string([]byte{h[v>>12&15], h[v>>8&15], h[v>>4&15], h[v&15]})
}

// ExeSignature: "ok" (valid Authenticode signature), "changed" (signed, but the file was changed
// after signing: a patched exe), "unsigned", or "" when it can't be told
func ExeSignature(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	file := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: p}
	data := &windows.WinTrustData{Size: uint32(unsafe.Sizeof(windows.WinTrustData{})), UIChoice: windows.WTD_UI_NONE,
		RevocationChecks: windows.WTD_REVOKE_NONE, UnionChoice: windows.WTD_CHOICE_FILE, StateAction: windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(file)}
	r := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	if r == nil {
		return "ok"
	}
	var en windows.Errno
	if errors.As(r, &en) {
		switch uint32(en) {
		case 0x80096010: // TRUST_E_BAD_DIGEST
			return "changed"
		case 0x800B0100: // TRUST_E_NOSIGNATURE
			return "unsigned"
		}
	}
	return ""
}

func DriveInfo(path string) DriveFact {
	root := filepath.VolumeName(path) + `\`
	f := DriveFact{Root: root}
	var free, total, tfree uint64
	if p, err := windows.UTF16PtrFromString(root); err == nil && windows.GetDiskFreeSpaceEx(p, &free, &total, &tfree) == nil {
		f.FreeGB, f.TotalGB = float64(free)/(1<<30), float64(total)/(1<<30)
	}
	dev, _ := windows.UTF16PtrFromString(`\\.\` + strings.TrimSuffix(root, `\`))
	h, err := windows.CreateFile(dev, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return f
	}
	defer windows.CloseHandle(h)
	const ioctlQuery = 0x2D1400
	q := make([]byte, 12) // STORAGE_PROPERTY_QUERY: PropertyId, QueryType, AdditionalParameters
	out := make([]byte, 1024)
	var n uint32
	if windows.DeviceIoControl(h, ioctlQuery, &q[0], uint32(len(q)), &out[0], uint32(len(out)), &n, nil) == nil && n >= 36 {
		f.Bus = BusName(binary.LittleEndian.Uint32(out[28:]))
		if f.Bus == "NVMe" {
			f.SSD, f.Known = true, true
		}
		if off := binary.LittleEndian.Uint32(out[16:]); off > 0 && int(off) < int(n) {
			e := int(off)
			for e < int(n) && out[e] != 0 {
				e++
			}
			f.Model = strings.TrimSpace(string(out[off:e]))
		}
	}
	binary.LittleEndian.PutUint32(q, 7) // StorageDeviceSeekPenaltyProperty
	sp := make([]byte, 12)
	if !f.Known && windows.DeviceIoControl(h, ioctlQuery, &q[0], uint32(len(q)), &sp[0], uint32(len(sp)), &n, nil) == nil && n >= 9 {
		f.SSD, f.Known = sp[8] == 0, true
	}
	return f
}

// UserSettingsPath: the game's settings file (one per Windows user, shared by all installs)
func UserSettingsPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "CD Projekt Red", "Cyberpunk 2077", "UserSettings.json")
}

func FileReadOnly(path string) (bool, bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, false
	}
	a, err := windows.GetFileAttributes(p)
	if err != nil {
		return false, false
	}
	return a&windows.FILE_ATTRIBUTE_READONLY != 0, true
}
