package app

// Experiments: Windows settings people change to make games run better, each one switched on by
// itself and undone exactly; every session remembers which were on, so two sessions in Compare
// show whether a setting helped on this PC.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"framelab/internal/platform"
)

type ExpDef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	What   string `json:"what"`   // what it changes, plainly
	Expect string `json:"expect"` // what to expect / when it helps
	Risk   string `json:"risk,omitempty"`
	When   string `json:"when"`            // when it takes effect
	Action bool   `json:"action"`          // one-shot (run now), not on/off
	Warn   bool   `json:"warn"`            // shown with a warning colour
	Group  string `json:"group"`           // display, cpu, driver, memory
	New    bool   `json:"new"`             // new in this version
	Info   bool   `json:"info"`            // shown only: Frame Lab doesn't change it
	Short  string `json:"short,omitempty"` // a few words next to the name
}

// ExpGroups: the order and titles of the groups on the Experiments tab.
var ExpGroups = []struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Sub   string `json:"sub"`
}{
	{"display", "Display and Windows", "how frames reach the screen"},
	{"cpu", "CPU", "where the game runs"},
	{"power", "Power", "power plan, idle states, PCIe link"},
	{"driver", "Graphics driver", "clocks, timeouts, interrupts and caches"},
	{"memory", "Memory and security", "RAM, Defender, exploit protection"},
	{"files", "Game settings file", "UserSettings.json · close the game first"},
}

