//go:build !windows

package platform

// Dev stand-in for the graphics-card programs list: a 5080 with Ryzen integrated graphics
// (FRAMELAB_FAKE_IGPU=0: none), a few programs, and the Windows GPU choices kept in memory.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	fakePrefMu sync.Mutex
	fakePrefs  = map[string]string{`C:\Users\Dev\AppData\Local\Programs\Sample\Sample.exe`: "GpuPreference=2;",
		`C:\Program Files (x86)\GOG Galaxy\GalaxyClient.exe`: "SpecificAdapter=1002&164E&164E1002;GpuPreference=1073741824;"}
)

const (
	fakeMain = "0x00000000_0x0000d1a6"
	fakeIgpu = "0x00000000_0x0000c4f2"
)

func GpuSnapshot() ([]GpuAdapter, []GpuProc, error) {
	ads := []GpuAdapter{{Luid: fakeMain, Name: "NVIDIA GeForce RTX 5080", Vendor: 0x10de, MemMB: 16303, UsedMB: 2310, SharedUsedMB: 140}}
	igpu := os.Getenv("FRAMELAB_FAKE_IGPU") != "0"
	if igpu {
		ads = append(ads, GpuAdapter{Luid: fakeIgpu, Name: "AMD Radeon(TM) Graphics", Vendor: 0x1002, MemMB: 512, Integrated: true, UsedMB: 96, SharedUsedMB: 210})
	}
	fakePrefMu.Lock()
	defer fakePrefMu.Unlock()
	on := func(path string) string { // a program set to power saving shows on the integrated graphics (as if restarted)
		for k, v := range fakePrefs {
			if strings.EqualFold(k, path) && (GpuPrefOf(v) == "1" || GpuPrefVendor(v) == 0x1002) && igpu {
				return fakeIgpu
			}
		}
		return fakeMain
	}
	chrome := `C:\Program Files\Google\Chrome\Application\chrome.exe`
	discord := `C:\Users\Dev\AppData\Local\Discord\app-1.0.9170\Discord.exe`
	list := []GpuProc{
		{Pid: 1204, Name: "dwm.exe", Path: `C:\Windows\System32\dwm.exe`, DedicatedMB: 512, SharedMB: 40},
		{Pid: 8812, Name: "chrome.exe", Path: chrome, DedicatedMB: 604, SharedMB: 88},
		{Pid: 8840, Name: "chrome.exe", Path: chrome, DedicatedMB: 12, SharedMB: 3},
		{Pid: 9120, Name: "Discord.exe", Path: discord, DedicatedMB: 243, SharedMB: 31},
		{Pid: 6620, Name: "obs64.exe", Path: `C:\Program Files\obs-studio\bin\64bit\obs64.exe`, DedicatedMB: 388, SharedMB: 12},
		{Pid: 7004, Name: "Notion.exe", Path: `C:\Users\Dev\AppData\Local\Programs\Notion\Notion.exe`, DedicatedMB: 176, SharedMB: 20},
		{Pid: 5544, Name: "steamwebhelper.exe", Path: `C:\Program Files (x86)\Steam\bin\cef\cef.win7x64\steamwebhelper.exe`, DedicatedMB: 92, SharedMB: 8},
		{Pid: 4410, Name: "NVIDIA Overlay.exe", Path: `C:\Program Files\NVIDIA Corporation\NVIDIA app\CEF\NVIDIA Overlay.exe`, DedicatedMB: 118, SharedMB: 6},
		{Pid: 3320, Name: "explorer.exe", Path: `C:\Windows\explorer.exe`, DedicatedMB: 64, SharedMB: 10},
		{Pid: 7720, Name: "Spotify.exe", Path: `C:\Program Files\WindowsApps\SpotifyAB.SpotifyMusic_1.248.0_x64__zpdnekdrzrea0\Spotify.exe`, DedicatedMB: 71, SharedMB: 9},
		{Pid: 9900, Name: "msedgewebview2.exe", Path: `C:\Program Files (x86)\Microsoft\EdgeWebView\Application\130.0.2849.80\msedgewebview2.exe`, DedicatedMB: 58, SharedMB: 7},
		{Pid: 6100, Name: "GalaxyClient Helper.exe", Path: `C:\Program Files (x86)\GOG Galaxy\GalaxyClient Helper.exe`, DedicatedMB: 47, SharedMB: 5},
		{Pid: 6200, Name: "Wacom_Tablet.exe", Path: `C:\Program Files\Tablet\Wacom\Wacom_Tablet.exe`, DedicatedMB: 24, SharedMB: 2},
	}
	if os.Getenv("FRAMELAB_FAKE_GAME_GPU") == "1" { // game running: overlays show the game's memory too
		ads[0].UsedMB = 15970
		list = append(list,
			GpuProc{Pid: 2496, Name: "Cyberpunk2077.exe", Path: `C:\Program Files (x86)\GOG Galaxy\Games\Cyberpunk 2077\bin\x64\Cyberpunk2077.exe`, DedicatedMB: 14272},
			GpuProc{Pid: 5500, Name: "GalaxyClient.exe", Path: `C:\Program Files (x86)\GOG Galaxy\GalaxyClient.exe`, DedicatedMB: 31639})
		list[0].DedicatedMB = 2412 // dwm holds the game's screen buffers
		list[7].DedicatedMB = 3201 // NVIDIA Overlay
	}
	var out []GpuProc
	for _, p := range list {
		p.Luid = fakeMain
		if l := on(p.Path); l != fakeMain {
			p.Luid = l
			p.DedicatedMB = math.Round(p.DedicatedMB * 0.15) // its window still gets copied to the main card
			if p.Name == "GalaxyClient.exe" {
				p.DedicatedMB = 44120 // overlay: the game's memory shared across cards, counted on the integrated graphics
			}
		}
		out = append(out, p)
	}
	return ads, out, nil
}

