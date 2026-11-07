//go:build windows

package platform

// Experiments: Windows settings people change to make games run better. Each one can be read,
// switched on (what was there before is kept in prev) and restored exactly from prev. Frame Lab runs as administrator, which all of them need.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	gameExe       = "Cyberpunk2077.exe"
	ifeoKey       = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Image File Execution Options\` + gameExe
	perfKey       = ifeoKey + `\PerfOptions`
	gfxDriversKey = `SYSTEM\CurrentControlSet\Control\GraphicsDrivers`
	displayClass  = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	schemeHigh    = "8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c"
	schemeUlt     = "e9a42b02-d5df-448d-aa00-03f14749eb61"
	dwmKey        = `SOFTWARE\Microsoft\Windows\Dwm`
	gameDvrKey    = `Software\Microsoft\Windows\CurrentVersion\GameDVR` // per user (HKCU)
)

// ---------------------------------------------------------------- per-user registry (HKCU)

func hkcuDword(path, name string) (uint32, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	return uint32(v), err == nil
}

func hkcuGet(path, name string) string {
	if v, ok := hkcuDword(path, name); ok {
		return "dword:" + strconv.FormatUint(uint64(v), 10)
	}
	return "absent"
}

func hkcuSetDword(path, name string, v uint32) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(name, v)
}

func hkcuRestore(path, name, was string) error {
	if strings.HasPrefix(was, "dword:") {
		n, _ := strconv.ParseUint(was[6:], 10, 32)
		return hkcuSetDword(path, name, uint32(n))
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- registry helpers

// regGet: "absent", "dword:<n>", "qword:<n>" or "bin:<hex>"
func regGet(path, name string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "absent"
	}
	defer k.Close()
	buf := make([]byte, 256)
	n, typ, err := k.GetValue(name, buf)
	if err != nil {
		return "absent"
	}
	b := buf[:n]
	switch typ {
	case registry.DWORD:
		if n >= 4 {
			return "dword:" + strconv.FormatUint(uint64(uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16|uint32(b[3])<<24), 10)
		}
	case registry.QWORD:
		if n >= 8 {
			var v uint64
			for i := 7; i >= 0; i-- {
				v = v<<8 | uint64(b[i])
			}
			return "qword:" + strconv.FormatUint(v, 10)
		}
	}
	return "bin:" + hex.EncodeToString(b)
}

func regDword(path, name string) (uint32, bool) {
	v := regGet(path, name)
	if !strings.HasPrefix(v, "dword:") {
		return 0, false
	}
	n, _ := strconv.ParseUint(v[6:], 10, 32)
	return uint32(n), true
}

func regSetDword(path, name string, v uint32) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(name, v)
}

// regRestore puts a value back the way regGet saw it.
func regRestore(path, name, was string) error {
	switch {
	case was == "absent" || was == "":
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return nil
		}
		defer k.Close()
		if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	case strings.HasPrefix(was, "dword:"):
		n, _ := strconv.ParseUint(was[6:], 10, 32)
		return regSetDword(path, name, uint32(n))
	case strings.HasPrefix(was, "qword:"):
		n, _ := strconv.ParseUint(was[6:], 10, 64)
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return err
		}
		defer k.Close()
		return k.SetQWordValue(name, n)
	case strings.HasPrefix(was, "bin:"):
		b, err := hex.DecodeString(was[4:])
		if err != nil {
			return err
		}
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return err
		}
		defer k.Close()
		return k.SetBinaryValue(name, b)
	}
	return fmt.Errorf("unknown saved value %q", was)
}

// the NVIDIA display adapter keys (0000, 0001, ...)
func nvidiaKeys() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return nil
	}
	defer k.Close()
	subs, _ := k.ReadSubKeyNames(-1)
	var out []string
	for _, s := range subs {
		if len(s) != 4 {
			continue
		}
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass+`\`+s, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err != nil {
			continue
		}
		prov, _, _ := sk.GetStringValue("ProviderName")
		desc, _, _ := sk.GetStringValue("DriverDesc")
		sk.Close()
		if strings.Contains(strings.ToUpper(prov+desc), "NVIDIA") {
			out = append(out, displayClass+`\`+s)
		}
	}
	return out
}

// ---------------------------------------------------------------- commands

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	HideWindow(cmd)
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

func ps(script string) (string, error) {
	return run("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

var reGUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func activeScheme() (guid, name string) {
	out, err := run("powercfg", "/getactivescheme")
	if err != nil {
		return "", ""
	}
	guid = strings.ToLower(reGUID.FindString(out))
	if i := strings.Index(out, "("); i >= 0 {
		name = strings.TrimSuffix(strings.TrimSpace(out[i+1:]), ")")
	}
	return
}

type defenderPrefs struct {
	Paths []string `json:"paths"`
	Procs []string `json:"procs"`
}

func defender() (*defenderPrefs, error) {
	out, err := ps(`$ErrorActionPreference='Stop'; $p=Get-MpPreference; [PSCustomObject]@{paths=@($p.ExclusionPath | Where-Object {$_}); procs=@($p.ExclusionProcess | Where-Object {$_})} | ConvertTo-Json -Compress`)
	if err != nil {
		return nil, errors.New("Microsoft Defender isn't available here (another antivirus?)")
	}
	var d defenderPrefs
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &d); err != nil {
		return nil, errors.New("could not read Defender's settings")
	}
	return &d, nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimRight(x, `\`), strings.TrimRight(s, `\`)) {
			return true
		}
	}
	return false
}

func cfgState() (string, error) {
	out, err := ps(`$ErrorActionPreference='Stop'; (Get-ProcessMitigation -Name ` + gameExe + `).CFG.Enable`)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(strings.TrimSpace(out)), nil
}

// ---------------------------------------------------------------- read / on / restore

func ExpRead(id, game string) (on bool, detail string, ok bool) {
	if on, detail, ok, handled := exp3Read(id, game); handled {
		return on, detail, ok
	}
	if on, detail, ok, handled := exp2Read(id, game); handled {
		return on, detail, ok
	}
	switch id {
	case "power":
		g, n := activeScheme()
		if g == "" {
			return false, "could not read the power plan", false
		}
		return g == schemeHigh || g == schemeUlt, "active plan: " + n, true
	case "priority":
		c, okc := regDword(perfKey, "CpuPriorityClass")
		i, oki := regDword(perfKey, "IoPriority")
		d := "not set (Windows default: normal)"
		if okc || oki {
			d = fmt.Sprintf("CPU priority class %d, disk priority %d (3 = high)", c, i)
		}
		return okc && c == 3 && oki && i == 3, d, true
	case "pstate":
		keys := nvidiaKeys()
		if len(keys) == 0 {
			return false, "no NVIDIA graphics card found", false
		}
		all := true
		for _, k := range keys {
			if v, ok := regDword(k, "DisableDynamicPstate"); !ok || v != 1 {
				all = false
			}
		}
		if all {
			return true, "GPU clocks held at maximum (DisableDynamicPstate = 1)", true
		}
		return false, "GPU clocks managed by the driver (default)", true
	case "tdrdelay":
		v, okv := regDword(gfxDriversKey, "TdrDelay")
		if !okv {
			return false, "Windows default: 2 seconds", true
		}
		return v >= 10, fmt.Sprintf("GPU timeout %d seconds", v), true
	case "tdroff":
		v, okv := regDword(gfxDriversKey, "TdrLevel")
		if !okv {
			return false, "Windows default: recovers from GPU hangs", true
		}
		return v == 0, fmt.Sprintf("TdrLevel %d (0 = no recovery)", v), true
	case "defender":
		d, err := defender()
		if err != nil {
			return false, err.Error(), false
		}
		p, x := containsFold(d.Paths, game), containsFold(d.Procs, gameExe)
		switch {
		case p && x:
			return true, "game folder and " + gameExe + " excluded from scans", true
		case p || x:
			return false, "only partly excluded", true
		}
		return false, "the game folder is scanned (default)", true
	case "cfg":
		st, err := cfgState()
		if err != nil {
			return false, "could not read the game's exploit protection settings", false
		}
		if st == "OFF" {
			return true, "Control Flow Guard off for " + gameExe, true
		}
		return false, "Control Flow Guard: " + strings.ToLower(st) + " (system default)", true
	case "mpo":
		if v, ok := regDword(dwmKey, "OverlayTestMode"); ok && v == 5 {
			return true, "multiplane overlay off (OverlayTestMode = 5)", true
		}
		return false, "multiplane overlay on (Windows default)", true
	case "gamebar":
		v, ok := hkcuDword(gameDvrKey, "HistoricalCaptureEnabled")
		if ok && v == 1 {
			return false, "Record what happened is on: Windows keeps recording in the background", true
		}
		if !ok {
			return true, "Record what happened is off (Windows default)", true
		}
		return true, "Record what happened is off", true
	case "hags":
		v, ok := regDword(gfxDriversKey, "HwSchMode")
		switch {
		case ok && v == 2:
			return true, "on", true
		case ok && v == 1:
			return false, "off", true
		}
		return false, "not set: Windows decides (usually off)", true
	case "pcores":
		c := CPU()
		if !c.Hybrid {
			return false, fmt.Sprintf("not on this PC: the %s has %d cores of one kind", c.Name, c.Cores), false
		}
		return false, fmt.Sprintf("%d performance and %d efficiency cores", c.PCores, c.ECores), true
	case "standby", "shadercache", "standbyauto":
		return false, "", true
	}
	return false, "unknown", false
}

func ExpOn(id, game string) (map[string]string, error) {
	if p, handled, err := exp3On(id, game); handled {
		return p, err
	}
	if p, handled, err := exp2On(id, game); handled {
		return p, err
	}
	prev := map[string]string{}
	switch id {
	case "power":
		g, _ := activeScheme()
		if g == "" {
			return nil, errors.New("could not read the power plan")
		}
		prev["scheme"] = g
		if _, err := run("powercfg", "/setactive", schemeHigh); err != nil {
			// some PCs hide the High performance plan: make a copy of it and use that
			out, err2 := run("powercfg", "-duplicatescheme", schemeHigh)
			ng := strings.ToLower(reGUID.FindString(out))
			if err2 != nil || ng == "" {
				return nil, errors.New("could not switch to the High performance plan")
			}
			if _, err := run("powercfg", "/setactive", ng); err != nil {
				return nil, err
			}
		}
	case "priority":
		prev["cpu"], prev["io"] = regGet(perfKey, "CpuPriorityClass"), regGet(perfKey, "IoPriority")
		if err := regSetDword(perfKey, "CpuPriorityClass", 3); err != nil {
			return nil, err
		}
		if err := regSetDword(perfKey, "IoPriority", 3); err != nil {
			return nil, err
		}
	case "pstate":
		keys := nvidiaKeys()
		if len(keys) == 0 {
			return nil, errors.New("no NVIDIA graphics card found")
		}
		for i, k := range keys {
			prev[fmt.Sprintf("key%d", i)] = k
			prev[fmt.Sprintf("val%d", i)] = regGet(k, "DisableDynamicPstate")
			if err := regSetDword(k, "DisableDynamicPstate", 1); err != nil {
				return nil, err
			}
		}
	case "tdrdelay":
		prev["delay"], prev["ddi"] = regGet(gfxDriversKey, "TdrDelay"), regGet(gfxDriversKey, "TdrDdiDelay")
		if err := regSetDword(gfxDriversKey, "TdrDelay", 20); err != nil {
			return nil, err
		}
		if err := regSetDword(gfxDriversKey, "TdrDdiDelay", 20); err != nil {
			return nil, err
		}
	case "tdroff":
		prev["level"] = regGet(gfxDriversKey, "TdrLevel")
		if err := regSetDword(gfxDriversKey, "TdrLevel", 0); err != nil {
			return nil, err
		}
	case "defender":
		d, err := defender()
		if err != nil {
			return nil, err
		}
		prev["path"], prev["proc"] = strconv.FormatBool(containsFold(d.Paths, game)), strconv.FormatBool(containsFold(d.Procs, gameExe))
		if _, err := ps(`$ErrorActionPreference='Stop'; Add-MpPreference -ExclusionPath ` + psQuote(game) + ` -ExclusionProcess ` + psQuote(gameExe)); err != nil {
			return nil, errors.New("Defender refused the exclusion (Tamper Protection or a policy may block it)")
		}
	case "cfg":
		prev["mit"], prev["audit"] = regGet(ifeoKey, "MitigationOptions"), regGet(ifeoKey, "MitigationAuditOptions")
		if _, err := ps(`$ErrorActionPreference='Stop'; Set-ProcessMitigation -Name ` + gameExe + ` -Disable CFG`); err != nil {
			return nil, errors.New("could not change the game's exploit protection settings")
		}
	case "mpo":
		prev["mode"] = regGet(dwmKey, "OverlayTestMode")
		if err := regSetDword(dwmKey, "OverlayTestMode", 5); err != nil {
			return nil, err
		}
	case "gamebar":
		prev["hist"] = hkcuGet(gameDvrKey, "HistoricalCaptureEnabled")
		if err := hkcuSetDword(gameDvrKey, "HistoricalCaptureEnabled", 0); err != nil {
			return nil, err
		}
	case "pcores":
		if !CPU().Hybrid {
			return nil, errors.New("this CPU has one kind of core")
		}
		// nothing changes in Windows: Frame Lab pins the game when it sees it start
	case "hags":
		return nil, errors.New("change it in Windows Settings > System > Display > Graphics, then restart")
	default:
		return nil, errors.New("not a setting that can be switched on")
	}
	return prev, nil
}

func ExpRestore(id, game string, prev map[string]string) error {
	if handled, err := exp3Restore(id, game, prev); handled {
		return err
	}
	if handled, err := exp2Restore(id, game, prev); handled {
		return err
	}
	switch id {
	case "power":
		if g := prev["scheme"]; g != "" {
			_, err := run("powercfg", "/setactive", g)
			return err
		}
	case "priority":
		if err := regRestore(perfKey, "CpuPriorityClass", prev["cpu"]); err != nil {
			return err
		}
		return regRestore(perfKey, "IoPriority", prev["io"])
	case "pstate":
		if _, ok := prev["key0"]; !ok { // switched on elsewhere: back to the driver default
			for _, k := range nvidiaKeys() {
				if err := regRestore(k, "DisableDynamicPstate", "absent"); err != nil {
					return err
				}
			}
			return nil
		}
		for i := 0; ; i++ {
			k, ok := prev[fmt.Sprintf("key%d", i)]
			if !ok {
				break
			}
			if err := regRestore(k, "DisableDynamicPstate", prev[fmt.Sprintf("val%d", i)]); err != nil {
				return err
			}
		}
	case "tdrdelay":
		if err := regRestore(gfxDriversKey, "TdrDelay", prev["delay"]); err != nil {
			return err
		}
		return regRestore(gfxDriversKey, "TdrDdiDelay", prev["ddi"])
	case "tdroff":
		return regRestore(gfxDriversKey, "TdrLevel", prev["level"])
	case "defender":
		var parts []string
		if prev["path"] != "true" {
			parts = append(parts, `-ExclusionPath `+psQuote(game))
		}
		if prev["proc"] != "true" {
			parts = append(parts, `-ExclusionProcess `+psQuote(gameExe))
		}
		if len(parts) > 0 {
			_, err := ps(`$ErrorActionPreference='Stop'; Remove-MpPreference ` + strings.Join(parts, " "))
			return err
		}
	case "cfg":
		if err := regRestore(ifeoKey, "MitigationOptions", prev["mit"]); err != nil {
			return err
		}
		return regRestore(ifeoKey, "MitigationAuditOptions", prev["audit"])
	case "mpo":
		return regRestore(dwmKey, "OverlayTestMode", prev["mode"])
	case "gamebar":
		return hkcuRestore(gameDvrKey, "HistoricalCaptureEnabled", prev["hist"])
	case "pcores":
		UnpinGame()
	}
	return nil
}

// ---------------------------------------------------------------- one-shot actions

var (
	ntdll                      = windows.NewLazySystemDLL("ntdll.dll")
	procNtSetSystemInformation = ntdll.NewProc("NtSetSystemInformation")
)

func enablePrivilege(name string) error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return err
	}
	defer tok.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	return windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
}

// PurgeStandby empties Windows' standby list (cached file data kept in RAM), like ISLC / RAMMap.
func PurgeStandby() error {
	if err := enablePrivilege("SeProfileSingleProcessPrivilege"); err != nil {
		return err
	}
	const systemMemoryListInformation = 80
	cmd := uint32(4) // MemoryPurgeStandbyList
	r, _, _ := procNtSetSystemInformation.Call(systemMemoryListInformation, uintptr(unsafe.Pointer(&cmd)), 4)
	if r != 0 {
		return fmt.Errorf("Windows refused (NTSTATUS 0x%08x)", uint32(r))
	}
	return nil
}

func shaderCacheDirs() []string {
	la, up := os.Getenv("LOCALAPPDATA"), os.Getenv("USERPROFILE")
	return []string{
		filepath.Join(la, "NVIDIA", "DXCache"),
		filepath.Join(la, "NVIDIA", "GLCache"),
		filepath.Join(up, "AppData", "LocalLow", "NVIDIA", "PerDriverVersion", "DXCache"),
		filepath.Join(la, "D3DSCache"),
		filepath.Join(la, "AMD", "DxCache"),
		filepath.Join(la, "AMD", "DxcCache"), // DirectX 12 (newer AMD drivers)
		filepath.Join(up, "AppData", "LocalLow", "Intel", "ShaderCache"),
	}
}

func ExpRun(id, game string) (string, error) {
	if msg, handled, err := exp2Run(id); handled {
		return msg, err
	}
	switch id {
	case "standby":
		if err := PurgeStandby(); err != nil {
			return "", err
		}
		return "Standby memory cleared.", nil
	case "shadercache":
		var files, locked int
		var bytes int64
		for _, d := range shaderCacheDirs() {
			filepath.Walk(d, func(p string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return nil
				}
				if os.Remove(p) == nil {
					files++
					bytes += fi.Size()
				} else {
					locked++
				}
				return nil
			})
		}
		msg := fmt.Sprintf("Deleted %d shader cache files (%.0f MB).", files, float64(bytes)/(1<<20))
		if locked > 0 {
			msg += fmt.Sprintf(" %d were in use and stayed (close the game and other 3D apps first).", locked)
		}
		return msg, nil
	}
	return "", errors.New("not an action")
}