var expDefs = []ExpDef{
	{ID: "mpo", Group: "display", New: true, Name: "Multiplane overlay (MPO) off",
		What:   "Stops Windows handing windows to the display hardware as separate layers (multiplane overlay).",
		Expect: "Players report it fixes flicker and stutter on some setups, mostly with more than one monitor or in a borderless window. Try it if you see either.",
		When:   "after a restart"},
	{ID: "gamebar", Group: "display", New: true, Name: "Game Bar background recording off",
		What:   "Turns off Record what happened in the Xbox Game Bar (Windows Settings > Gaming > Captures), which keeps recording the last minutes of play in the background.",
		Expect: "Frees the video encoder and some GPU time. OBS and the NVIDIA app aren't affected. Windows has it off unless you switched it on.",
		When:   "next time the game starts"},
	{ID: "hags", Group: "display", New: true, Info: true, Name: "Hardware-accelerated GPU scheduling",
		What:   "The graphics card schedules its own work instead of Windows (Windows Settings > System > Display > Graphics).",
		Expect: "DLSS frame generation needs it on. Frame Lab only shows it: change it in Windows Settings if you need to, then restart.",
		When:   "after a restart"},
	{ID: "power", Group: "power", Name: "High performance power plan",
		What:   "Switches Windows to the High performance power plan (CPU cores don't park or clock down between frames).",
		Expect: "Can smooth frame times on CPUs that drop clocks quickly. Costs some power at idle.",
		When:   "at once"},
	{ID: "priority", Group: "cpu", Name: "Game at high CPU and disk priority",
		What:   "Tells Windows to start Cyberpunk2077.exe with high CPU priority and high disk priority (Image File Execution Options).",
		Expect: "Helps when other programs compete for the CPU or disk while you play, like recording or streaming. Little effect on an otherwise idle PC.",
		When:   "next time the game starts"},
	{ID: "pcores", Group: "cpu", New: true, Name: "Keep the game on the performance cores", Short: "beta",
		What:   "Keeps Cyberpunk2077.exe on the performance cores of CPUs with two kinds of cores (Intel 12th to 14th gen, Core Ultra), using Windows CPU sets.",
		Expect: "Stops Windows moving game threads to the slower efficiency cores. Works while Frame Lab is open; new in this version and not yet tested on many CPUs.",
		When:   "when the game starts (Frame Lab has to be open)"},
	{ID: "pstate", Group: "driver", Name: "NVIDIA: keep GPU clocks at maximum",
		What:   "Sets DisableDynamicPstate = 1 on the NVIDIA card, so it never steps down to lower clocks.",
		Expect: "Most cards already hold their boost clock under load (check the GPU clock chart of a session first); this mostly adds heat and power at idle.",
		Risk:   "Higher idle power and temperature.",
		When:   "after a restart"},
	{ID: "tdrdelay", Group: "driver", Name: "Longer GPU timeout (TdrDelay 20 s)",
		What:   "Gives the GPU 20 seconds instead of 2 before Windows treats it as hung and resets the driver.",
		Expect: "Avoids driver resets during very long GPU work (heavy path tracing, loading). No effect on fps.",
		When:   "after a restart"},
	{ID: "tdroff", Group: "driver", Name: "Turn off GPU hang recovery (TdrLevel 0)", Warn: true, Short: "risky",
		What:   "Windows stops watching for GPU hangs at all.",
		Expect: "No fps gain. Only stops the 'display driver stopped responding' reset.",
		Risk:   "If the GPU really hangs, the whole PC freezes and needs a hard reset instead of the screen flickering back.",
		When:   "after a restart"},
	{ID: "shadercache", Group: "driver", Name: "Clear the shader caches", Action: true, Warn: true, Short: "next session stutters while shaders are built again",
		What:   "Deletes the graphics driver's and DirectX's shader cache files (NVIDIA DXCache, AMD DxCache / DxcCache, Intel ShaderCache, Windows D3DSCache). The cache size setting is left alone.",
		Expect: "Only useful after a driver update or a broken cache. If your sessions show no stutter from shader builds, leave it.",
		Risk:   "The next session stutters while every shader is built again.",
		When:   "at once (close the game first)"},
	{ID: "standbyauto", Group: "memory", Name: "Clear standby memory before each session",
		What:   "When you start a capture, Frame Lab empties Windows' standby list (cached file data kept in RAM) first, like ISLC does.",
		Expect: "Helps only when free RAM runs low during play; with plenty of RAM it changes nothing.",
		When:   "at the start of each capture"},
	{ID: "standby", Group: "memory", Name: "Clear standby memory now", Action: true,
		What:   "Empties Windows' standby list once.",
		Expect: "Same as above, once.",
		When:   "at once"},
	{ID: "defender", Group: "memory", Name: "Defender exclusion for the game", Warn: true, Short: "risky",
		What:   "Microsoft Defender stops scanning the game folder and Cyberpunk2077.exe.",
		Expect: "Can cut stutter while the game streams files from disk (Defender checks files as they are read).",
		Risk:   "Anything placed in the game folder isn't scanned (mods you download are still scanned in Downloads).",
		When:   "at once"},
	{ID: "cfg", Group: "memory", Name: "Turn off Control Flow Guard for the game", Warn: true, Short: "risky",
		What:   "Switches off the CFG exploit protection for Cyberpunk2077.exe only (Windows Security > App & browser control > Exploit protection, set per program).",
		Expect: "CFG adds a small check to indirect calls; some people measure a little more CPU headroom without it.",
		Risk:   "Removes a protection against exploits for this one program. (DEP can't be turned off for 64-bit games, so it isn't offered.)",
		When:   "next time the game starts"},

	// ---- 2.0
	{ID: "gamemode", Group: "display", New: true, Name: "Game Mode off",
		What:   "Turns off Windows Game Mode (Windows Settings > Gaming > Game Mode), which gives the running game priority and holds back Windows Update installs.",
		Expect: "Game Mode usually helps. Some players report more even frame pacing with it off, mostly on older Windows 10 builds. Try it only if other settings didn't fix a stutter.",
		When:   "next time the game starts"},
	{ID: "quiet", Group: "display", New: true, Name: "Quiet session",
		What:   "When a capture starts, Frame Lab stops Windows Search indexing, Windows Update and SysMain (Superfetch), and starts them again when the session ends.",
		Expect: "Stops background disk and CPU work that can cause hitches during play. Only for the session: nothing stays changed.",
		When:   "at the start of each capture"},
	{ID: "x3d", Group: "cpu", New: true, Name: "Keep the game on the 3D V-Cache cores", Short: "beta",
		What:   "Keeps Cyberpunk2077.exe on the cores with the big 3D V-Cache on Ryzen 7900X3D / 7950X3D / 9900X3D / 9950X3D, using Windows CPU sets.",
		Expect: "AMD's driver normally does this through Game Bar. When it guesses wrong the game runs on the other die, without the extra cache. Works while Frame Lab is open.",
		When:   "when the game starts (Frame Lab has to be open)"},
	{ID: "timer", Group: "cpu", New: true, Name: "Timer resolution 0.5 ms",
		What:   "While Frame Lab is open it asks Windows for a 0.5 ms timer. On Windows 11 it also lets one program's timer request count for every program (GlobalTimerResolutionRequests), so the game gets it too.",
		Expect: "Can make frame pacing more even where the game waits on short sleeps (frame limiters, Reflex off). The line under the name shows the timer Windows actually runs at.",
		When:   "at once (the Windows 11 switch after a restart)"},
	{ID: "idle", Group: "power", New: true, Name: "CPU idle states off", Warn: true, Short: "risky",
		What:   "Stops the CPU entering its idle (C) states in the active power plan (Processor idle disable).",
		Expect: "Can lower latency spikes when cores wake up. The CPU then runs at full power all the time, at the desktop too.",
		Risk:   "Much higher temperature and power draw for as long as it is on. Not for laptops or small cases.",
		When:   "at once"},
	{ID: "aspm", Group: "power", New: true, Name: "PCIe link power saving off",
		What:   "Sets PCI Express > Link State Power Management to Off in the active power plan.",
		Expect: "Stops the graphics card's PCIe link dropping to a low-power state between bursts of work; can help with micro-stutter on some boards. A few watts more at idle.",
		When:   "at once"},
	{ID: "msi", Group: "driver", New: true, Name: "Graphics card interrupts in MSI mode",
		What:   "Switches the graphics card to message-signalled interrupts (MSISupported = 1 for the card in the registry).",
		Expect: "Most NVIDIA and AMD cards already use MSI mode: this only matters if the Checks tab shows it off. Can lower interrupt latency.",
		When:   "after a restart"},
	{ID: "memcomp", Group: "memory", New: true, Name: "Memory compression off",
		What:   "Windows stops compressing memory pages in RAM (Disable-MMAgent -MemoryCompression).",
		Expect: "Saves a little CPU work on PCs with plenty of RAM (32 GB or more). With 16 GB it can make things worse: the page file gets used sooner.",
		When:   "after a restart"},
	{ID: "hvci", Group: "memory", New: true, Action: true, Name: "Memory integrity (core isolation)",
		What:   "Windows Security > Device security > Core isolation > Memory integrity runs drivers in a protected virtual machine.",
		Expect: "Microsoft's own advice for gamers is that some games lose a few percent with it on. Frame Lab doesn't switch it: Open Windows Security takes you to the page, then restart.",
		Risk:   "Turning it off removes a protection against malicious drivers.",
		When:   "after a restart"},
	{ID: "nvperf", Group: "driver", New: true, Name: "NVIDIA: prefer maximum performance",
		What:   "Sets Power management mode to Prefer maximum performance in the NVIDIA driver's Cyberpunk 2077 profile (the same switch as in the NVIDIA app). Other games keep their setting.",
		Expect: "The card stays at full clocks while the game runs instead of stepping down in light scenes, which can make frame times uneven when the load jumps. Costs some power in menus and light scenes.",
		When:   "next time the game starts"},
	{ID: "nvdlss", Group: "driver", New: true, Name: "NVIDIA: DLSS latest model",
		What:   "Turns on the driver's DLSS override for Cyberpunk 2077 with the Latest model for Super Resolution and Ray Reconstruction (in the NVIDIA app: DLSS Override, Model presets, Latest).",
		Expect: "The driver uses its newest DLSS model instead of the one the game's files ask for: usually sharper in motion with less ghosting. The DLSS indicator below shows what really runs.",
		When:   "next time the game starts"},
	{ID: "dlssind", Group: "driver", New: true, Name: "DLSS indicator on screen",
		What:   "Shows NVIDIA's DLSS indicator in a corner of the screen: the DLSS version, preset and resolution in use (ShowDlssIndicator in the driver's settings).",
		Expect: "Doesn't change performance. Use it to check that a DLSS override or newer DLSS files are really used. It shows in every DLSS game until you switch it off.",
		When:   "next time the game starts"},
	{ID: "dlssdll", Group: "driver", New: true, Name: "Newer DLSS files for the game",
		What:   "Replaces the game's nvngx_dlss, nvngx_dlssd and nvngx_dlssg .dll with newer ones you put in the DLSS files folder next to Frame Lab. Only files signed by NVIDIA and newer than the game's are used; Frame Lab keeps an exact copy of the old ones and puts them back on undo.",
		Expect: "Mostly for when the driver's DLSS override can't be used. Frame Lab doesn't download anything: take the files from NVIDIA's DLSS release on GitHub (NVIDIA/DLSS) or another source you trust.",
		Risk:   "A game update can replace them again. Frame Lab then leaves the game's new files alone.",
		When:   "next time the game starts (close the game first)"},
	{ID: "gpuirq", Group: "driver", New: true, Name: "Graphics card interrupts on their own core", Warn: true, Short: "risky",
		What:   "Sends the graphics card's interrupts to one CPU thread (the first thread of the second core) instead of letting Windows choose: the registry setting AutoGpuAffinity uses (Interrupt Management, Affinity Policy).",
		Expect: "Keeps the card's interrupts off CPU 0, which handles most of Windows' own work. Results differ from PC to PC: compare a session before and after.",
		Risk:   "On some PCs it makes input latency or frame pacing worse. Undo gives the choice back to Windows.",
		When:   "after a restart"},
	{ID: "pagefile", Group: "memory", New: true, Name: "Fixed page file on the fastest drive",
		What:   "Sets one page file of a fixed size (the size of your RAM, 16 to 32 GB) on the fastest drive that has room, instead of one Windows grows and shrinks.",
		Expect: "Avoids the pause when Windows grows the page file while the game uses a lot of memory. Matters most with 16 GB of RAM. Uses that much disk space.",
		When:   "after a restart"},
	{ID: "crowd", Group: "files", New: true, Name: "Crowd density Low",
		What:   "Sets Crowd Density to Low in the game's settings file (both places the game keeps it).",
		Expect: "Fewer people and cars in the streets: the biggest CPU setting the game has. Helps most in busy areas like Japantown and City Center.",
		When:   "next time the game starts"},
	{ID: "smt", Group: "files", New: true, Name: "SMT On (Ryzen)",
		What:   "Sets SMT to On in the game's settings file (Gameplay > Performance), instead of Auto.",
		Expect: "Auto leaves the second thread of each core unused on some Ryzen CPUs; On uses both. Usually helps 6 and 8-core Ryzens in busy areas.",
		When:   "next time the game starts"},
	{ID: "hybrid", Group: "files", New: true, Name: "Hybrid CPU: prioritise P-cores",
		What:   "Sets Hybrid CPU Utilization to Prioritize P-cores in the game's settings file.",
		Expect: "The game's own way of keeping its main threads on the performance cores of Intel 12th to 14th gen and Core Ultra CPUs.",
		When:   "next time the game starts"},
}

