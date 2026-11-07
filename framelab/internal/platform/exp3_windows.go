//go:build windows

package platform

// Frame Lab 2.0 Experiments, part 2: NVIDIA driver profile switches for the game (power
// management, DLSS latest model), the DLSS on-screen indicator, a fixed page file on the fastest
// drive, the graphics card's interrupt CPU, and newer DLSS files the player put in Frame Lab's
// "DLSS files" folder (exact backup, put back on undo).
// Driver settings use NVIDIA's own nvapi64.dll (IDs from NVIDIA's NVAPI headers, MIT). The shader
// cache settings are never touched.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	nvDrsSetSetting                   = 0x577dd202
	nvDrsSaveSettings                 = 0xfcbc7e14
	nvDrsDeleteProfileSetting         = 0xe4a26362
	nvDrsRestoreProfileDefault        = 0x53f0381e
	nvSettingPredefinedU32            = 4120 // u32PredefinedValue
	nvSettingIsCurrentPredef          = 4112
	nvSettingIDOff                    = 4100
	nvSettingTypeOff                  = 4104
	nvPstate                   uint32 = 0x1057EB71 // PREFERRED_PSTATE: 1 = prefer maximum performance
	nvSrOverride               uint32 = 0x10E41E01 // NGX_DLSS_SR_OVERRIDE: 1 = on
	nvSrPreset                 uint32 = 0x10E41DF3 // NGX_DLSS_SR_OVERRIDE_RENDER_PRESET_SELECTION
	nvRrOverride               uint32 = 0x10E41E02 // NGX_DLSS_RR_OVERRIDE
	nvRrPreset                 uint32 = 0x10E41DF7 // NGX_DLSS_RR_OVERRIDE_RENDER_PRESET_SELECTION
	nvPresetLatest             uint32 = 0x00ffffff
	ngxKey                            = `SOFTWARE\NVIDIA Corporation\Global\NGXCore`
	mmKey                             = `SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management`
	affSub                            = `\Device Parameters\Interrupt Management\Affinity Policy`
)

// ---------------------------------------------------------------- NVIDIA driver profile

type nvDrs struct {
	n    nvapi
	sess uintptr
	prof uintptr
}

func nvOpen() (*nvDrs, error) {
	dll := windows.NewLazySystemDLL("nvapi64.dll")
	if err := dll.Load(); err != nil {
		return nil, errors.New("no NVIDIA driver (nvapi64.dll) on this PC")
	}
	n := nvapi{qi: dll.NewProc("nvapi_QueryInterface")}
	if err := n.qi.Find(); err != nil {
		return nil, errors.New("the NVIDIA driver's settings API is missing")
	}
	if r := n.call(nvInitialize); r != 0 {
		return nil, fmt.Errorf("the NVIDIA driver didn't start its settings API (%d)", r)
	}
	d := &nvDrs{n: n}
	if r := n.call(nvDrsCreateSession, uintptr(unsafe.Pointer(&d.sess))); r != 0 || d.sess == 0 {
		return nil, fmt.Errorf("could not open the driver settings (%d)", r)
	}
	if r := n.call(nvDrsLoadSettings, d.sess); r != 0 {
		d.close()
		return nil, fmt.Errorf("could not load the driver settings (%d)", r)
	}
	var name [2048]uint16
	copy(name[:], windows.StringToUTF16("cyberpunk2077.exe"))
	app := make([]byte, nvApplicationSize)
	binary.LittleEndian.PutUint32(app, nvApplicationSize|4<<16)
	n.call(nvDrsFindAppByName, d.sess, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&d.prof)), uintptr(unsafe.Pointer(&app[0])))
	if d.prof == 0 {
		d.close()
		return nil, errors.New("the driver has no Cyberpunk 2077 profile (update the NVIDIA driver)")
	}
	return d, nil
}

func (d *nvDrs) close() { d.n.call(nvDrsDestroySession, d.sess) }

// get: the value in the game profile, whether it is set there (not inherited), and whether that
// value is NVIDIA's own predefined one
func (d *nvDrs) get(id uint32) (val uint32, inProfile, predefined, ok bool) {
	buf := make([]byte, nvSettingSize)
	binary.LittleEndian.PutUint32(buf, nvSettingSize|1<<16)
	if r := d.n.call(nvDrsGetSetting, d.sess, d.prof, uintptr(id), uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
		return 0, false, false, r == nvSettingNotFound
	}
	le := binary.LittleEndian
	return le.Uint32(buf[nvSettingCurrentU32:]), le.Uint32(buf[nvSettingLocation:]) == 0, le.Uint32(buf[nvSettingIsCurrentPredef:]) != 0, true
}

