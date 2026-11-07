package app

// Checks tab: things that quietly cost frames or add stutter, read from the PC and the game build.
// Read only, except a few fixes that keep what was there (data\undo\checks.json) so Undo puts it back.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"framelab/internal/platform"
)

type CheckRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Detail      string `json:"detail"`
	Status      string `json:"status"` // fine, check, found, info, na
	Label       string `json:"label"`  // the status word shown
	Action      string `json:"action,omitempty"`
	ActionLabel string `json:"action_label,omitempty"`
	What        string `json:"what,omitempty"`    // shown when the row is opened
	UndoID      string `json:"undo_id,omitempty"` // journal entry a fix of this row writes
}

type CheckGroup struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Sub   string     `json:"sub"`
	Rows  []CheckRow `json:"rows"`
}

type ChecksReport struct {
	Build  string       `json:"build"`
	At     time.Time    `json:"at"`
	Groups []CheckGroup `json:"groups"`
}

var (
	checksMu    sync.Mutex
	checksCache = map[string]*ChecksReport{}
)

func row(id, name, detail, status, label string) CheckRow {
	return CheckRow{ID: id, Name: name, Detail: detail, Status: status, Label: label}
}

func (r CheckRow) act(id, label string) CheckRow { r.Action, r.ActionLabel = id, label; return r }
func (r CheckRow) why(s string) CheckRow         { r.What = s; return r }

// Checks: the cached report for a build, or a new one when fresh is set or there is none yet
func (s *Server) checks(i int, fresh bool) (*ChecksReport, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return nil, errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	b := cfg.Builds[i]
	checksMu.Lock()
	defer checksMu.Unlock()
	if r, ok := checksCache[b.Path]; ok && !fresh {
		return r, nil
	}
	r := runChecks(cfg, b, s.Paths)
	checksCache[b.Path] = r
	return r, nil
}

