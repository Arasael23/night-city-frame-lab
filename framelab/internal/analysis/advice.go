package analysis

import (
	"fmt"
	"math"
	"regexp"
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
	Do     *Do      `json:"do,omitempty"` // what to do about it ("Do this next", 2.5)
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

// EditionName: "2.5E" or "2.5" (the app's edition, see app/edition.go)
func EditionName() string {
	if Experimental {
		return "2.5E"
	}
	return "2.5"
}

// Readvise: a saved result with the advice of this edition (2.5 and 2.5E can share a data folder).
func Readvise(r *Result) {
	r.Advice = Advise(r)
	r.Verdict = VerdictLine(r)
	r.Edition = EditionName()
}

// Advise: the report's items. Frame Lab 2.5 gets them without the Experiments / Optimise links (edition.go).
func Advise(r *Result) []Advice {
	out := advise(r)
	if !Experimental {
		for i := range out {
			out[i].Detail = plainText(out[i].Detail)
			if d := out[i].Do; d != nil {
				if strings.Contains(d.Text, "in Experiments") {
					d.Text, d.Title, d.Steps = diyDo["standbyauto"].text, diyDo["standbyauto"].title, diyDo["standbyauto"].steps
				}
				if d.Go == "optimise" || strings.HasPrefix(d.Go, "exp:") {
					d.Button, d.Go, d.E = "", "", true
					d.Text = strings.Replace(d.Text, "Slow down ", "Look in the settings of ", 1)
					d.Text = strings.Replace(d.Text, " in Optimise if it is listed there", " for an update rate or a lighter mode", 1)
				}
			}
		}
	}
	return out
}

var reExpParen = regexp.MustCompile(` \(Experiments[^)]*\)`)

// plainText: advice text without the pointers to Experiments and Optimise (Frame Lab 2.5)
func plainText(s string) string {
	s = strings.ReplaceAll(s, "Worth a test in Experiments: ", "Worth a test: ")
	s = reExpParen.ReplaceAllString(s, "")
	return s
}

func advise(r *Result) []Advice {
	out := []Advice{}
	k := r.KPI
	add := func(level, title, detail string, items ...string) {
		out = append(out, Advice{Level: level, Title: title, Detail: detail, Items: items})
	}
	// do: what to do about the item just added
	do := func(d *Do) {
		if d != nil && len(out) > 0 {
			out[len(out)-1].Do = d
		}
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
		if c.Report != "" {
			do(&Do{Text: "Open the crash report and look at the mod, shader or place it names.", Effect: "The same name in two crashes is the thing to update or remove first."})
		} else {
			do(&Do{Text: "Install Advanced Crash Reporter, so the next crash says what the card was drawing.", Button: "Open its Nexus page", Go: "nexus:34403"})
		}
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
			do(&Do{Text: "Record one session without the overclock or undervolt, and with EXPO / XMP memory settings off."})
		}
		if len(drv) > 0 {
			add("crash", "Windows reset the graphics driver", "Display 4101 means the driver stopped responding and Windows restarted it (a TDR). The game usually crashes with it.", drv...)
			do(&Do{Text: "Update the graphics driver, or reinstall it cleanly, then record again."})
		}
		if len(power) > 0 {
			add("crash", "The PC restarted without shutting down (Kernel-Power 41)", "A full system crash or power loss. With a game running that is usually the power supply, the graphics card's power connectors, or an unstable overclock.", power...)
			do(&Do{Text: "Check the power supply and the graphics card's power cables, and remove any overclock."})
		}
	}

	// ---- CPU temperature (2.0, when HWiNFO or PawnIO gave a reading)
	if t := r.KPI.CpuTempMax; t >= 95 {
		add("hw", fmt.Sprintf("The CPU reached %.0f °C", t),
			"That is at or near where the CPU slows itself down to protect itself, which shows up as uneven frame times. Check the cooler's mounting and paste, the fan curve, and case airflow; an undervolt (Curve Optimizer on Ryzen) helps too.")
		do(&Do{Text: "Check the CPU cooler: mounting, thermal paste and fan curve.", Metric: "cpu_temp"})
	} else if t >= 88 {
		add("try", fmt.Sprintf("The CPU ran hot: %.0f °C at the most", t),
			"Not throttling yet, but close. Worth checking the cooler and fan curve before a long session in summer.")
		do(&Do{Text: "Check the CPU cooler and its fan curve.", Metric: "cpu_temp"})
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
		fk, _ := r.Meta.Keys()
		add("info", fmt.Sprintf("You pressed %s %d time%s (\"I felt a stutter\")", fk, len(r.Marks), plural(len(r.Marks))),
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
		if len(hurt) > 0 {
			do(&Do{Text: "Switch Window Mode to Windowed Borderless (game Settings > Video).", Effect: "The game stops re-applying full screen each time it loses focus.", Metric: "reset_slow_s"})
		}
	}

	// ---- system RAM
	if k.RamFreeMinMB > 0 && (k.RamFreeMinMB < 1024 || (k.CommitLimitMB > 0 && k.CommitPeakMB > 0.9*k.CommitLimitMB)) {
		ramN, ramWorst := count(func(s *Spike) bool { return s.Group != "crash" && s.RamFreeMB > 0 && s.RamFreeMB < 500 })
		level := "try"
		if ramN > 0 || (k.CommitLimitMB > 0 && k.CommitPeakMB > 0.95*k.CommitLimitMB) {
			level = "fix"
		}
		title := fmt.Sprintf("System RAM nearly ran out: %.0f MB free at the lowest", k.RamFreeMinMB)
		if k.CommitLimitMB > 0 && k.CommitPeakMB > 0 {
			title += fmt.Sprintf(" (memory promised to programs: %s of the %s limit)", gb(k.CommitPeakMB), gb(k.CommitLimitMB))
		}
		detail := fmt.Sprintf("Normally %s was free while you played. ", gb(k.RamFreeTypMB))
		if k.RamFreeMinMB >= 2048 {
			// 2.31: plenty of RAM free, only the commit limit was close: say that, not "RAM ran out"
			title = fmt.Sprintf("Memory promised to programs came close to Windows' limit: %s of %s", gb(k.CommitPeakMB), gb(k.CommitLimitMB))
			detail = fmt.Sprintf("RAM itself still had %s free at the lowest. The limit is RAM plus the page file: programs reserve memory they may never use, and when the reservations reach the limit new requests fail and the game can crash. ", gb(k.RamFreeMinMB))
		}
		if ramN > 0 {
			detail += fmt.Sprintf("%d stutter%s (worst %.0f ms) came with less than 500 MB free. ", ramN, plural(ramN), ramWorst)
		}
		if len(hurt) > 0 {
			detail += "Here it happened during the screen resets above: fixing those fixes this. "
		}
		if k.RamFreeMinMB < 1024 {
			detail += "When RAM runs out Windows moves memory to the page file and everything waits; at the limit, memory requests fail and the game can crash. "
		}
		if r.Crash != nil {
			detail += "Only the time before the crash counts here (the crash report and the driver reset use memory too). "
		}
		detail += "Closing browsers and launchers while playing helps; a bigger page file raises the limit (Experiments: " + ExpNames["pagefile"] + ")."
		add(level, title, detail)
		ramMetric := "commit_headroom_mb"
		if k.RamFreeMinMB < 1024 {
			ramMetric = "ram_free_min_mb"
		}
		switch {
		case k.CommitLimitMB > 0 && k.CommitPeakMB > 0.9*k.CommitLimitMB && !expOn(r.Meta, "pagefile"):
			d := expDo("pagefile", fmt.Sprintf("Windows' limit of %s goes up by the size of the new page file (16 to 32 GB) after a restart, so the game can't run into it.", gb(k.CommitLimitMB)), "commit_headroom_mb")
			d.Bar = gbBar("Memory promised to programs", k.CommitPeakMB, k.CommitLimitMB)
			do(d)
		case k.RamFreeMinMB < 1024 && !expOn(r.Meta, "standbyauto"):
			d := expDo("standbyauto", "Frees the RAM Windows keeps for cached files before each session.", ramMetric)
			d.Text = "Close browsers and launchers before playing, and turn on \"" + ExpNames["standbyauto"] + "\" in Experiments."
			do(d)
		default:
			do(&Do{Text: "Close browsers and launchers before playing.", Effect: "Leaves more memory for the game.", Metric: ramMetric})
		}
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
		vd := &Do{Text: "Lower the game's texture setting one step.", Effect: "Frees VRAM, so the game stops making room in the middle of a frame.", Button: "Open VRAM", Go: "vram", Metric: "vram_peak_pct"}
		if len(big) > 0 {
			names := big[0].Name
			if len(big) > 1 {
				names += " and " + big[1].Name
			}
			vd.Text = "Free VRAM: lower the game's texture setting one step, or use a lighter version of the mods that loaded the most (" + names + ")."
		}
		if vramN > 0 {
			vd.Metric = "vram_stutters_10"
		}
		vd.Title = "Free graphics memory (VRAM)"
		vp := k.VramPlayPeakMB
		if vp <= 0 {
			vp = k.VramPeakMB
		}
		vd.Bar = gbBar("Graphics memory the game used", vp, k.VramBudgetMB)
		do(vd)
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
			"VRAM page, \"Programs on the graphics card\" (it changes the same Windows setting as Settings > Display > Graphics, with an exact undo), or close them before playing."
		if ga.MovableTypMB < 150 {
			detail = "Most of it is Windows itself or programs that belong on " + cardName(r.Meta) + ", so moving programs would free little here."
		}
		add(level, fmt.Sprintf("Everything else on the game's card held %s of graphics memory while you played", mbOrGb(ga.OthersTypMB)), detail, items...)
		if level == "try" {
			// both editions: Frame Lab moves them itself (a per-program Windows setting with an exact Undo)
			d := &Do{Text: "Move the programs that don't need " + cardName(r.Meta) + " to the integrated graphics, or close them before playing.",
				Effect: fmt.Sprintf("Frees about %s of VRAM for the game.", mbOrGb(ga.MovableTypMB)), Button: "Open VRAM", Go: "vram", Metric: "others_vram_mb",
				Title: "Move background programs off the game's card"}
			if !Experimental {
				d.Steps = []string{"Open VRAM with the button below, then Programs on the graphics card.", "Press Move all suggested (or Move to integrated on one program). Undo on each row puts it back.", "Close those programs fully, also from the tray, and start them again."}
			}
			do(d)
		}
	}

	// ---- long freezes nothing in the game explains
	var disk, core, vr, dpc, shared, pages, unRead, unDef float64
	unN, unWorst := count(func(s *Spike) bool {
		if s.Group == "unexplained" && s.Ms >= 150 {
			unRead = math.Max(unRead, s.ReadMaxMs)
			unDef += s.DefenderMB
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
	// the game's GPU memory in system RAM over the whole session: its usual level and its low level
	var sharedTyp, sharedBase float64
	{
		// while playing only: loading screens and the main menu keep less there
		var sh []float64
		for i, v := range r.Timeline.SharedMB {
			if v <= 0 || i >= len(r.Timeline.SampT) {
				continue
			}
			t, play := r.Timeline.SampT[i], true
			for _, np := range r.Timeline.NotPlaying {
				if t >= np[0]-2 && t <= np[1]+10 {
					play = false
					break
				}
			}
			if play {
				sh = append(sh, v)
			}
		}
		if len(sh) > 0 {
			sort.Float64s(sh)
			sharedTyp, sharedBase = pctl(sh, 50), pctl(sh, 10)
		}
	}
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
		do(&Do{Text: "Find the driver with LatencyMon, then update it or disable its device for one session.", Metric: "freezes_10"})
	} else if unN > 0 && shared >= 300 && (vr >= 0.95 || shared-sharedBase >= 300) {
		// 2.5: only when VRAM was near the game's budget at the freezes, or the game's memory in RAM grew
		// well past its usual level then. A fixed amount the game keeps in RAM from loading to quitting
		// (upload and readback buffers, about 400-500 MB on a 16 GB card) is not VRAM running out.
		title := fmt.Sprintf("VRAM spilled into system RAM during %d long freeze%s (worst %.0f ms): %.0f MB of the game's GPU memory in RAM", unN, plural(unN), unWorst, shared)
		if sharedTyp >= 0.8*shared && vr >= 0.95 {
			title = fmt.Sprintf("VRAM was full: %.0f MB of the game's GPU memory stayed in system RAM while you played, with %d long freeze%s (worst %.0f ms)", sharedTyp, unN, plural(unN), unWorst)
		}
		add("fix", title,
			"When VRAM runs out Windows keeps part of the game's GPU memory in normal RAM and moves it back and forth, which freezes frames. Freeing VRAM (see the VRAM item) is the fix.")
	} else if unN > 0 {
		next := "Frame Lab's Windows event recorder (Settings) names which. "
		if r.Windows != nil && r.Windows.Recorded {
			next = "The Windows events don't show a driver or paging at those moments either, so it is most likely memory moving between VRAM and RAM, or work inside the game nothing here can see. "
		}
		test := "Worth a test in Experiments: clearing standby memory before playing."
		if unRead >= SlowReadMs || unDef >= 1 {
			test = fmt.Sprintf("Disk reads were slow then (longest %.0f ms). Worth a test in Experiments: a Defender exclusion for the game folder, and clearing standby memory before playing.", unRead)
			if unDef >= 1 {
				test = fmt.Sprintf("Microsoft Defender was reading from disk at those moments (%.0f MB). Worth a test in Experiments: a Defender exclusion for the game folder, and clearing standby memory before playing.", unDef)
			}
		}
		steady := ""
		if shared >= 300 {
			steady = fmt.Sprintf("The game kept about %.0f MB of its GPU memory in system RAM the whole session, a fixed amount that didn't grow at the freezes, so that is not VRAM running out. ", sharedTyp)
		}
		add("try", fmt.Sprintf("%d long freeze%s (worst %.0f ms) with nothing inside the game to show for it", unN, plural(unN), unWorst),
			fmt.Sprintf("At those moments: the game reading up to %.0f MB/s, the busiest CPU core at %.0f%%, VRAM at %.0f%% of the budget. "+
				"%sThat points at Windows rather than a mod: memory being moved between VRAM and RAM, a driver holding the CPU, or the disk. "+
				"%s%s",
				disk, core, vr*100, steady, next, test))
		switch {
		case (unRead >= SlowReadMs || unDef >= 1) && !expOn(r.Meta, "defender"):
			do(expDo("defender", "Stops Defender scanning the game's files as they are read.", "freezes_10"))
		case !expOn(r.Meta, "standbyauto"):
			do(expDo("standbyauto", "Rules out Windows moving cached memory around while you play.", "freezes_10"))
		case !expOn(r.Meta, "quiet"):
			do(expDo("quiet", "Rules out Windows Search, Windows Update and SysMain working in the background.", "freezes_10"))
		case r.Windows == nil || !r.Windows.Recorded:
			do(&Do{Text: "Switch on the Windows event recorder (Settings) and record again.", Effect: "It names the driver or program behind these freezes.", Metric: "freezes_10"})
		}
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
			do(&Do{Text: "Update or roll back the driver this item names.", Effect: "For a network, audio, USB or Bluetooth device, one session with it unplugged shows whether it is the one.", Metric: "driver_10"})
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
			if !expOn(r.Meta, "standbyauto") {
				d := expDo("standbyauto", "Frees the RAM Windows keeps for cached files before each session.", "paging_10")
				d.Text = "Close browsers and launchers before playing, and turn on \"" + ExpNames["standbyauto"] + "\" in Experiments."
				do(d)
			} else {
				do(&Do{Text: "Close browsers and launchers before playing.", Effect: "Leaves more RAM for the game.", Metric: "paging_10"})
			}
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
		_, mk := r.Meta.Keys()
		add(level, "Script mods that cost the most in your "+mk+" part",
			"This is steady work on every frame, not stutter. A mod that runs where it has nothing to do (far from its place, or every frame instead of now and then) is the one to look at, or to switch off for one test session and compare.",
			items...)
		if costly[0].ms >= 20 {
			c := costly[0]
			cost := fmt.Sprintf("It costs %.1f ms of every second now.", c.ms)
			if c.kind == "CET" {
				do(&Do{Text: "Slow down " + c.name + " in Optimise if it is listed there, or switch it off for one session and compare.", Effect: cost, Button: "Open Optimise", Go: "optimise", Metric: modMetric + c.name})
			} else {
				do(&Do{Text: "Switch " + c.name + " off for one session and compare.", Effect: cost, Metric: modMetric + c.name})
			}
		}
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

	// ---- disk (2.31: heavy reads alone are the game streaming; only slow reads blame the drive)
	dN, dMax, slowN, slowMax, defN := 0, 0.0, 0, 0.0, 0
	winRec := r.Windows != nil && r.Windows.Recorded
	for _, s := range r.Spikes {
		if s.Group == "disk" {
			dN++
			dMax = math.Max(dMax, s.DiskMBs)
		}
		if s.ReadMaxMs >= SlowReadMs {
			slowN++
			slowMax = math.Max(slowMax, s.ReadMaxMs)
		}
		if s.DefenderMB >= 1 {
			defN++
		}
	}
	defTip := "a Defender exclusion for the game folder (Experiments) stops Windows scanning those files as they are read."
	switch {
	case slowN > 0:
		det := fmt.Sprintf("Windows saw the game wait on the drive: one read took %.0f ms, where a healthy SSD answers in a few ms at most. Keep the game on the fastest NVMe drive with some free space; %s", slowMax, defTip)
		if defN > 0 {
			det += fmt.Sprintf(" Microsoft Defender was reading from disk during %d of the stutters.", defN)
		}
		add("try", fmt.Sprintf("Slow disk reads during %d stutter%s", slowN, plural(slowN)), det)
		if !expOn(r.Meta, "defender") {
			do(expDo("defender", "Stops Defender scanning the game's files as they are read.", "slow_reads_10"))
		} else {
			do(&Do{Text: "Move the game to your fastest NVMe drive, with some free space on it.", Metric: "slow_reads_10"})
		}
	case defN > 0:
		add("try", fmt.Sprintf("Microsoft Defender was reading from disk during %d stutter%s", defN, plural(defN)),
			"The game's own reads were fast, but Defender was scanning at the same time; "+defTip)
		if !expOn(r.Meta, "defender") {
			do(expDo("defender", "Stops Defender scanning the game's files as they are read.", ""))
		}
	case dN > 0 && winRec:
		add("info", fmt.Sprintf("The game was streaming in a lot of data during %d stutter%s (up to %.0f MB/s)", dN, plural(dN), dMax),
			fmt.Sprintf("The drive kept up: no read took over %.0f ms. Those stutters come from the game handling that data (unpacking it, building the world and textures from it), not from waiting on the disk, so a faster drive or a Defender exclusion won't change them.", SlowReadMs))
	case dN > 0:
		add("info", fmt.Sprintf("The game was streaming in a lot of data during %d stutter%s (up to %.0f MB/s)", dN, plural(dN), dMax),
			"Heavy reads on their own are normal streaming, not a slow drive. Windows' read times weren't recorded in this session (Windows events in Settings, Frame Lab as administrator), so Frame Lab can't tell whether the drive itself was slow.")
	}

	// ---- the same files loaded again and again (2.31)
	if n := r.ReloadStutters; n > 0 && len(r.Reloaded) > 0 {
		var items []string
		for i, f := range r.Reloaded {
			if i == 8 {
				break
			}
			name := f.Name
			if name == "" && f.Mod != "" {
				name = "a file from " + f.Mod
			} else if name == "" {
				name = "a base-game file (" + f.Label + ")"
			}
			items = append(items, fmt.Sprintf("%s: loaded in %d stutters", name, f.Spikes))
		}
		files := fmt.Sprintf("%d file%s", len(r.Reloaded), plural(len(r.Reloaded)))
		if r.ReloadVramFull*2 >= n {
			add("fix", fmt.Sprintf("The same %s loaded again and again (in %d stutters): VRAM is too full to keep them", files, n),
				fmt.Sprintf("VRAM was at 90%% of its budget or more in %d of those stutters, so the game drops these files to make room and loads them again when they're needed. Freeing VRAM (see the VRAM item) stops the reloading.", r.ReloadVramFull),
				items...)
		} else {
			add("info", fmt.Sprintf("The same %s loaded again in %d stutters", files, n),
				"VRAM wasn't near its budget then, so VRAM running out isn't the reason. More likely the game unloads an area and loads it again as you move back and forth.",
				items...)
		}
	}

	// ---- GPU
	if k.ThrottleBad {
		add("fix", "The GPU slowed itself down: "+k.ThrottleText, fmt.Sprintf("Hottest: %.0f °C. Check the case airflow and the GPU fans.", k.GpuTempMax))
		do(&Do{Text: "Check the case airflow and the graphics card's fans.", Metric: "gpu_temp"})
	} else if k.ThrottleText != "" {
		add("info", "The GPU ran at its power limit", fmt.Sprintf("Normal under full load (hottest %.0f °C). More fps from here means lighter settings, not tweaks.", k.GpuTempMax))
	}
	if k.GpuBoundPct < 0 {
		for _, w := range r.Warnings {
			if strings.Contains(w, "frame generation") {
				add("info", "GPU time isn't measured with frame generation on",
					"PresentMon can't tell the generated frames apart, so Frame Lab can't say whether the GPU or the CPU is the limit. One test session with frame generation off answers that.")
				do(&Do{Text: "Optional: record one session with frame generation off, to see whether the GPU or the CPU is the limit."})
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
			do(&Do{Text: fmt.Sprintf("Get to %.0f shown frames a second: a higher frame generation multiplier, or VRR (G-SYNC / FreeSync).", pc.RefreshHz), Metric: "held_pct"})
		case pc.RefreshHz > 0:
			add("good", fmt.Sprintf("Even frame pacing: %.0f frames shown per second on a fixed %.0f Hz screen, %.1f%% held for two refreshes", pc.ShownFps, pc.RefreshHz, pc.HeldPct), "")
		}
	}

	// ---- pacing
	if k.AvgFps > 0 && k.Low1Fps > 0 && k.Low1Fps/k.AvgFps < 0.7 {
		add("try", fmt.Sprintf("Uneven frame pacing: 1%% low %.0f fps against %.0f fps average", k.Low1Fps, k.AvgFps),
			"Frames come at uneven intervals even without big spikes. A frame cap a little under your usual fps ("+capPlace(r.Meta)+") usually evens it out.")
		mixed := false
		for _, w := range r.Warnings {
			mixed = mixed || strings.Contains(w, "frame generation") || strings.Contains(w, "no markers from the game")
		}
		if !mixed {
			do(&Do{Text: fmt.Sprintf("Set a frame cap a little under %.0f fps (%s).", k.AvgFps, capPlace(r.Meta)), Metric: "low1_ratio"})
		}
	}

	// ---- overall
	if k.Spikes == 0 {
		add("good", "No stutters over the spike limit", fmt.Sprintf("%.0f min of play without a frame over %.0f ms.", mins, k.SpikeMs))
	} else if float64(k.Spikes)/mins < 0.5 {
		add("good", fmt.Sprintf("Few stutters: %d in %.0f min of play", k.Spikes, mins), "")
	}

	order := map[string]int{"crash": -2, "hw": -1, "fix": 0, "try": 1, "info": 2, "good": 3}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Level] < order[out[j].Level] })
	said := map[string]bool{}
	for i := range out {
		d := out[i].Do
		if d == nil {
			continue
		}
		key := d.Text
		if strings.HasPrefix(d.Go, "exp:") {
			key = d.Go
		}
		if said[key] {
			out[i].Do = nil
		}
		said[key] = true
	}
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