type expSaved struct {
	Prev map[string]string `json:"prev"`
	At   time.Time         `json:"at"`
	Game string            `json:"game"`
}

type expFile struct {
	Applied     map[string]expSaved `json:"applied"`
	StandbyAuto bool                `json:"standby_auto"`
	QuietAuto   bool                `json:"quiet_auto"`
}

var expMu sync.Mutex

func expPath(data string) string { return filepath.Join(data, "experiments.json") }

func loadExp(data string) *expFile {
	f := &expFile{Applied: map[string]expSaved{}}
	if b, err := os.ReadFile(expPath(data)); err == nil {
		json.Unmarshal(b, f)
	}
	if f.Applied == nil {
		f.Applied = map[string]expSaved{}
	}
	return f
}

func saveExp(data string, f *expFile) error {
	b, _ := json.MarshalIndent(f, "", " ")
	return os.WriteFile(expPath(data), b, 0o644)
}

type ExpState struct {
	ExpDef
	On     bool   `json:"on"`
	Detail string `json:"detail"`
	OK     bool   `json:"ok"`   // can be used on this PC
	Ours   bool   `json:"ours"` // switched on by Frame Lab (undo restores exactly what was there)
	Since  string `json:"since,omitempty"`
}

// experimentStates reads every setting (a few seconds: two of them ask PowerShell).
func experimentStates(data, game string) []ExpState {
	expMu.Lock()
	f := loadExp(data)
	expMu.Unlock()
	out := make([]ExpState, len(expDefs))
	var wg sync.WaitGroup
	for i, d := range expDefs {
		wg.Add(1)
		go func(i int, d ExpDef) {
			defer wg.Done()
			st := ExpState{ExpDef: d}
			if isGameFileExp(d.ID) {
				st.On, st.Detail, st.OK = usRead(d.ID)
			} else {
				st.On, st.Detail, st.OK = platform.ExpRead(d.ID, game)
			}
			switch d.ID {
			case "standbyauto":
				st.On = f.StandbyAuto
			case "quiet":
				st.On = f.QuietAuto
			case "pcores", "x3d", "timer", "dlssdll":
				_, st.On = f.Applied[d.ID]
			}
			if d.ID == "hags" && st.On {
				st.Short = "needed for frame generation"
			}
			if s, ok := f.Applied[d.ID]; ok {
				st.Ours = true
				st.OK = true // whatever Frame Lab switched on can always be undone
				st.Since = s.At.Format("2 Jan 15:04")
			}
			out[i] = st
		}(i, d)
	}
	wg.Wait()
	// NVIDIA-only settings stay out of the list on PCs without an NVIDIA card (unless Frame Lab
	// switched one on before, so it can still be undone)
	kept := out[:0]
	for _, st := range out {
		if nvidiaOnly[st.ID] && !st.OK && !st.Ours && platform.GPU().Vendor != "nvidia" {
			continue
		}
		kept = append(kept, st)
	}
	return kept
}