func runChecks(cfg Config, b BuildCfg, p Paths) *ChecksReport {
	f := platform.ReadCheckFacts()
	game := b.Path
	us := readUserSettings()
	nv, _ := nvProfile()
	nvv := map[string]string{}
	for _, x := range nv {
		nvv[x.Name] = x.Value
	}
	r := &ChecksReport{Build: b.Name, At: time.Now()}

	// ---- 01 Frame Lab itself
	g := CheckGroup{ID: "framelab", Title: "Frame Lab itself", Sub: "what recording costs"}
	g.Rows = append(g.Rows, lastSessionCost(p)...)
	if cfg.TrackFrameType {
		g.Rows = append(g.Rows, row("frametype", "Frame-type tracking", "on: PresentMon can use a whole core and add spikes of its own (PresentMon issue #695)", "check", "Check").
			act("frametype_off", "Turn off").why("Frame-type tracking tells generated frames apart from real ones. On some PCs it makes PresentMon use a whole CPU core and adds frame time spikes of its own, so it is best off unless you need it."))
	} else {
		g.Rows = append(g.Rows, row("frametype", "Frame-type tracking", "off", "fine", "Fine"))
	}
	r.Groups = append(r.Groups, g)

	// ---- 02 between the game and the screen
	g = CheckGroup{ID: "screen", Title: "Between the game and the screen", Sub: "display, frame limiters, injectors, DLSS files"}
	for _, d := range f.Displays {
		if !d.Primary && len(f.Displays) > 1 {
			continue
		}
		name := "Display"
		if d.Monitor != "" && d.Monitor != "Generic PnP Monitor" {
			name = "Display (" + d.Monitor + ")"
		}
		vrr, vrrOff := "", false
		switch v := strings.ToLower(nvv["G-SYNC"]); {
		case strings.HasPrefix(v, "allowed"):
			vrr = " · G-SYNC allowed"
		case strings.HasPrefix(v, "fixed refresh"), strings.HasPrefix(v, "forced off"), strings.HasPrefix(v, "not allowed"):
			vrr, vrrOff = " · variable refresh (G-SYNC) off: the driver is set to "+v, true
		case v != "":
			vrr = " · G-SYNC " + v
		}
		switch {
		case d.MaxHz > d.Hz+2 && d.Hz > 0: // 119 and 120 are the same mode (119.88 Hz) listed twice
			g.Rows = append(g.Rows, row("display", name, fmt.Sprintf("%d Hz, it can do %d Hz%s", d.Hz, d.MaxHz, vrr), "check", "Check").
				why("Windows runs the screen below its highest refresh rate. Settings > System > Display > Advanced display > Choose a refresh rate."))
		case d.Hz > 0 && vrrOff:
			g.Rows = append(g.Rows, row("display", name, fmt.Sprintf("%d Hz, the highest it offers%s", d.Hz, vrr), "info", "VRR off").
				why("With variable refresh the screen waits for each frame instead of showing some frames twice, so drops below the refresh rate look smoother and V-sync isn't needed. If the screen supports G-SYNC or HDMI 2.1 VRR, switch it on in NVIDIA Control Panel > Set up G-SYNC (on a TV, VRR or Game Mode has to be on too). If it doesn't, this row can be ignored."))
		case d.Hz > 0:
			g.Rows = append(g.Rows, row("display", name, fmt.Sprintf("%d Hz, the highest it offers%s", d.Hz, vrr), "fine", "Fine"))
		}
	}
	lim := []string{}
	inGame := "off"
	if on, _ := us["/video/display/MaximumFPS_OnOff"].(bool); on {
		inGame = fmt.Sprintf("%v fps", us["/video/display/MaximumFPS_Value"])
		lim = append(lim, "in game")
	}
	drv := nvv["Max frame rate"]
	if drv != "" && !strings.HasPrefix(strings.ToLower(drv), "off") {
		lim = append(lim, "NVIDIA driver")
	} else if drv == "" {
		drv = "–"
	}
	rtss := "not running"
	if f.RTSS {
		rtss = "running (its limit isn't readable)"
		lim = append(lim, "RTSS")
	}
	det := "in game " + inGame + " · NVIDIA driver " + strings.ToLower(drv) + " · RTSS " + rtss
	switch {
	case len(lim) > 1:
		g.Rows = append(g.Rows, row("limiters", "Frame limiters", det, "check", "Two").why("More than one frame cap is set. They can fight each other and make pacing uneven: keep one (the NVIDIA driver's or the game's)."))
	case len(lim) == 1:
		g.Rows = append(g.Rows, row("limiters", "Frame limiters", det, "fine", "One"))
	default:
		g.Rows = append(g.Rows, row("limiters", "Frame limiters", det, "info", "None"))
	}
	if inj := injected(game); len(inj) > 0 {
		g.Rows = append(g.Rows, row("injected", "Injected into the game", strings.Join(inj, " · "), "info", "Info").
			why("These load into the game and change frames on their way to the screen. Each one adds a little to every frame and can be the thing behind a crash; compare a session without it when in doubt."))
	} else {
		g.Rows = append(g.Rows, row("injected", "Injected into the game", "nothing besides the mod frameworks", "fine", "Fine"))
	}
	if d := dlssFiles(game); d != "" {
		g.Rows = append(g.Rows, row("dlss", "DLSS files in the game folder", d, "info", "Info").
			why("The NVIDIA app can load newer DLSS files over these (DLSS override). A session report shows which ones the game really used."))
	}
	r.Groups = append(r.Groups, g)

	// ---- 03 hardware
	g = CheckGroup{ID: "hardware", Title: "Hardware", Sub: "card link, memory, drives, CPU temperature"}
	if f.PCIe.Known {
		pc := f.PCIe
		switch {
		case pc.CurWidth > 0 && pc.CurWidth < pc.MaxWidth:
			g.Rows = append(g.Rows, row("pcie", "Graphics card link", fmt.Sprintf("PCIe x%d, the card can use x%d", pc.CurWidth, pc.MaxWidth), "check", "Check").
				why("The card runs on fewer PCIe lanes than it can. Usual causes: a second slot taking lanes, a riser cable, or the card in the wrong slot."))
		case pc.CurGen < pc.MaxGen:
			g.Rows = append(g.Rows, row("pcie", "Graphics card link", fmt.Sprintf("Gen %d x%d now, Gen %d x%d possible: cards slow the link when idle, a session checks it under load", pc.CurGen, pc.CurWidth, pc.MaxGen, pc.MaxWidth), "info", "Idle"))
		default:
			g.Rows = append(g.Rows, row("pcie", "Graphics card link", fmt.Sprintf("PCIe Gen %d x%d, the card's maximum", pc.CurGen, pc.CurWidth), "fine", "Fine"))
		}
	}
	if f.RAM.Known {
		rm := f.RAM
		if platform.RAMStock(rm) {
			g.Rows = append(g.Rows, row("ram", "Memory speed", fmt.Sprintf("%d MT/s %s, the stock speed: XMP / EXPO looks off", rm.MTs, rm.Type), "check", "Check").
				why("The memory runs at its safe stock speed. The speed on the box needs XMP (Intel) or EXPO (AMD) switched on in the BIOS; it usually means better 1% lows."))
		} else {
			d := fmt.Sprintf("%d MT/s %s, %d GB", rm.MTs, rm.Type, rm.SizeGB)
			if rm.RatedMTs > 0 && rm.MTs > rm.RatedMTs {
				d += fmt.Sprintf(", XMP / EXPO on (stock %d)", rm.RatedMTs)
			}
			g.Rows = append(g.Rows, row("ram", "Memory speed", d, "fine", "Fine"))
		}
	}
	gd := platform.DriveInfo(game)
	cd := platform.DriveInfo(os.Getenv("SystemDrive") + `\`)
	if gd.Root != "" {
		kind := "drive"
		switch {
		case gd.Bus == "NVMe":
			kind = "NVMe SSD"
		case gd.Known && gd.SSD:
			kind = strings.TrimSpace(strings.TrimSuffix(gd.Bus, "other") + " SSD")
		case gd.Known:
			kind = "hard drive"
		case gd.Bus != "" && gd.Bus != "other":
			kind = gd.Bus + " drive"
		}
		d := fmt.Sprintf("%s %s · %.0f GB free", strings.TrimSuffix(gd.Root, `\`), kind, gd.FreeGB)
		if !strings.EqualFold(cd.Root, gd.Root) && cd.Root != "" {
			d += fmt.Sprintf(" · %s %.0f GB free for the page file and shader cache", strings.TrimSuffix(cd.Root, `\`), cd.FreeGB)
		}
		st, lb := "fine", "Fine"
		why := ""
		if gd.Known && !gd.SSD {
			st, lb, why = "check", "Check", "The game is on a hard drive. Cyberpunk streams the city from disk while you move, so a hard drive means pop-in and stutter. An SSD fixes it."
		}
		if gd.FreeGB > 0 && gd.FreeGB < 20 || cd.FreeGB > 0 && cd.FreeGB < 15 {
			st, lb, why = "check", "Low", "Little free space: Windows needs room for the page file and the driver for its shader cache, and both slow down when the drive is nearly full."
		}
		g.Rows = append(g.Rows, row("drive", "Game drive", d, st, lb).why(why))
	}
	cpu := platform.CPU()
	amd := strings.Contains(strings.ToUpper(cpu.Name), "AMD")
	switch {
	case f.HWiNFO:
		g.Rows = append(g.Rows, row("cputemp", "CPU temperature", "read from HWiNFO (shared memory on)", "info", "HWiNFO"))
	case f.PawnIO != "" && amd:
		g.Rows = append(g.Rows, row("cputemp", "CPU temperature", "read through PawnIO "+f.PawnIO, "fine", "Fine"))
	case f.PawnIO != "":
		g.Rows = append(g.Rows, row("cputemp", "CPU temperature", "PawnIO "+f.PawnIO+" is installed, but Frame Lab reads it only on AMD Ryzen: run HWiNFO with Shared Memory Support on", "info", "Not set up"))
	default:
		r := row("cputemp", "CPU temperature", "not read: run HWiNFO with Shared Memory Support on while you play", "info", "Not set up").
			why("Windows can't read the CPU temperature by itself. While HWiNFO runs with its Shared Memory Support option on, Frame Lab reads the temperature from it.")
		if amd {
			r = row("cputemp", "CPU temperature", "not read: install PawnIO (the signed driver LibreHardwareMonitor and Fan Control use) or run HWiNFO", "info", "Not set up").
				act("pawnio_get", "Get PawnIO").
				why("Windows can't read the CPU temperature by itself. Get PawnIO opens pawnio.eu: download its installer there and run it yourself (Windows asks you first). Frame Lab never installs drivers. Or run HWiNFO with Shared Memory Support on.")
		}
		g.Rows = append(g.Rows, r)
	}
	if platform.TwoGroupX3D(cpu.Name) {
		switch f.VCacheSvc {
		case "running":
			g.Rows = append(g.Rows, row("vcache", "AMD 3D V-Cache optimizer", "running: it keeps games on the cache cores", "fine", "Fine"))
		case "stopped":
			g.Rows = append(g.Rows, row("vcache", "AMD 3D V-Cache optimizer", "installed but not running", "check", "Check").why("AMD's chipset driver puts games on the cores with the extra cache. Its service isn't running, so the game can land on the other core group."))
		default:
			g.Rows = append(g.Rows, row("vcache", "AMD 3D V-Cache optimizer", "not installed: install AMD's chipset driver", "check", "Check").why("On processors with two core groups, AMD's chipset driver keeps games on the group with the extra cache. Without it the game can land on the other one."))
		}
	} else if cpu.Name != "" {
		g.Rows = append(g.Rows, row("vcache", "AMD 3D V-Cache optimizer", "not needed on this processor", "na", ""))
	}
	for _, m := range gameCards(f.MSI) {
		switch {
		case m.Known && m.MSI:
			g.Rows = append(g.Rows, row("msi", "Card interrupts (MSI mode)", m.Device+": on", "fine", "Fine"))
		case m.Known:
			g.Rows = append(g.Rows, row("msi", "Card interrupts (MSI mode)", m.Device+": off (line-based interrupts)", "check", "Check").why("The card signals the CPU the old way, which can share a line with other devices and add latency. Experiments can switch MSI mode on (needs a restart, Undo puts it back)."))
		default:
			g.Rows = append(g.Rows, row("msi", "Card interrupts (MSI mode)", m.Device+": driver default", "fine", "Fine"))
		}
	}
	r.Groups = append(r.Groups, g)

	// ---- 04 mod frameworks
	sub := "and the logs they keep"
	if gv := gameVersion(filepath.Join(game, "bin", "x64", "Cyberpunk2077.exe")); gv != "" && gv != "?" {
		sub = "on game version " + gv + ", and the logs they keep"
	}
	g = CheckGroup{ID: "frameworks", Title: "Mod frameworks", Sub: sub}
	g.Rows = append(g.Rows, frameworkRows(game)...)
	g.Rows = append(g.Rows, debugRow(game))
	r.Groups = append(r.Groups, g)

	// ---- 05 old tweaks
	g = CheckGroup{ID: "tweaks", Title: "Old tweaks", Sub: "things that stay after a guide is forgotten"}
	bt := []string{}
	for _, k := range []string{"useplatformclock", "useplatformtick", "disabledynamictick", "tscsyncpolicy"} {
		if v, ok := f.BootTimer[k]; ok {
			bt = append(bt, k+" "+v)
		}
	}
	switch {
	case f.BootTimer["error"] != "":
		g.Rows = append(g.Rows, row("bcd", "HPET and timer tweaks (bcdedit)", f.BootTimer["error"], "info", "Unknown"))
	case len(bt) > 0:
		g.Rows = append(g.Rows, row("bcd", "HPET and timer tweaks (bcdedit)", strings.Join(bt, " · "), "found", "Found").act("bcd_clear", "Remove").
			why("Old guides set these boot options. On today's PCs they usually make timing worse (HPET forced on is the classic). Remove puts Windows' default back; Undo sets them again."))
	default:
		g.Rows = append(g.Rows, row("bcd", "HPET and timer tweaks (bcdedit)", "Windows default", "fine", "Fine"))
	}
	if f.ReservedCpuSets {
		g.Rows = append(g.Rows, row("cpusets", "Reserved CPU sets", "some cores are reserved away from programs", "found", "Found").act("cpusets_clear", "Clear").
			why("A registry tweak keeps some cores away from everything except Windows. Left over from a guide, it takes cores away from the game."))
	} else {
		g.Rows = append(g.Rows, row("cpusets", "Reserved CPU sets", "none", "fine", "Fine"))
	}
	exe := filepath.Join(game, "bin", "x64", "Cyberpunk2077.exe")
	if _, err := os.Stat(exe); err == nil {
		switch platform.ExeSignature(exe) {
		case "ok":
			g.Rows = append(g.Rows, row("exe", "Patched game exe (old SMT fix)", "the exe carries its original signature", "fine", "Fine"))
		case "changed":
			g.Rows = append(g.Rows, row("exe", "Patched game exe (old SMT fix)", "the exe was changed after it was signed", "found", "Found").
				why("Old SMT fixes patched the exe. The game has had its own SMT setting for years; verify the game files in GOG or Steam to get the original exe back."))
		case "unsigned":
			g.Rows = append(g.Rows, row("exe", "Patched game exe (old SMT fix)", "this exe has no signature, so a change can't be seen", "info", "Unknown"))
		}
	}
	if ro, ok := platform.FileReadOnly(platform.UserSettingsPath()); ok {
		if ro {
			g.Rows = append(g.Rows, row("usro", "UserSettings.json read-only", "read-only: the game can't save settings", "found", "Found").act("us_writable", "Make writable").
				why("Some guides make the settings file read-only to lock tweaks in. The game then can't save changes you make in its menus."))
		} else {
			g.Rows = append(g.Rows, row("usro", "UserSettings.json read-only", "writable", "fine", "Fine"))
		}
	}
	if len(f.FTH) > 0 {
		g.Rows = append(g.Rows, row("fth", "Fault Tolerant Heap", "Windows put Cyberpunk2077.exe on its list after crashes: it slows memory calls", "found", "Found").act("fth_clear", "Clear").
			why("After a program crashes a few times Windows runs it with a slower, safer memory allocator from then on. Clearing takes the game off that list; Undo puts it back."))
	} else {
		g.Rows = append(g.Rows, row("fth", "Fault Tolerant Heap", "the game isn't on its list", "fine", "Fine"))
	}
	r.Groups = append(r.Groups, g)

	// ---- 06 security
	g = CheckGroup{ID: "security", Title: "Windows security", Sub: "virtualisation-based security"}
	switch {
	case f.HVCI:
		g.Rows = append(g.Rows, row("hvci", "VBS and memory integrity", "both on: about 5% fewer fps with ray tracing (Tom's Hardware). Experiments has the steps to test it off", "info", "On").act("goto:experiments", "Experiments"))
	case f.VBS:
		g.Rows = append(g.Rows, row("hvci", "VBS and memory integrity", "VBS on, memory integrity off", "info", "VBS"))
	default:
		g.Rows = append(g.Rows, row("hvci", "VBS and memory integrity", "off", "fine", "Off"))
	}
	r.Groups = append(r.Groups, g)
	undoIDs := map[string]string{"fth": "fth", "debuglog": "debuglog:" + game, "usro": "usro", "bcd": "bcd_clear", "cpusets": "cpusets_clear"}
	for gi := range r.Groups {
		for ri := range r.Groups[gi].Rows {
			r.Groups[gi].Rows[ri].UndoID = undoIDs[r.Groups[gi].Rows[ri].ID]
		}
	}
	return r
}

var reIntegrated = regexp.MustCompile(`(?i)^(amd radeon(\(tm\))?( \d+m)? graphics|intel(\(r\))? (uhd|iris|hd)\b.*|intel(\(r\))? graphics.*)$`)

// gameCards: the display adapters that matter for the game. A processor's built-in graphics is left
// out when a separate card is there.
func gameCards(all []platform.MSIFact) []platform.MSIFact {
	var cards, igpu []platform.MSIFact
	for _, m := range all {
		if reIntegrated.MatchString(strings.TrimSpace(m.Device)) && !strings.Contains(strings.ToLower(m.Device), "arc") {
			igpu = append(igpu, m)
		} else {
			cards = append(cards, m)
		}
	}
	if len(cards) == 0 {
		return igpu
	}
	return cards
}

// what the last session measured about Frame Lab's own cost (filled in from 2.0 sessions on)
func lastSessionCost(p Paths) []CheckRow {
	pm := row("pmload", "PresentMon load (last session)", "measured from your next session", "info", "–")
	hr := row("hrcost", "Hitch recorder", "measured from your next session", "info", "–")
	ents, _ := os.ReadDir(p.Sessions)
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() > ents[j].Name() })
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(p.Sessions, e.Name(), "analysis.json"))
		if err != nil {
			continue
		}
		var a struct {
			Kpi struct {
				PmCPUPct  float64 `json:"pm_cpu_pct"`
				PmMemMB   float64 `json:"pm_mem_mb"`
				HrMsFrame float64 `json:"hr_ms_per_frame"`
			} `json:"kpi"`
		}
		if json.Unmarshal(b, &a) != nil {
			continue
		}
		if a.Kpi.PmCPUPct > 0 {
			st, lb := "fine", "Fine"
			if a.Kpi.PmCPUPct > 50 {
				st, lb = "check", "High"
			}
			pm = row("pmload", "PresentMon load (last session)", fmt.Sprintf("%.1f%% of one core, %.0f MB", a.Kpi.PmCPUPct, a.Kpi.PmMemMB), st, lb)
		}
		if a.Kpi.HrMsFrame > 0 {
			hr = row("hrcost", "Hitch recorder", fmt.Sprintf("%.2f ms per frame", a.Kpi.HrMsFrame), "fine", "Fine")
		}
		break
	}
	return []CheckRow{pm, hr}
}

// ---------------------------------------------------------------- game folder

// DLLs other programs drop next to the game to hook into it (the mod frameworks' loaders left out)
var injectorDLLs = []string{"dxgi.dll", "d3d11.dll", "d3d12.dll", "dinput8.dll", "opengl32.dll", "winhttp.dll", "nvngx.dll", "reshade64.dll", "dxgi.asi"}

func injected(game string) []string {
	var out []string
	bin := filepath.Join(game, "bin", "x64")
	for _, n := range injectorDLLs {
		p := filepath.Join(bin, n)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		v, prod := platform.FileVersion(p)
		label := prod
		if label == "" {
			label = "unknown"
		}
		if v != "" {
			label += " " + shortVer(v)
		}
		out = append(out, label+" ("+n+")")
	}
	asis, _ := filepath.Glob(filepath.Join(bin, "plugins", "*.asi"))
	for _, a := range asis {
		n := strings.ToLower(filepath.Base(a))
		if n == "cyber_engine_tweaks.asi" {
			continue
		}
		out = append(out, filepath.Base(a)+" (ASI plugin)")
	}
	if _, err := os.Stat(filepath.Join(game, "red4ext", "plugins", "UltraTool")); err == nil {
		out = append(out, "Ultra+ (UltraTool)")
	}
	return out
}

func dlssFiles(game string) string {
	bin := filepath.Join(game, "bin", "x64")
	var parts []string
	for _, x := range [][2]string{{"nvngx_dlss.dll", "Super Resolution"}, {"nvngx_dlssd.dll", "Ray Reconstruction"}, {"nvngx_dlssg.dll", "Frame Gen"}} {
		if v, _ := platform.FileVersion(filepath.Join(bin, x[0])); v != "" {
			parts = append(parts, x[1]+" "+shortVer(v))
		}
	}
	return strings.Join(parts, " · ")
}

func shortVer(v string) string {
	p := strings.Split(v, ".")
	for len(p) > 2 && p[len(p)-1] == "0" {
		p = p[:len(p)-1]
	}
	return strings.Join(p, ".")
}

type framework struct{ name, file, logDir, logPrefix string }

var frameworks = []framework{
	{"Cyber Engine Tweaks", `bin\x64\plugins\cyber_engine_tweaks.asi`, `bin\x64\plugins\cyber_engine_tweaks`, "cyber_engine_tweaks"},
	{"RED4ext", `red4ext\RED4ext.dll`, `red4ext\logs`, "red4ext"},
	{"redscript", `engine\tools\scc.exe`, `r6\logs`, "redscript"},
	{"ArchiveXL", `red4ext\plugins\ArchiveXL\ArchiveXL.dll`, `red4ext\logs`, "archivexl"},
	{"TweakXL", `red4ext\plugins\TweakXL\TweakXL.dll`, `red4ext\logs`, "tweakxl"},
	{"Codeware", `red4ext\plugins\Codeware\Codeware.dll`, `red4ext\logs`, "codeware"},
}

var reVersionTrouble = regexp.MustCompile(`(?i)(not supported|unsupported|incompatible|not compatible|version mismatch|wrong game version|failed to load)`)

func frameworkRows(game string) []CheckRow {
	var out []CheckRow
	for _, fw := range frameworks {
		p := filepath.Join(game, filepath.FromSlash(strings.ReplaceAll(fw.file, `\`, "/")))
		if _, err := os.Stat(p); err != nil {
			continue
		}
		v, _ := platform.FileVersion(p)
		name := fw.name
		if v != "" {
			name += " " + shortVer(v)
		}
		if line := logTrouble(filepath.Join(game, filepath.FromSlash(strings.ReplaceAll(fw.logDir, `\`, "/"))), fw.logPrefix); line != "" {
			out = append(out, row("fw_"+fw.logPrefix, name, "its last log: "+line, "check", "Check").
				why("The framework's own log reports a problem loading or a game version it doesn't support. After a game update, frameworks need their update too."))
		} else {
			out = append(out, row("fw_"+fw.logPrefix, name, "no version or loading problems in its last log", "fine", "Fine"))
		}
	}
	return out
}

func logTrouble(dir, prefix string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bt time.Time
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasSuffix(n, ".log") || !strings.HasPrefix(n, prefix) {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(bt) {
			best, bt = filepath.Join(dir, e.Name()), fi.ModTime()
		}
	}
	if best == "" {
		return ""
	}
	b, err := os.ReadFile(best)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if reVersionTrouble.MatchString(l) {
			l = strings.TrimSpace(l)
			if len(l) > 140 {
				l = l[:140] + "..."
			}
			return l
		}
	}
	return ""
}

// ---------------------------------------------------------------- debug logging

var reDebugOn = regexp.MustCompile(`(?im)^(\s*(debug|debug_log|verbose)\s*[:=]\s*)(true|1|yes)\s*$`)
var reLevel = regexp.MustCompile(`(?im)^(\s*level\s*=\s*")(debug|trace)(")`)

type debugFile struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // "flag" or "red4ext"
}

func debugFiles(game string) []debugFile {
	var out []debugFile
	for _, pat := range []string{`red4ext/plugins/*/*.yaml`, `red4ext/plugins/*/*.yml`, `red4ext/plugins/*/*.ini`, `red4ext/plugins/*/*.toml`} {
		ms, _ := filepath.Glob(filepath.Join(game, filepath.FromSlash(pat)))
		for _, m := range ms {
			if b, err := os.ReadFile(m); err == nil && len(b) < 1<<20 && reDebugOn.Match(b) {
				out = append(out, debugFile{m, "flag"})
			}
		}
	}
	cfg := filepath.Join(game, "red4ext", "config.ini")
	if b, err := os.ReadFile(cfg); err == nil && reLevel.Match(b) {
		out = append(out, debugFile{cfg, "red4ext"})
	}
	return out
}

func logsSizeMB(game string) float64 {
	var total int64
	for _, d := range []string{"red4ext/logs", "r6/logs", "bin/x64/plugins/cyber_engine_tweaks"} {
		filepath.Walk(filepath.Join(game, filepath.FromSlash(d)), func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.EqualFold(filepath.Ext(p), ".log") {
				total += fi.Size()
			}
			return nil
		})
	}
	return float64(total) / (1 << 20)
}

func debugRow(game string) CheckRow {
	files := debugFiles(game)
	mb := logsSizeMB(game)
	if len(files) == 0 {
		st, lb := "fine", "Fine"
		if mb > 500 {
			st, lb = "info", "Big logs"
		}
		return row("debuglog", "Debug logging", fmt.Sprintf("off · logs %.0f MB", mb), st, lb)
	}
	var names []string
	for _, f := range files {
		if f.Kind == "red4ext" {
			names = append(names, "RED4ext (log level debug)")
		} else {
			names = append(names, filepath.Base(filepath.Dir(f.Path))+" ("+filepath.Base(f.Path)+")")
		}
	}
	return row("debuglog", "Debug logging", fmt.Sprintf("on in %s · logs %.0f MB", strings.Join(names, ", "), mb), "found", "Found").act("debug_off", "Turn off").
		why("Debug logging writes a line for things the game does all the time, which costs time on every frame that logs and fills the drive. It is meant for hunting one problem, then switching off.")
}

// ---------------------------------------------------------------- UserSettings.json

// readUserSettings flattens the game's settings file to "/group/name" -> value
func readUserSettings() map[string]any {
	out := map[string]any{}
	b, err := os.ReadFile(platform.UserSettingsPath())
	if err != nil {
		return out
	}
	var us struct {
		Data []struct {
			Group   string `json:"group_name"`
			Options []struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"options"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &us) != nil {
		return out
	}
	for _, g := range us.Data {
		for _, o := range g.Options {
			out[g.Group+"/"+o.Name] = o.Value
		}
	}
	return out
}

// ---------------------------------------------------------------- fixes (kept for Undo)

func (s *Server) checkAction(i int, id string) (string, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return "", errors.New("no game build picked yet")
	}
	game := cfg.Builds[i].Path
	switch id {
	case "pawnio_get":
		platform.OpenURL("https://pawnio.eu/")
		return "pawnio.eu opened in your browser. After installing PawnIO, run Checks again.", nil
	case "frametype_off":
		cfg.TrackFrameType = false
		if err := s.Settings.Set(cfg); err != nil {
			return "", err
		}
		return "Frame-type tracking is off (Settings can switch it back on).", nil
	case "fth_clear":
		saved, err := platform.ClearFTH()
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(saved)
		undoAdd(s.Paths.Data, UndoEntry{ID: "fth", What: "Fault Tolerant Heap entries for the game", Kind: "fth", Data: b})
		return "The game is off the Fault Tolerant Heap list.", nil
	case "debug_off":
		var changed []UndoFile
		for _, f := range debugFiles(game) {
			b, err := os.ReadFile(f.Path)
			if err != nil {
				continue
			}
			nb := b
			if f.Kind == "red4ext" {
				nb = reLevel.ReplaceAll(b, []byte(`${1}info${3}`))
			} else {
				nb = reDebugOn.ReplaceAllFunc(b, func(m []byte) []byte {
					v := reDebugOn.FindSubmatch(m)
					repl := "false"
					if string(v[3]) == "1" {
						repl = "0"
					} else if strings.EqualFold(string(v[3]), "yes") {
						repl = "no"
					}
					return append(append([]byte{}, v[1]...), repl...)
				})
			}
			if err := os.WriteFile(f.Path, nb, 0o644); err != nil {
				return "", err
			}
			changed = append(changed, UndoFile{Path: f.Path, Was: b})
		}
		if len(changed) > 0 {
			bb, _ := json.Marshal(changed)
			undoAdd(s.Paths.Data, UndoEntry{ID: "debuglog:" + game, What: "debug logging switches", Kind: "files", Data: bb})
		}
		if len(changed) == 0 {
			return "Debug logging was already off.", nil
		}
		var where []string
		for _, f := range changed {
			if filepath.Base(f.Path) == "config.ini" {
				where = append(where, "RED4ext")
			} else {
				where = append(where, filepath.Base(filepath.Dir(f.Path)))
			}
		}
		return "Debug logging is off in " + strings.Join(where, ", ") + ". It takes effect at the next game start.", nil
	case "us_writable":
		p := platform.UserSettingsPath()
		fi, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		if err := os.Chmod(p, fi.Mode()|0o200); err != nil {
			return "", err
		}
		undoAdd(s.Paths.Data, UndoEntry{ID: "usro", What: "UserSettings.json read-only", Kind: "readonly", Data: mustJSON(p)})
		return "UserSettings.json is writable again.", nil
	case "bcd_clear", "cpusets_clear":
		was, err := clearTweak(id)
		if was != nil {
			undoAdd(s.Paths.Data, UndoEntry{ID: id, What: map[string]string{"bcd_clear": "boot timer options (bcdedit)", "cpusets_clear": "reserved CPU sets"}[id], Kind: id, Data: was})
		}
		if err != nil {
			return "", err
		}
		return "Done. Restart Windows for it to take effect.", nil
	}
	return "", errors.New("unknown fix " + id)
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func clearTweak(id string) (json.RawMessage, error) {
	switch id {
	case "bcd_clear":
		was, err := platform.ClearBootTimer()
		if len(was) > 0 {
			return mustJSON(was), err // what was removed before a failure still goes in the journal
		}
		return nil, err
	case "cpusets_clear":
		b, err := platform.ClearReservedCpuSets()
		if err != nil {
			return nil, err
		}
		return mustJSON(b), nil
	}
	return nil, errors.New("unknown fix " + id)
}

// checksForget drops the cached report so the next look reads everything again
func checksForget() {
	checksMu.Lock()
	checksCache = map[string]*ChecksReport{}
	checksMu.Unlock()
}
