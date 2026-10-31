//go:build windows

package platform

// Programs on the graphics card: the graphics adapters Windows has (DXGI), what every process
// holds on each of them right now (Windows' "GPU Process Memory" counters), and the per-program
// GPU choice Windows keeps in HKCU\Software\Microsoft\DirectX\UserGpuPreferences: the same
// value Settings > System > Display > Graphics writes (value name = the program's full path,
// data "GpuPreference=1;" = power saving, "=2;" = high performance).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const gpuPrefKey = `Software\Microsoft\DirectX\UserGpuPreferences`

var (
	dxgiDll                = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory1 = dxgiDll.NewProc("CreateDXGIFactory1")
	iidDXGIFactory1        = windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
)

// comCall calls method idx of a COM object's vtable.
func comCall(obj uintptr, idx int, args ...uintptr) uint32 {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return uint32(r)
}

func luidString(high, low uint32) string { return fmt.Sprintf("0x%08x_0x%08x", high, low) }

// dxgiAdapters lists the hardware adapters (IDXGIFactory1::EnumAdapters1 + IDXGIAdapter1::GetDesc1).
func dxgiAdapters() ([]GpuAdapter, error) {
	if err := procCreateDXGIFactory1.Find(); err != nil {
		return nil, err
	}
	var f uintptr
	if r, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidDXGIFactory1)), uintptr(unsafe.Pointer(&f))); uint32(r) != 0 || f == 0 {
		return nil, fmt.Errorf("CreateDXGIFactory1: 0x%08x", uint32(r))
	}
	defer comCall(f, 2) // Release
	var out []GpuAdapter
	for i := uintptr(0); i < 16; i++ {
		var a uintptr
		if hr := comCall(f, 12, i, uintptr(unsafe.Pointer(&a))); hr != 0 || a == 0 { // EnumAdapters1; DXGI_ERROR_NOT_FOUND ends the list
			break
		}
		var raw [48]uint64                                     // DXGI_ADAPTER_DESC1 is 312 bytes
		hr := comCall(a, 10, uintptr(unsafe.Pointer(&raw[0]))) // GetDesc1
		comCall(a, 2)
		if hr != 0 {
			continue
		}
		b := unsafe.Slice((*byte)(unsafe.Pointer(&raw[0])), 384)
		le := binary.LittleEndian
		name := windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&raw[0])), 128))
		vendor := le.Uint32(b[256:])
		mem := float64(le.Uint64(b[272:])) / 1048576
		low, high := le.Uint32(b[296:]), le.Uint32(b[300:])
		flags := le.Uint32(b[304:])
		if flags&2 != 0 { // DXGI_ADAPTER_FLAG_SOFTWARE (Microsoft Basic Render Driver)
			continue
		}
		out = append(out, GpuAdapter{Luid: luidString(high, low), Name: strings.TrimSpace(name), Vendor: vendor, MemMB: mem, Integrated: looksIntegrated(name, vendor, mem)})
	}
	return out, nil
}

var (
	dxgiMu    sync.Mutex
	dxgiCache []GpuAdapter
	dxgiAt    time.Time
)

// cachedAdapters: the adapter list changes only with a driver change or a restart, so DXGI is
// asked once a minute at most.
func cachedAdapters() ([]GpuAdapter, error) {
	dxgiMu.Lock()
	defer dxgiMu.Unlock()
	if dxgiCache != nil && time.Since(dxgiAt) < time.Minute {
		return append([]GpuAdapter(nil), dxgiCache...), nil
	}
	ads, err := dxgiAdapters()
	if err != nil {
		return nil, err
	}
	dxgiCache, dxgiAt = ads, time.Now()
	return append([]GpuAdapter(nil), ads...), nil
}

// GpuSnapshot: the adapters and what every process holds on each of them now.
func GpuSnapshot() ([]GpuAdapter, []GpuProc, error) {
	ads, derr := cachedAdapters()
	q := newPdh()
	if q == nil {
		return ads, nil, errors.New("Windows performance counters are not available")
	}
	defer q.close()
	q.add("ded", `\GPU Process Memory(*)\Dedicated Usage`)
	q.add("sh", `\GPU Process Memory(*)\Shared Usage`)
	q.add("ad", `\GPU Adapter Memory(*)\Dedicated Usage`)
	q.add("ash", `\GPU Adapter Memory(*)\Shared Usage`)
	q.collect()
	time.Sleep(60 * time.Millisecond)
	q.collect()
	adUsed, adShared := map[string]float64{}, map[string]float64{}
	for inst, v := range q.array("ad") {
		adUsed[luidOf(inst)] += v / 1048576
	}
	for inst, v := range q.array("ash") {
		adShared[luidOf(inst)] += v / 1048576
	}
	if len(ads) == 0 { // no DXGI: name the adapters by their counters
		for l, v := range adUsed {
			ads = append(ads, GpuAdapter{Luid: l, Name: "graphics adapter " + l, MemMB: 0, UsedMB: v})
		}
	}
	for i := range ads {
		ads[i].UsedMB = adUsed[ads[i].Luid]
		ads[i].SharedUsedMB = adShared[ads[i].Luid]
	}
	type key struct {
		pid  uint32
		luid string
	}
	m := map[key]*GpuProc{}
	get := func(inst string) *GpuProc {
		pid := pidOf(inst)
		if pid == 0 {
			return nil
		}
		k := key{pid, luidOf(inst)}
		p := m[k]
		if p == nil {
			p = &GpuProc{Pid: pid, Luid: k.luid}
			m[k] = p
		}
		return p
	}
	for inst, v := range q.array("ded") {
		if p := get(inst); p != nil {
			p.DedicatedMB += v / 1048576
		}
	}
	for inst, v := range q.array("sh") {
		if p := get(inst); p != nil {
			p.SharedMB += v / 1048576
		}
	}
	names := processes()
	paths := map[uint32]string{}
	var procs []GpuProc
	for _, p := range m {
		if p.DedicatedMB < 0.5 && p.SharedMB < 0.5 {
			continue
		}
		p.Name = names[p.Pid]
		if p.Name == "" {
			continue // ended since
		}
		if _, ok := paths[p.Pid]; !ok {
			paths[p.Pid] = exePath(p.Pid)
		}
		p.Path = paths[p.Pid]
		procs = append(procs, *p)
	}
	if derr != nil && len(ads) == 0 {
		return ads, procs, derr
	}
	return ads, procs, nil
}