var nvidiaOnly = map[string]bool{"pstate": true, "nvperf": true, "nvdlss": true, "dlssind": true, "dlssdll": true}

func setExperiment(data, game, id string, on bool) error {
	expMu.Lock()
	defer expMu.Unlock()
	f := loadExp(data)
	if id == "standbyauto" {
		f.StandbyAuto = on
		return saveExp(data, f)
	}
	if id == "quiet" {
		f.QuietAuto = on
		return saveExp(data, f)
	}
	if len(platform.FindGame()) > 0 && (id == "priority" || id == "cfg" || id == "dlssdll") {
		return errors.New("close the game first")
	}
	if on {
		if _, done := f.Applied[id]; done {
			return nil
		}
		var prev map[string]string
		var err error
		if isGameFileExp(id) {
			prev, err = usOn(id)
		} else {
			prev, err = platform.ExpOn(id, game)
		}
		if err != nil {
			return err
		}
		f.Applied[id] = expSaved{Prev: prev, At: time.Now(), Game: game}
		switch id {
		case "pcores":
			platform.PinGame()
		case "x3d":
			platform.PinGameX3D()
		}
		return saveExp(data, f)
	}
	// off: put back exactly what was there, or (switched on outside Frame Lab) Windows' default
	s, ok := f.Applied[id]
	prev := s.Prev
	if !ok {
		prev = map[string]string{"scheme": "381b4222-f694-41f0-9685-ff5bb260df2e"} // power: Balanced
		s.Game = game
	}
	var err error
	if isGameFileExp(id) {
		if ok {
			err = usRestore(id, prev)
		} else {
			err = errors.New("set outside Frame Lab: change it back in the game's settings")
		}
	} else {
		err = platform.ExpRestore(id, s.Game, prev)
	}
	if err != nil {
		return err
	}
	delete(f.Applied, id)
	return saveExp(data, f)
}