func (d *nvDrs) set(id, v uint32) error {
	buf := make([]byte, nvSettingSize)
	le := binary.LittleEndian
	le.PutUint32(buf, nvSettingSize|1<<16)
	le.PutUint32(buf[nvSettingIDOff:], id)
	le.PutUint32(buf[nvSettingTypeOff:], 0) // NVDRS_DWORD_TYPE
	le.PutUint32(buf[nvSettingCurrentU32:], v)
	if r := d.n.call(nvDrsSetSetting, d.sess, d.prof, uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
		return fmt.Errorf("the driver refused the setting (%d)", r)
	}
	return nil
}

func (d *nvDrs) save() error {
	if r := d.n.call(nvDrsSaveSettings, d.sess); r != 0 {
		return fmt.Errorf("the driver didn't save its settings (%d)", r)
	}
	return nil
}

// remember: "unset" (inherited), "predef:<v>" (NVIDIA's value in the profile) or "user:<v>"
func (d *nvDrs) remember(id uint32) string {
	v, in, pre, _ := d.get(id)
	switch {
	case !in:
		return "unset"
	case pre:
		return "predef:" + strconv.FormatUint(uint64(v), 10)
	}
	return "user:" + strconv.FormatUint(uint64(v), 10)
}

func (d *nvDrs) putBack(id uint32, was string) error {
	switch {
	case was == "" || was == "unset":
		if r := d.n.call(nvDrsDeleteProfileSetting, d.sess, d.prof, uintptr(id)); r != 0 && r != nvSettingNotFound {
			return fmt.Errorf("the driver didn't remove the setting (%d)", r)
		}
		return nil
	case strings.HasPrefix(was, "predef:"):
		if r := d.n.call(nvDrsRestoreProfileDefault, d.sess, d.prof, uintptr(id)); r != 0 {
			return fmt.Errorf("the driver didn't restore NVIDIA's value (%d)", r)
		}
		return nil
	case strings.HasPrefix(was, "user:"):
		v, err := strconv.ParseUint(was[5:], 10, 32)
		if err != nil {
			return err
		}
		return d.set(id, uint32(v))
	}
	return fmt.Errorf("unknown saved driver value %q", was)
}

var nvExpIDs = map[string][]uint32{
	"nvperf": {nvPstate},
	"nvdlss": {nvSrOverride, nvSrPreset, nvRrOverride, nvRrPreset},
}

func nvExpRead(id string) (bool, string, bool) {
	d, err := nvOpen()
	if err != nil {
		return false, err.Error(), false
	}
	defer d.close()
	switch id {
	case "nvperf":
		v, in, _, _ := d.get(nvPstate)
		names := map[uint32]string{0: "Adaptive", 1: "Prefer maximum performance", 2: "Driver controlled", 3: "Prefer consistent performance", 4: "Prefer minimum", 5: "Normal"}
		where := "game profile"
		if !in {
			where = "global / driver default"
		}
		n, ok := names[v]
		if !ok {
			n = fmt.Sprintf("0x%X", v)
		}
		return v == 1, "Power management mode: " + n + " (" + where + ")", true
	case "nvdlss":
		so, _, _, _ := d.get(nvSrOverride)
		sp, _, _, _ := d.get(nvSrPreset)
		ro, _, _, _ := d.get(nvRrOverride)
		rp, _, _, _ := d.get(nvRrPreset)
		desc := func(o, p uint32) string {
			switch {
			case o != 1 || p == 0:
				return "the game's own"
			case p == nvPresetLatest:
				return "latest"
			case p >= 1 && p <= 26:
				return "preset " + string(rune('A'+p-1))
			}
			return fmt.Sprintf("0x%X", p)
		}
		on := so == 1 && sp == nvPresetLatest && ro == 1 && rp == nvPresetLatest
		return on, "Super Resolution: " + desc(so, sp) + " · Ray Reconstruction: " + desc(ro, rp), true
	}
	return false, "", false
}