// GpuPrefs reads every per-program GPU choice: value name (program path) -> data.
func GpuPrefs() (map[string]string, error) {
	out := map[string]string{}
	k, err := registry.OpenKey(registry.CURRENT_USER, gpuPrefKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return out, err
	}
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil {
			out[n] = v
		}
	}
	return out, nil
}

// SetGpuPref writes a program's value; data "" deletes it.
func SetGpuPref(path, data string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, gpuPrefKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if data == "" {
		if err := k.DeleteValue(path); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	return k.SetStringValue(path, data)
}

// DisplayDrivers: the display adapters Windows has a driver entry for (including ones that are
// switched off now), for telling "no integrated graphics" from "switched off / no driver".
func DisplayDrivers() []string {
	var out []string
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return nil
	}
	defer k.Close()
	subs, _ := k.ReadSubKeyNames(-1)
	for _, s := range subs {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass+`\`+s, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err != nil {
			continue
		}
		if d, _, err := sk.GetStringValue("DriverDesc"); err == nil && d != "" {
			out = append(out, d)
		}
		sk.Close()
	}
	return out
}

// gpuAppsWriter: every 2 s while the game runs, what each other program holds on the game's card
// (gpu_apps.csv; read by the analysis).
type gpuAppsWriter struct {
	f     *os.File
	paths map[uint32]string
}

func newGpuAppsWriter(dir string) *gpuAppsWriter {
	f, err := os.Create(filepath.Join(dir, "gpu_apps.csv"))
	if err != nil {
		return nil
	}
	f.WriteString("qpc,pid,name,dedicated_mb,shared_mb,path\n")
	return &gpuAppsWriter{f: f, paths: map[uint32]string{}}
}

func (w *gpuAppsWriter) write(qpc int64, ded, sh map[string]float64, gamePid uint32, names map[uint32]string) {
	if w == nil {
		return
	}
	// the game's card: where the game holds the most
	gameLuid, best := "", 0.0
	for inst, v := range ded {
		if pidOf(inst) == gamePid && v > best {
			best, gameLuid = v, luidOf(inst)
		}
	}
	if gameLuid == "" {
		return
	}
	type use struct{ ded, sh float64 }
	per := map[uint32]*use{}
	for inst, v := range ded {
		pid := pidOf(inst)
		if pid == 0 || pid == gamePid || luidOf(inst) != gameLuid {
			continue
		}
		if per[pid] == nil {
			per[pid] = &use{}
		}
		per[pid].ded += v / 1048576
	}
	for inst, v := range sh {
		pid := pidOf(inst)
		if p := per[pid]; p != nil && luidOf(inst) == gameLuid {
			p.sh += v / 1048576
		}
	}
	var b strings.Builder
	tot, totSh := 0.0, 0.0
	clean := func(s string) string { return strings.NewReplacer(",", " ", "\n", " ", "\r", " ").Replace(s) }
	for pid, u := range per {
		tot += u.ded
		totSh += u.sh
		if u.ded < 20 {
			continue
		}
		name := names[pid]
		if name == "" {
			name = fmt.Sprintf("pid %d", pid)
		}
		p, ok := w.paths[pid]
		if !ok {
			p = exePath(pid)
			w.paths[pid] = p
		}
		fmt.Fprintf(&b, "%d,%d,%s,%.1f,%.1f,%s\n", qpc, pid, clean(name), u.ded, u.sh, clean(p))
	}
	fmt.Fprintf(&b, "%d,0,_all_others,%.1f,%.1f,\n", qpc, tot, totSh)
	w.f.WriteString(b.String())
}

func (w *gpuAppsWriter) close() {
	if w != nil {
		w.f.Close()
	}
}

// ProgramExists: the program file is still there.
func ProgramExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
