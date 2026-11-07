package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Advice: what the numbers of one session suggest trying, in plain words. Worked out from the
// report itself (no guessing about the PC), so every line points at something that was measured.
type Advice struct {
	Level  string   `json:"level"` // "crash", "hw" (needs a look), "fix" (caused stutters), "try" (worth a test), "info", "good"
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Items  []string `json:"items,omitempty"`
}

// mods that aren't a mod you can change
func notAMod(name string) bool {
	switch name {
	case "game scripts", "CET itself", "scripts without a source file":
		return true
	}
	return false
}

// cardName: how advice names the game's graphics card. Sessions before 1.1 were NVIDIA only.
func cardName(m *Meta) string {
	if m == nil || m.GpuVendor == "" || m.GpuVendor == "nvidia" {
		return "the NVIDIA card"
	}
	return "the game's graphics card"
}

// capPlace: where a frame cap is set on this card.
func capPlace(m *Meta) string {
	switch {
	case m == nil || m.GpuVendor == "" || m.GpuVendor == "nvidia":
		return "in the NVIDIA app or the game"
	case m.GpuVendor == "amd":
		return "in AMD Software or the game"
	}
	return "in the game"
}

func Advise(r *Result) []Advice {
	out := []Advice{}
	k := r.KPI
	add := func(level, title, detail string, items ...string) {
		out = append(out, Advice{Level: level, Title: title, Detail: detail, Items: items})
	}
	mins := math.Max(k.DurationS/60, 0.1)
	count := func(f func(s *Spike) bool) (n int, worst float64) {
		for i := range r.Spikes {
			if f(&r.Spikes[i]) {
				n++
				worst = math.Max(worst, r.Spikes[i].Ms)
			}
		}
		return
	}
	gb := func(mb float64) string { return fmt.Sprintf("%.1f GB", mb/1024) }

	// ---- the session ended in a crash (2.0)
	if c := r.Crash; c != nil {
		title := "The game crashed"
		if c.Hung {
			title = fmt.Sprintf("The game crashed: the graphics card hung at %s", c.Clock)
		} else if c.Clock != "" {
			title = fmt.Sprintf("The game crashed at %s", c.Clock)
		}
		det := ""
		if c.Hung {
			det = fmt.Sprintf("The game stopped sending frames and the card stayed busy at low power for %.0f s: it had stopped responding. Windows then reset it and the game closed. ", c.HangS)
		}
		if c.Summary != "" {
			det += "The crash report says: " + c.Summary
		} else if c.Report == "" {
			det += "No Advanced Crash Reporter report was found for this time (that mod writes one with what the card was drawing)."
		}
		var items []string
		if c.Error != "" {
			items = append(items, "Error: "+c.Error)
		}
		if c.Device != "" {
			items = append(items, "Card state: "+c.Device)
		}
		if c.Pass != "" {
			items = append(items, "Drawing: "+c.Pass)
		}
		if c.Shader != "" {
			items = append(items, "Shader: "+c.Shader)
		}
		if c.Place != "" {
			items = append(items, "Place in the world: "+c.Place)
		}
		if c.Report != "" {
			items = append(items, "Report: "+c.Report+" (AdvancedCrashReporter\\reports in the game folder)")
		}
		add("crash", title, det, items...)
	}

	// ---- Windows System log (2.0)
	if len(r.SysEvents) > 0 {
		var drv, whea, power []string
		for _, e := range r.SysEvents {
			line := e.Time.Format("15:04:05") + "  " + e.Source + " " + e.ID
			if e.Text != "" {
				t := e.Text
				if len(t) > 160 {
					t = t[:160] + "..."
				}
				line += ": " + t
			}
			switch {
			case strings.Contains(e.Source, "WHEA"):
				whea = append(whea, line)
			case e.ID == "41":
				power = append(power, line)
			default:
				drv = append(drv, line)
			}
		}
		if len(whea) > 0 {
			add("hw", fmt.Sprintf("Windows logged %d hardware error%s during the session (WHEA)", len(whea), plural(len(whea))),
				"WHEA errors come from the hardware itself: the CPU, RAM, the PCIe link or the graphics card. Corrected ones are harmless now and then; many of them, or with crashes, point at an overclock / undervolt, EXPO / XMP memory settings or a loose card. Test without the overclock first.", whea...)
		}
		if len(drv) > 0 {
			add("crash", "Windows reset the graphics driver", "Display 4101 means the driver stopped responding and Windows restarted it (a TDR). The game usually crashes with it.", drv...)
		}
		if len(power) > 0 {
			add("crash", "The PC restarted without shutting down (Kernel-Power 41)", "A full system crash or power loss. With a game running that is usually the power supply, the graphics card's power connectors, or an unstable overclock.", power...)
		}
	}

	// ---- CPU temperature (2.0, when HWiNFO or PawnIO gave a reading)
	if t := r.KPI.CpuTempMax; t >= 95 {
		add("hw", fmt.Sprintf("The CPU reached %.0f °C", t),
			"That is at or near where the CPU slows itself down to protect itself, which shows up as uneven frame times. Check the cooler's mounting and paste, the fan curve, and case airflow; an undervolt (Curve Optimizer on Ryzen) helps too.")
	} else if t >= 88 {
		add("try", fmt.Sprintf("The CPU ran hot: %.0f °C at the most", t),
			"Not throttling yet, but close. Worth checking the cooler and fan curve before a long session in summer.")
	}

	// ---- load times (2.0)
	if l := r.Loads; l != nil {
		var items []string
		if l.StartToMenuS > 0 {
			items = append(items, fmt.Sprintf("Game start to the main menu: %.0f s", l.StartToMenuS))
		}
		if l.ScriptsS > 0 {
			items = append(items, fmt.Sprintf("Loading the scripts (REDscript): %.1f s", l.ScriptsS))
		}
		if l.FirstLoadS > 0 {
			items = append(items, fmt.Sprintf("Loading the save: %.0f s", l.FirstLoadS))
		}
		if n := len(l.Screens); n > 0 {
			longest := 0.0
			for _, v := range l.Screens {
				longest = math.Max(longest, v)
			}
			items = append(items, fmt.Sprintf("%d loading screen%s, longest %.0f s", n, plural(n), longest))
		}
		if len(items) > 0 {
			add("info", "Load times", "From the session markers and RED4ext's log. A slow save load usually comes from many mods, a slow drive or little free RAM.", items...)
		}
	}

	// ---- F10: "I felt a stutter" (2.0)
	if len(r.Marks) > 0 {
		var items []string
		for _, m := range r.Marks {
			items = append(items, m.Clock+": "+m.Cause)
		}
		add("info", fmt.Sprintf("You pressed F10 %d time%s (\"I felt a stutter\")", len(r.Marks), plural(len(r.Marks))),
			"What happened in the 2 seconds before each press. The marks are the amber lines on the chart.", items...)
	}

	// ---- screen resets (the game re-applying full screen / rebuilding its screen buffers)
	var hurt []ResetMark // resets that took memory or started crawling frames
	for _, m := range r.Timeline.Resets {
		if m.Playing && !m.Release && (m.SlowS >= 1 || m.VramAfter > m.VramBefore+500 || (m.RamFreeMin > 0 && m.RamFreeMin < 1000)) {
			hurt = append(hurt, m)
		}
	}
	if k.Resets > 0 {
		level := "info"
		title := fmt.Sprintf("The game reset its screen %d time%s while you played, without trouble", k.Resets, plural(k.Resets))
		if len(hurt) > 0 {
			level = "fix"
			grow, slow := 0.0, 0.0
			for _, m := range hurt {
				grow = math.Max(grow, m.VramAfter-m.VramBefore)
				slow += m.SlowS
			}
			title = fmt.Sprintf("The game reset its screen %d time%s while you played", k.Resets, plural(k.Resets))
			if grow >= 500 {
				title += fmt.Sprintf(": %d time%s its VRAM grew by up to %s", len(hurt), plural(len(hurt)), gb(grow))
			}
			if slow >= 2 {
				title += fmt.Sprintf(", %.0f s of frames crawling in all", slow)
			}
		}
		var items []string
		quiet := 0
		for _, m := range r.Timeline.Resets {
			if !m.Playing || m.Release {
				continue
			}
			var fx []string
			if m.VramBefore > 0 && m.VramAfter > m.VramBefore+300 {
				fx = append(fx, fmt.Sprintf("VRAM %s → %s", gb(m.VramBefore), gb(m.VramAfter)))
			}
			if m.SharedAfter > m.SharedBefore+300 {
				fx = append(fx, fmt.Sprintf("%s of GPU memory in system RAM", gb(m.SharedAfter)))
			}
			if m.RamFreeMin > 0 && m.RamFreeMin < 1500 {
				fx = append(fx, fmt.Sprintf("free RAM down to %.0f MB", m.RamFreeMin))
			}
			if m.SlowS >= 1 {
				fx = append(fx, fmt.Sprintf("%.0f s of crawling frames", m.SlowS))
			}
			if len(fx) == 0 {
				quiet++
				continue
			}
			if len(items) >= 8 {
				continue
			}
			it := m.Clock
			if m.EndedBy != "" {
				it += " to " + m.EndedBy
			}
			items = append(items, it+": "+strings.Join(fx, ", "))
		}
		if quiet > 0 && len(items) > 0 {
			items = append(items, fmt.Sprintf("%d more without a visible effect", quiet))
		}
		src := r.Timeline.Resets[0].Source
		detail := "Each time " + r.Timeline.Resets[0].Text + " (" + src + "'s log). " +
			"When the graphics card is already full, the extra goes into system RAM and frames crawl until it is released, here at the next reset. " +
			"In the game's Fullscreen window mode this happens when the window loses and gets back focus (Alt+Tab, an overlay, a notification or another window popping up) or the display changes. " +
			"Window Mode: Windowed Borderless (Settings > Video) doesn't re-apply full screen. Clock times are the PC's, so they can be matched with what you did:"
		if len(items) == 0 {
			detail = "Each time the game rebuilt its screen buffers (" + src + "'s log); this time it didn't take extra memory or slow the frames."
		}
		add(level, title, detail, items...)
	}

	// ---- system RAM
	if k.RamFreeMinMB > 0 && (k.RamFreeMinMB < 1024 || (k.CommitLimitMB > 0 && k.CommitPeakMB > 0.9*k.CommitLimitMB)) {
		ramN, ramWorst := count(func(s *Spike) bool { return s.RamFreeMB > 0 && s.RamFreeMB < 500 })
		level := "try"
		if ramN > 0 || (k.CommitLimitMB > 0 && k.CommitPeakMB > 0.95*k.CommitLimitMB) {
			level = "fix"
		}
		title := fmt.Sprintf("System RAM nearly ran out: %.0f MB free at the lowest", k.RamFreeMinMB)
		if k.CommitLimitMB > 0 && k.CommitPeakMB > 0 {
			title += fmt.Sprintf(" (memory promised to programs: %s of the %s limit)", gb(k.CommitPeakMB), gb(k.CommitLimitMB))
		}
		detail := fmt.Sprintf("Normally %s was free while you played. ", gb(k.RamFreeTypMB))
		if ramN > 0 {
			detail += fmt.Sprintf("%d stutter%s (worst %.0f ms) came with less than 500 MB free. ", ramN, plural(ramN), ramWorst)
		}
		if len(hurt) > 0 {
			detail += "Here it happened during the screen resets above: fixing those fixes this. "
		}
		detail += "When RAM runs out Windows moves memory to the page file and everything waits; at the limit, memory requests fail and the game can crash. " +
			"Closing browsers and launchers while playing helps; a bigger page file (a Windows setting) raises the limit."
		add(level, title, detail)
	}

	// ---- VRAM
	vramN, vramWorst := count(func(s *Spike) bool { return s.Group == "vram" && !s.AfterReset })
	peak := k.VramPeakMB
	if k.VramPlayPeakMB > 0 {
		peak = k.VramPlayPeakMB
	}
	ratio := 0.0
	if k.VramBudgetMB > 0 {
		ratio = peak / k.VramBudgetMB
	}
	if vramN > 0 || ratio >= 0.95 {
		level, title := "try", fmt.Sprintf("VRAM is nearly full: %s of the %s the game may use (%.0f%%)", gb(peak), gb(k.VramBudgetMB), ratio*100)
		if vramN > 0 {
			level = "fix"
			title = fmt.Sprintf("VRAM caused %d stutter%s (worst %.0f ms): %s of %s in use", vramN, plural(vramN), vramWorst, gb(peak), gb(k.VramBudgetMB))
		}
		if k.Resets > 0 && peak < k.VramPeakMB-300 {
			title += fmt.Sprintf(" in normal play (%s during the screen resets)", gb(k.VramPeakMB))
		}
		var big []ModRow
		for _, m := range r.Mods {
			if m.MB >= 1 && !notAMod(m.Name) {
				big = append(big, m)
			}
		}
		sort.Slice(big, func(i, j int) bool { return big[i].MB > big[j].MB })
		var items []string
		for i := 0; i < len(big) && i < 6; i++ {
			items = append(items, fmt.Sprintf("%s: %.0f MB loaded (%d files)", big[i].Name, big[i].MB, big[i].Files))
		}
		detail := "When VRAM is full the game has to make room before it can show new things, and the frame waits. " +
			"What frees the most: lower-resolution versions (2K instead of 4K) of the texture mods that loaded the most, " +
			"or a lower texture setting in the game. Mods that loaded the most data this session:"
		if len(items) == 0 {
			detail = "When VRAM is full the game has to make room before it can show new things, and the frame waits. Lower-resolution texture mods or a lower texture setting free the most."
		}
		add(level, title, detail, items...)
	}

	// ---- other programs holding graphics memory on the game's card
	if ga := r.GpuApps; ga != nil && ga.Recorded && ga.OthersTypMB >= 300 {
		level := "info"
		if (vramN > 0 || ratio >= 0.9) && ga.MovableTypMB >= 150 {
			level = "try"
		}
		var items, shared []string
		for _, a := range ga.Apps {
			if a.Shared {
				shared = append(shared, a.Name)
				continue
			}
			if len(items) == 7 {
				continue
			}
			if a.TypMB < 30 && a.MaxMB < 100 {
				continue
			}
			what := ""
			switch a.Class {
			case "suggest":
				what = "can run on the integrated graphics (" + a.Reason + ")"
			case "keep", "game":
				what = "keep on " + cardName(r.Meta) + ": " + a.Reason
			case "windows":
				what = a.Reason + ", stays"
			case "store":
				what = a.Reason
			default:
				what = "not on Frame Lab's list"
			}
			use := mbOrGb(a.TypMB)
			if a.MaxMB >= a.TypMB+100 {
				use += " (up to " + mbOrGb(a.MaxMB) + ")"
			}
			items = append(items, fmt.Sprintf("%s: %s, %s", a.Name, use, what))
		}
		if len(shared) > 0 {
			items = append(items, strings.Join(shared, ", ")+": hooked into the game (overlay / desktop), so Windows counts the game's memory for them too; their real share is in the total")
		}
		detail := "Everything else on the card (programs, Windows, the driver) comes out of the same memory the game uses, so the game gets that much less before it has to spill into system RAM. " +
			"Programs that don't need " + cardName(r.Meta) + " (browsers, Discord, launchers) can run on the processor's integrated graphics instead: " +
			"VRAM tab, \"Programs on the graphics card\" (it changes the same Windows setting as Settings > Display > Graphics, with an exact undo), or close them before playing."
		if ga.MovableTypMB < 150 {
			detail = "Most of it is Windows itself or programs that belong on " + cardName(r.Meta) + ", so moving programs would free little here."
		}
		add(level, fmt.Sprintf("Everything else on the game's card held %s of graphics memory while you played", mbOrGb(ga.OthersTypMB)), detail, items...)
	}

	// ---- long freezes nothing in the game explains
	var disk, core, vr, dpc, shared, pages float64
	unN, unWorst := count(func(s *Spike) bool {
		if s.Group == "unexplained" && s.Ms >= 150 {
			dpc = math.Max(dpc, s.DpcPct)
			shared = math.Max(shared, s.SharedMB)
			pages = math.Max(pages, s.PagesSec)
			disk = math.Max(disk, s.DiskMBs)
			core = math.Max(core, s.MaxCore)
			if s.BudgetMB > 0 {
				vr = math.Max(vr, s.VramMB/s.BudgetMB)
			}
			return true
		}
		return false
	})
	// the Windows event recorder times every driver run; when none was long, the sampled DPC share
	// (2 s averages) doesn't make drivers the cause
	etwClean := false
	if w := r.Windows; w != nil && w.Recorded && len(w.Drivers) > 0 {
		longest := 0.0
		for _, x := range w.Drivers {
			longest = math.Max(longest, x.MaxMs)
		}
		etwClean = longest < 5
	}
	if unN > 0 && dpc >= 5 && !etwClean {
		add("fix", fmt.Sprintf("Drivers held the CPU during %d long freeze%s (worst %.0f ms): up to %.0f%% of the CPU in driver interrupts", unN, plural(unN), unWorst, dpc),
			"Driver interrupt time (DPC) that high stalls everything. Usual suspects: network (Wi-Fi especially), audio, USB devices, RGB/monitoring tools. LatencyMon names the driver; updating or disabling it for a test session tells you.")
	} else if unN > 0 && shared >= 300 {
		// 2.0.1: the game often keeps that memory in RAM the whole session, not just at the freeze
		var sh []float64
		for _, v := range r.Timeline.SharedMB {
			if v > 0 {
				sh = append(sh, v)
			}
		}
		typ := 0.0
		if len(sh) > 0 {
			sort.Float64s(sh)
			typ = pctl(sh, 50)
		}
		title := fmt.Sprintf("VRAM spilled into system RAM during %d long freeze%s (worst %.0f ms): %.0f MB of the game's GPU memory in RAM", unN, plural(unN), unWorst, shared)
		if typ >= 0.8*shared {
			title = fmt.Sprintf("VRAM was full: %.0f MB of the game's GPU memory stayed in system RAM while you played, with %d long freeze%s (worst %.0f ms)", typ, unN, plural(unN), unWorst)
		}
		add("fix", title,
			"When VRAM runs out Windows keeps part of the game's GPU memory in normal RAM and moves it back and forth, which freezes frames. Freeing VRAM (see the VRAM item) is the fix.")
	} else if unN > 0 {
		next := "Frame Lab's Windows event recorder (Settings) names which. "
		if r.Windows != nil && r.Windows.Recorded {
			next = "The Windows events don't show a driver or paging at those moments either, so it is most likely memory moving between VRAM and RAM, or work inside the game nothing here can see. "
		}
		add("try", fmt.Sprintf("%d long freeze%s (worst %.0f ms) with nothing inside the game to show for it", unN, plural(unN), unWorst),
			fmt.Sprintf("At those moments: disk reads up to %.0f MB/s, the busiest CPU core at %.0f%%, VRAM at %.0f%% of the budget. "+
				"That points at Windows rather than a mod: memory being moved between VRAM and RAM, a driver holding the CPU, or the disk. "+
				"%sWorth a test in Experiments: a Defender exclusion for the game folder, and clearing standby memory before playing.",
				disk, core, vr*100, next))
	}

	// ---- Windows event recorder
	if w := r.Windows; w != nil && w.Recorded {
		if w.DriverSpk > 0 {
			var items []string
			for _, d := range w.Drivers {
				if d.Spikes == 0 {
					continue
				}
				what := ""
				if d.What != "" {
					what = " (" + d.What + ")"
				}
				items = append(items, fmt.Sprintf("%s%s: %d stutter%s, longest single run %.1f ms, %.2f ms per second while playing", d.Name, what, d.Spikes, plural(d.Spikes), d.MaxMs, d.MsPerS))
			}
			add("fix", fmt.Sprintf("A driver held the CPU during %d stutter%s", w.DriverSpk, plural(w.DriverSpk)),
				"Drivers run their interrupts and DPCs ahead of every program on that CPU core, so a long one stalls whatever the game had running there. "+
					"Update (or roll back) that driver. For network, audio, USB or Bluetooth devices, unplugging or disabling the device for one session shows whether it is the one.", items...)
		}
		if w.PagingSpk > 0 {
			mb := 0.0
			for _, p := range w.Paging {
				if strings.EqualFold(p.Name, gameExe) {
					mb = p.MB
				}
			}
			add("fix", fmt.Sprintf("The game had to read its own memory back from disk during %d stutter%s (%.0f MB in the session)", w.PagingSpk, plural(w.PagingSpk), mb),
				"Windows had moved part of the game's memory out of RAM (to the page file), usually because RAM was short, and the game waited each time it needed it again. More free RAM fixes it: close browsers and launchers, or see the RAM item.")
		}
		if w.DriverSpk == 0 && w.PagingSpk == 0 && len(w.Drivers) > 0 {
			d := w.Drivers[0]
			longest := 0.0
			for _, x := range w.Drivers {
				longest = math.Max(longest, x.MaxMs)
			}
			level, title := "good", fmt.Sprintf("Drivers and paging: no stutter from them (longest single driver run %.1f ms)", longest)
			if longest >= 2 {
				level = "info"
			}
			if longest >= 50 {
				// a run that long is a freeze of its own; at the end of a crashed session it is the hang
				level = "try"
				title = fmt.Sprintf("A driver ran for %.0f ms in one go, outside the stutters Frame Lab lists", longest)
				if r.Crash != nil && r.Crash.Hung {
					title = fmt.Sprintf("A driver ran for %.0f ms in one go: the graphics driver when the card hung", longest)
				}
			}
			what := ""
			if d.What != "" {
				what = " (" + d.What + ")"
			}
			add(level, title, fmt.Sprintf("Busiest driver: %s%s, %.2f ms per second while playing.", d.Name, what, d.MsPerS))
		}
	}

	// ---- script mods that cost the most
	type sc struct {
		name string
		ms   float64
		kind string
	}
	var costly []sc
	for _, m := range r.Mods {
		if notAMod(m.Name) {
			continue
		}
		if m.RedMsS >= 1.0 {
			costly = append(costly, sc{m.Name, m.RedMsS, "REDscript"})
		}
		if m.CetMsS >= 1.0 {
			costly = append(costly, sc{m.Name, m.CetMsS, "CET"})
		}
	}
	sort.Slice(costly, func(i, j int) bool { return costly[i].ms > costly[j].ms })
	if len(costly) > 0 {
		var items []string
		top := 0.0
		for i := 0; i < len(costly) && i < 6; i++ {
			c := costly[i]
			top = math.Max(top, c.ms)
			perFrame := ""
			if k.AvgFps > 0 {
				perFrame = fmt.Sprintf(", %.2f ms per frame at %.0f fps", c.ms/k.AvgFps, k.AvgFps)
			}
			items = append(items, fmt.Sprintf("%s (%s): %.1f ms per second%s", c.name, c.kind, c.ms, perFrame))
		}
		level := "info"
		if top >= 3 {
			level = "try"
		}
		add(level, "Script mods that cost the most in your F11 part",
			"This is steady work on every frame, not stutter. A mod that runs where it has nothing to do (far from its place, or every frame instead of now and then) is the one to look at, or to switch off for one test session and compare.",
			items...)
	}

	// ---- shaders
	shN, shWorst := count(func(s *Spike) bool { return s.Group == "shader" })
	if r.Shaders.Recorded {
		if shN > 0 {
			add("fix", fmt.Sprintf("Shader builds caused %d stutter%s (worst %.0f ms)", shN, plural(shN), shWorst),
				"The game built a shader while you played. They are kept in the shader cache, so the same places get smoother. Don't clear the cache; after a driver update the first minutes stutter until it is rebuilt.")
		} else if r.Shaders.Total > 0 {
			add("good", "Shader builds: no stutters from them",
				fmt.Sprintf("%d were built, all off the main thread. Clearing the shader cache or pre-building shaders wouldn't help here; clearing it would make the next session worse.", r.Shaders.Total))
		}
	}

	// ---- disk
	dN, dMax := 0, 0.0
	for _, s := range r.Spikes {
		if s.DiskMBs >= 200 {
			dN++
			dMax = math.Max(dMax, s.DiskMBs)
		}
	}
	if dN > 0 {
		add("try", fmt.Sprintf("Heavy disk reads during %d stutter%s (up to %.0f MB/s)", dN, plural(dN), dMax),
			"The game was streaming from disk at the time. Keep the game on the fastest NVMe drive; a Defender exclusion for the game folder (Experiments) stops Windows scanning those files as they are read.")
	}

	// ---- GPU
	if k.ThrottleBad {
		add("fix", "The GPU slowed itself down: "+k.ThrottleText, fmt.Sprintf("Hottest: %.0f °C. Check the case airflow and the GPU fans.", k.GpuTempMax))
	} else if k.ThrottleText != "" {
		add("info", "The GPU ran at its power limit", fmt.Sprintf("Normal under full load (hottest %.0f °C). More fps from here means lighter settings, not tweaks.", k.GpuTempMax))
	}
	if k.GpuBoundPct < 0 {
		for _, w := range r.Warnings {
			if strings.Contains(w, "frame generation") {
				add("info", "GPU time isn't measured with frame generation on",
					"PresentMon can't tell the generated frames apart, so Frame Lab can't say whether the GPU or the CPU is the limit. One test session with frame generation off answers that.")
				break
			}
		}
	}

	// ---- how evenly frames reached the screen
	if pc := r.Pacing; pc != nil && pc.Samples >= 200 {
		switch {
		case pc.VRR:
			add("info", fmt.Sprintf("Frames reach the screen when they are ready (VRR or no v-sync): %.0f shown per second", pc.ShownFps),
				"The screen doesn't make frames wait for a fixed refresh, so a frame rate under the refresh rate doesn't cause judder.")
		case pc.HeldPct >= 5:
			own := ""
			if k.AvgFps > 0 {
				if mult := math.Round(pc.ShownFps / k.AvgFps); mult >= 2 {
					own = fmt.Sprintf(" (with frame generation x%.0f that is %.0f fps of the game's own)", mult, math.Ceil(pc.RefreshHz/mult))
				}
			}
			add("try", fmt.Sprintf("Judder: %.1f%% of frames stayed on screen for two refreshes (%.0f shown per second on a fixed %.0f Hz screen)", pc.HeldPct, pc.ShownFps, pc.RefreshHz),
				fmt.Sprintf("The screen refreshes at a fixed %.0f Hz and each frame waits for the next refresh. With fewer new frames than refreshes some stay up twice, which looks uneven even when no frame was slow. "+
					"It goes away when the frames shown reach %.0f a second: a higher frame generation multiplier, settings that raise the game's own frame rate%s, or VRR (G-SYNC / FreeSync) where it suits the screen and recording.",
					pc.RefreshHz, pc.RefreshHz, own))
		case pc.RefreshHz > 0:
			add("good", fmt.Sprintf("Even frame pacing: %.0f frames shown per second on a fixed %.0f Hz screen, %.1f%% held for two refreshes", pc.ShownFps, pc.RefreshHz, pc.HeldPct), "")
		}
	}

	// ---- pacing
	if k.AvgFps > 0 && k.Low1Fps > 0 && k.Low1Fps/k.AvgFps < 0.7 {
		add("try", fmt.Sprintf("Uneven frame pacing: 1%% low %.0f fps against %.0f fps average", k.Low1Fps, k.AvgFps),
			"Frames come at uneven intervals even without big spikes. A frame cap a little under your usual fps ("+capPlace(r.Meta)+") usually evens it out.")
	}

	// ---- overall
	if k.Spikes == 0 {
		add("good", "No stutters over the spike limit", fmt.Sprintf("%.0f min of play without a frame over %.0f ms.", mins, k.SpikeMs))
	} else if float64(k.Spikes)/mins < 0.5 {
		add("good", fmt.Sprintf("Few stutters: %d in %.0f min of play", k.Spikes, mins), "")
	}

	order := map[string]int{"crash": -2, "hw": -1, "fix": 0, "try": 1, "info": 2, "good": 3}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Level] < order[out[j].Level] })
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func mbOrGb(mb float64) string {
	if mb >= 1000 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}