func nvExpOn(id string) (map[string]string, error) {
	d, err := nvOpen()
	if err != nil {
		return nil, err
	}
	defer d.close()
	prev := map[string]string{}
	for _, s := range nvExpIDs[id] {
		prev[fmt.Sprintf("%08X", s)] = d.remember(s)
	}
	want := map[uint32]uint32{nvPstate: 1, nvSrOverride: 1, nvSrPreset: nvPresetLatest, nvRrOverride: 1, nvRrPreset: nvPresetLatest}
	for _, s := range nvExpIDs[id] {
		if err := d.set(s, want[s]); err != nil {
			return nil, err
		}
	}
	return prev, d.save()
}

func nvExpRestore(id string, prev map[string]string) error {
	d, err := nvOpen()
	if err != nil {
		return err
	}
	defer d.close()
	for _, s := range nvExpIDs[id] {
		if err := d.putBack(s, prev[fmt.Sprintf("%08X", s)]); err != nil {
			return err
		}
	}
	return d.save()
}

// ---------------------------------------------------------------- multi-string registry values

func regGetMulti(path, name string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "absent"
	}
	defer k.Close()
	v, _, err := k.GetStringsValue(name)
	if err != nil {
		return "absent"
	}
	b, _ := json.Marshal(v)
	return "multi:" + string(b)
}

func regSetMulti(path, name string, v []string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringsValue(name, v)
}

func regRestoreMulti(path, name, was string) error {
	if !strings.HasPrefix(was, "multi:") {
		return regRestore(path, name, was)
	}
	var v []string
	if err := json.Unmarshal([]byte(was[6:]), &v); err != nil {
		return err
	}
	return regSetMulti(path, name, v)
}

// ---------------------------------------------------------------- page file

var procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")

func ramMB() uint64 {
	var m [64]byte
	binary.LittleEndian.PutUint32(m[:], 64)
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m[0]))); r == 0 {
		return 0
	}
	return binary.LittleEndian.Uint64(m[8:]) >> 20 // ullTotalPhys
}