// undoEverything: every Experiment Frame Lab switched on goes back to how it was, the per-session
// ones are switched off, and every fix in the undo journal is put back. Returns what was undone
// and what failed (that stays as it is, so it can be tried again).
func undoEverything(data string) (int, []string) {
	expMu.Lock()
	f := loadExp(data)
	expMu.Unlock()
	n := 0
	var errs []string
	ids := make([]string, 0, len(f.Applied))
	for id := range f.Applied {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := setExperiment(data, f.Applied[id].Game, id, false); err != nil {
			errs = append(errs, expName(id)+": "+err.Error())
			continue
		}
		n++
	}
	if f.StandbyAuto || f.QuietAuto {
		expMu.Lock()
		g := loadExp(data)
		g.StandbyAuto, g.QuietAuto = false, false
		saveExp(data, g)
		expMu.Unlock()
		n++
	}
	m, e2 := undoAll(data)
	return n + m, append(errs, e2...)
}

func expName(id string) string {
	for _, d := range expDefs {
		if d.ID == id {
			return d.Name
		}
	}
	return id
}

func quietAuto(data string) bool {
	expMu.Lock()
	defer expMu.Unlock()
	return loadExp(data).QuietAuto
}

func runExperiment(data, game, id string) (string, error) {
	if id == "shadercache" && len(platform.FindGame()) > 0 {
		return "", errors.New("close the game first")
	}
	return platform.ExpRun(id, game)
}