func GpuPrefs() (map[string]string, error) {
	fakePrefMu.Lock()
	defer fakePrefMu.Unlock()
	out := map[string]string{}
	for k, v := range fakePrefs {
		out[k] = v
	}
	return out, nil
}

func SetGpuPref(path, data string) error {
	fakePrefMu.Lock()
	defer fakePrefMu.Unlock()
	for k := range fakePrefs {
		if strings.EqualFold(k, path) {
			delete(fakePrefs, k)
		}
	}
	if data != "" {
		fakePrefs[path] = data
	}
	return nil
}

func DisplayDrivers() []string {
	return []string{"NVIDIA GeForce RTX 5080", "AMD Radeon(TM) Graphics"} // with FRAMELAB_FAKE_IGPU=0: installed once, switched off now
}

// fake gpu_apps.csv for the dev sampler
func fakeGpuApps(dir string, stop <-chan struct{}, qpc func() int64) {
	f, err := os.Create(filepath.Join(dir, "gpu_apps.csv"))
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString("qpc,pid,name,dedicated_mb,shared_mb,path\n")
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for i := 0; ; i++ {
		x := float64(i)
		rows := []struct {
			pid  int
			name string
			mb   float64
			path string
		}{
			{1204, "dwm.exe", 520 + 20*math.Sin(x/5), `C:\Windows\System32\dwm.exe`},
			{8812, "chrome.exe", 610 + 90*math.Sin(x/9), `C:\Program Files\Google\Chrome\Application\chrome.exe`},
			{9120, "Discord.exe", 240, `C:\Users\Dev\AppData\Local\Discord\app-1.0.9170\Discord.exe`},
			{6620, "obs64.exe", 390, `C:\Program Files\obs-studio\bin\64bit\obs64.exe`},
			{7004, "Notion.exe", 170, `C:\Users\Dev\AppData\Local\Programs\Notion\Notion.exe`},
		}
		tot := 60.0
		for _, r := range rows {
			fmt.Fprintf(f, "%d,%d,%s,%.1f,%.1f,%s\n", qpc(), r.pid, r.name, r.mb, 10.0, r.path)
			tot += r.mb
		}
		fmt.Fprintf(f, "%d,0,_all_others,%.1f,%.1f,\n", qpc(), tot, 60.0)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func ProgramExists(path string) bool { return !strings.Contains(path, "app-1.0.3000") }