// pageTarget: the fastest fixed drive with room (NVMe, then other SSD, then the system drive),
// and the size: the PC's RAM, at least 16 GB and at most 32 GB
func pageTarget() (string, uint64, error) {
	size := ramMB()
	if size < 16384 {
		size = 16384
	}
	if size > 32768 {
		size = 32768
	}
	type cand struct {
		root string
		rank int
		free float64
	}
	var cs []cand
	mask, _ := windows.GetLogicalDrives()
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) != windows.DRIVE_FIXED {
			continue
		}
		f := DriveInfo(root)
		if f.FreeGB*1024 < float64(size)+10*1024 {
			continue
		}
		rank := 0
		switch {
		case f.Bus == "NVMe":
			rank = 3
		case f.SSD:
			rank = 2
		}
		if strings.EqualFold(root, os.Getenv("SystemDrive")+`\`) {
			rank++ // same speed class: the system drive wins
		}
		cs = append(cs, cand{root, rank, f.FreeGB})
	}
	if len(cs) == 0 {
		return "", 0, fmt.Errorf("no drive has %d GB free for a page file", size/1024+10)
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].rank != cs[j].rank {
			return cs[i].rank > cs[j].rank
		}
		return cs[i].free > cs[j].free
	})
	return cs[0].root[:2], size, nil
}

func pageRead() (bool, string, bool) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, mmKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return false, "could not read the page file setting", false
	}
	defer k.Close()
	v, _, err := k.GetStringsValue("PagingFiles")
	if err != nil || len(v) == 0 {
		return false, "no page file", true
	}
	if len(v) == 1 && strings.HasPrefix(v[0], "?:") {
		return false, "page file managed by Windows (grows and shrinks)", true
	}
	var parts []string
	fixed := len(v) == 1
	for _, e := range v {
		f := strings.Fields(e)
		if len(f) == 3 && f[1] == f[2] && f[1] != "0" {
			mb, _ := strconv.Atoi(f[1])
			parts = append(parts, fmt.Sprintf("%s fixed %d GB", strings.TrimSuffix(f[0], `\pagefile.sys`), mb/1024))
		} else {
			fixed = false
			parts = append(parts, strings.TrimSuffix(f[0], `\pagefile.sys`)+" system managed")
		}
	}
	return fixed, "page file: " + strings.Join(parts, ", "), true
}

// ---------------------------------------------------------------- the graphics card's interrupt CPU

// irqCPU: the first thread of the second (fastest kind of) core, so the card's interrupts don't
// land on CPU 0, where Windows handles most of its own work
func irqCPU() (int, error) {
	var need uint32
	procGetSystemCpuSetInformation.Call(0, 0, uintptr(unsafe.Pointer(&need)), 0, 0)
	if need == 0 || need > 1<<20 {
		return 0, errors.New("could not read the CPU layout")
	}
	buf := make([]byte, need)
	if r, _, _ := procGetSystemCpuSetInformation.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(need), uintptr(unsafe.Pointer(&need)), 0, 0); r == 0 {
		return 0, errors.New("could not read the CPU layout")
	}
	type lp struct{ lp, core, eff int }
	var all []lp
	maxEff := 0
	le := binary.LittleEndian
	for off := 0; off+20 <= int(need); {
		size := int(le.Uint32(buf[off:]))
		if size < 20 || off+size > int(need) {
			break
		}
		// SYSTEM_CPU_SET_INFORMATION: Group u16 @12, LogicalProcessorIndex @14, CoreIndex @15, EfficiencyClass @18
		if le.Uint32(buf[off+4:]) == 0 && le.Uint16(buf[off+12:]) == 0 {
			e := int(buf[off+18])
			all = append(all, lp{int(buf[off+14]), int(buf[off+15]), e})
			if e > maxEff {
				maxEff = e
			}
		}
		off += size
	}
	cores := map[int]int{} // core index -> lowest logical processor
	var order []int
	for _, p := range all {
		if p.eff != maxEff {
			continue
		}
		if cur, ok := cores[p.core]; !ok || p.lp < cur {
			if !ok {
				order = append(order, p.core)
			}
			cores[p.core] = p.lp
		}
	}
	if len(order) < 2 {
		return 0, errors.New("needs a CPU with at least two cores")
	}
	sort.Slice(order, func(i, j int) bool { return cores[order[i]] < cores[order[j]] })
	return cores[order[1]], nil
}

func irqRead() (bool, string, bool) {
	gs := gpuInstances()
	if len(gs) == 0 {
		return false, "no graphics card found", false
	}
	pol, has := regDword(gs[0]+affSub, "DevicePolicy")
	if !has || pol != 4 {
		return false, "Windows chooses the CPU for the card's interrupts", true
	}
	m := regGet(gs[0]+affSub, "AssignmentSetOverride")
	if strings.HasPrefix(m, "bin:") {
		if b, err := hex.DecodeString(m[4:]); err == nil {
			var mask uint64
			for i := len(b) - 1; i >= 0; i-- {
				mask = mask<<8 | uint64(b[i])
			}
			for c := 0; c < 64; c++ {
				if mask&(1<<c) != 0 {
					return true, fmt.Sprintf("the card's interrupts go to CPU %d", c), true
				}
			}
		}
	}
	return true, "the card's interrupts go to a fixed CPU", true
}

func irqOn() (map[string]string, error) {
	gs := gpuInstances()
	if len(gs) == 0 {
		return nil, errors.New("no graphics card found")
	}
	cpu, err := irqCPU()
	if err != nil {
		return nil, err
	}
	prev := map[string]string{"cpu": strconv.Itoa(cpu)}
	mask := make([]byte, 8)
	binary.LittleEndian.PutUint64(mask, 1<<uint(cpu))
	for i, g := range gs {
		p := g + affSub
		prev[fmt.Sprintf("key%d", i)] = g
		prev[fmt.Sprintf("pol%d", i)] = regGet(p, "DevicePolicy")
		prev[fmt.Sprintf("set%d", i)] = regGet(p, "AssignmentSetOverride")
		if err := regSetDword(p, "DevicePolicy", 4); err != nil { // IrqPolicySpecifiedProcessors
			return nil, err
		}
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, p, registry.SET_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return nil, err
		}
		err = k.SetBinaryValue("AssignmentSetOverride", mask)
		k.Close()
		if err != nil {
			return nil, err
		}
	}
	return prev, nil
}

func irqRestore(prev map[string]string) error {
	if _, ok := prev["key0"]; !ok {
		return errors.New("set outside Frame Lab: Frame Lab doesn't know the old setting, so it leaves it")
	}
	for i := 0; ; i++ {
		g, ok := prev[fmt.Sprintf("key%d", i)]
		if !ok {
			return nil
		}
		if err := regRestore(g+affSub, "DevicePolicy", prev[fmt.Sprintf("pol%d", i)]); err != nil {
			return err
		}
		if err := regRestore(g+affSub, "AssignmentSetOverride", prev[fmt.Sprintf("set%d", i)]); err != nil {
			return err
		}
	}
}

// ---------------------------------------------------------------- newer DLSS files

var dlssNames = []string{"nvngx_dlss.dll", "nvngx_dlssd.dll", "nvngx_dlssg.dll"}

// DlssFolder: where the player puts newer DLSS files for Frame Lab to use (next to the exe)
func DlssFolder() string {
	exe, err := os.Executable()
	if err != nil {
		return "DLSS files"
	}
	return filepath.Join(filepath.Dir(exe), "DLSS files")
}

func verNewer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 4; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func fileHash(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".framelab-tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// dlssOffer: per file, the newer signed NVIDIA DLL in the DLSS files folder that would replace the
// game's (name -> source path), and a line describing the state
func dlssOffer(game string) (map[string]string, string) {
	bin := filepath.Join(game, "bin", "x64")
	offer := map[string]string{}
	var parts []string
	for _, n := range dlssNames {
		gv, _ := FileVersion(filepath.Join(bin, n))
		if gv == "" {
			continue
		}
		src := filepath.Join(DlssFolder(), n)
		nv, prod := FileVersion(src)
		switch {
		case nv == "":
			parts = append(parts, n+" "+gv)
		case !strings.Contains(strings.ToUpper(prod), "NVIDIA") && !strings.Contains(strings.ToUpper(prod), "DLSS"):
			parts = append(parts, n+": the file in DLSS files isn't an NVIDIA DLSS file")
		case ExeSignature(src) != "ok":
			parts = append(parts, n+": the file in DLSS files has no valid NVIDIA signature")
		case !verNewer(nv, gv):
			parts = append(parts, n+" "+gv+" (DLSS files has "+nv+", not newer)")
		default:
			offer[n] = src
			parts = append(parts, n+" "+gv+" → "+nv)
		}
	}
	if len(parts) == 0 {
		return offer, "no DLSS files in the game's bin\\x64"
	}
	return offer, strings.Join(parts, " · ")
}

func dlssOn(game string) (map[string]string, error) {
	if len(FindGame()) > 0 {
		return nil, errors.New("close the game first")
	}
	offer, line := dlssOffer(game)
	if len(offer) == 0 {
		return nil, errors.New("nothing newer to use: put newer nvngx_dlss*.dll files in " + DlssFolder() + " (" + line + ")")
	}
	bdir := filepath.Join(dataDir, "dlss backup", fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(game))))[:12])
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return nil, err
	}
	prev := map[string]string{"dir": bdir}
	names := make([]string, 0, len(offer))
	for n := range offer {
		names = append(names, n)
	}
	sort.Strings(names)
	bin := filepath.Join(game, "bin", "x64")
	undo := func(err error) (map[string]string, error) {
		// a file failed half way: put back the ones already replaced, so nothing is left changed
		if e := dlssRestore(game, prev); e != nil {
			return nil, errors.New(err.Error() + "; putting the others back failed too: " + e.Error())
		}
		return nil, err
	}
	for _, n := range names {
		dst := filepath.Join(bin, n)
		oh, err := fileHash(dst)
		if err != nil {
			return undo(err)
		}
		if err := copyFile(dst, filepath.Join(bdir, n)); err != nil {
			return undo(err)
		}
		if bh, _ := fileHash(filepath.Join(bdir, n)); bh != oh {
			return undo(errors.New("the backup of " + n + " didn't match the original"))
		}
		prev["orig:"+n] = oh
		if err := copyFile(offer[n], dst); err != nil {
			prev["new:"+n] = "?"
			return undo(err)
		}
		prev["new:"+n], _ = fileHash(dst)
	}
	return prev, nil
}

func dlssRestore(game string, prev map[string]string) error {
	if len(FindGame()) > 0 {
		return errors.New("close the game first")
	}
	bin := filepath.Join(game, "bin", "x64")
	var kept []string
	for k, oh := range prev {
		if !strings.HasPrefix(k, "orig:") {
			continue
		}
		n := k[5:]
		dst := filepath.Join(bin, n)
		cur, _ := fileHash(dst)
		if cur == oh {
			continue // already the original
		}
		if cur != prev["new:"+n] && prev["new:"+n] != "?" {
			kept = append(kept, n) // the game or another tool replaced it since: leave that file alone
			continue
		}
		bk := filepath.Join(prev["dir"], n)
		if bh, _ := fileHash(bk); bh != oh {
			return errors.New("the backup of " + n + " is missing or changed: the game's file is left as it is")
		}
		if err := copyFile(bk, dst); err != nil {
			return err
		}
	}
	if len(kept) > 0 {
		return errors.New("changed since Frame Lab put it there (a game update?), left as it is: " + strings.Join(kept, ", "))
	}
	return nil
}

// ---------------------------------------------------------------- read / on / restore

func exp3Read(id, game string) (on bool, detail string, ok bool, handled bool) {
	switch id {
	case "nvperf", "nvdlss":
		if GPU().Vendor != "nvidia" {
			return false, "no NVIDIA graphics card found", false, true
		}
		on, detail, ok = nvExpRead(id)
		return on, detail, ok, true
	case "dlssind":
		if GPU().Vendor != "nvidia" {
			return false, "no NVIDIA graphics card found", false, true
		}
		v, has := regDword(ngxKey, "ShowDlssIndicator")
		if has && v == 0x400 {
			return true, "the DLSS indicator shows in the corner while DLSS runs", true, true
		}
		return false, "indicator off", true, true
	case "pagefile":
		on, detail, ok = pageRead()
		if t, size, err := pageTarget(); err == nil && !on {
			detail += fmt.Sprintf(" · Frame Lab would use %s, %d GB", t, size/1024)
		}
		return on, detail, ok, true
	case "gpuirq":
		on, detail, ok = irqRead()
		if c, err := irqCPU(); err == nil && !on {
			detail += fmt.Sprintf(" · Frame Lab would use CPU %d", c)
		}
		return on, detail, ok, true
	case "dlssdll":
		if game == "" {
			return false, "no game folder set", false, true
		}
		os.MkdirAll(DlssFolder(), 0o755) // so the player can find where the files go
		offer, line := dlssOffer(game)
		if len(offer) == 0 {
			line += " · put newer NVIDIA DLSS files in " + DlssFolder() + " to use this"
		}
		return false, line, len(offer) > 0, true
	}
	return false, "", false, false
}

func exp3On(id, game string) (map[string]string, bool, error) {
	switch id {
	case "nvperf", "nvdlss":
		p, err := nvExpOn(id)
		return p, true, err
	case "dlssind":
		prev := map[string]string{"v": regGet(ngxKey, "ShowDlssIndicator")}
		return prev, true, regSetDword(ngxKey, "ShowDlssIndicator", 0x400)
	case "pagefile":
		drive, size, err := pageTarget()
		if err != nil {
			return nil, true, err
		}
		prev := map[string]string{"v": regGetMulti(mmKey, "PagingFiles")}
		if prev["v"] == "absent" {
			return nil, true, errors.New("could not read the page file setting")
		}
		return prev, true, regSetMulti(mmKey, "PagingFiles", []string{fmt.Sprintf(`%s\pagefile.sys %d %d`, drive, size, size)})
	case "gpuirq":
		p, err := irqOn()
		return p, true, err
	case "dlssdll":
		p, err := dlssOn(game)
		return p, true, err
	}
	return nil, false, nil
}

func exp3Restore(id, game string, prev map[string]string) (bool, error) {
	switch id {
	case "nvperf", "nvdlss":
		return true, nvExpRestore(id, prev) // not set by Frame Lab: the game profile goes back to the driver's own values
	case "dlssind":
		was := prev["v"]
		if was == "" {
			was = "absent"
		}
		return true, regRestore(ngxKey, "ShowDlssIndicator", was)
	case "pagefile":
		if !strings.HasPrefix(prev["v"], "multi:") {
			return true, regSetMulti(mmKey, "PagingFiles", []string{`?:\pagefile.sys`}) // Windows manages it (the default)
		}
		return true, regRestoreMulti(mmKey, "PagingFiles", prev["v"])
	case "gpuirq":
		return true, irqRestore(prev)
	case "dlssdll":
		if prev["dir"] == "" {
			return true, errors.New("Frame Lab didn't replace these files, so it has no backup of them")
		}
		return true, dlssRestore(game, prev)
	}
	return false, nil
}