// experimentsOn: the names of the settings that are on, for the session's notes
func experimentsOn(data, game string) []string {
	var out []string
	for _, s := range experimentStates(data, game) {
		if s.On && !s.Action {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

func standbyAuto(data string) bool {
	expMu.Lock()
	defer expMu.Unlock()
	return loadExp(data).StandbyAuto
}

func (s *Server) buildPath(i int) string {
	cfg := s.Settings.Get()
	if i >= 0 && i < len(cfg.Builds) {
		return cfg.Builds[i].Path
	}
	if len(cfg.Builds) > 0 {
		return cfg.Builds[0].Path
	}
	return ""
}

// pinCoresLoop: with "Keep the game on the performance cores" on, pin each new game process.
func pinCoresLoop(data string) {
	for {
		time.Sleep(5 * time.Second)
		expMu.Lock()
		ap := loadExp(data).Applied
		expMu.Unlock()
		if _, on := ap["pcores"]; on {
			platform.PinGame()
		}
		if _, on := ap["x3d"]; on {
			platform.PinGameX3D()
		}
		// the 0.5 ms timer is held by this process: ask again (cheap) so it stays after a sleep
		if _, on := ap["timer"]; on {
			platform.HoldTimer(true)
		}
	}
}

// nvProfile: the NVIDIA driver's settings for the game (read only), cached for a minute.
var (
	nvMu   sync.Mutex
	nvAt   time.Time
	nvList []platform.NvSetting
	nvErr  string
)

func nvProfile() ([]platform.NvSetting, string) {
	nvMu.Lock()
	defer nvMu.Unlock()
	if time.Since(nvAt) > time.Minute {
		nvList, nvErr = nil, ""
		if l, err := platform.NvProfile(); err != nil {
			nvErr = err.Error()
		} else {
			nvList = l
		}
		nvAt = time.Now()
	}
	return nvList, nvErr
}
