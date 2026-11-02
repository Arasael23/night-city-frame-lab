//go:build windows

package platform

// GPU check: what every reading source gives for every graphics adapter on this PC, written line
// by line (so a driver library that closes the program still leaves everything before it).

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"framelab/internal/samples"
)

func GpuCheck(line func(string)) {
	p := func(f string, a ...any) { line(fmt.Sprintf(f, a...)) }
	v := windows.RtlGetVersion()
	p("Night City Frame Lab - GPU check, %s", time.Now().Format("2 Jan 2006 15:04:05"))
	p("Windows %d.%d build %d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	p("Display drivers in the registry: %s", strings.Join(DisplayDrivers(), "; "))
	p("")

	ads, err := dxgiAdapters()
	if err != nil {
		p("DXGI: %v", err)
	}
	main, _ := pickMain(ads)
	p("== Adapters (DXGI) ==")
	for _, a := range ads {
		tag := ""
		if a.Luid == main.Luid {
			tag = "  <- the game's card"
		}
		p("%s | vendor 0x%04x (%s) | luid %s | own memory %.0f MB | integrated %v%s", a.Name, a.Vendor, vendorOf(a.Vendor), a.Luid, a.MemMB, a.Integrated, tag)
		p("    %s", kmtText(a.Luid))
	}
	p("")

	p("== Windows GPU counters (Task Manager's numbers) ==")
	if q := newPdh(); q == nil {
		p("PDH not available")
	} else {
		e := q.add("eng", `\GPU Engine(*)\Utilization Percentage`)
		m := q.add("ad", `\GPU Adapter Memory(*)\Dedicated Usage`)
		p("GPU Engine counter: %v, GPU Adapter Memory counter: %v", e, m)
		q.collect()
		time.Sleep(700 * time.Millisecond)
		q.collect()
		eng, ad := q.array("eng"), q.array("ad")
		for _, a := range ads {
			u, uok := gpuUsage(eng, a.Luid)
			mb, mok := adapterMemMB(ad, a.Luid)
			p("%s: usage %.1f%% (%v), own memory in use %.0f MB (%v)", a.Name, u, uok, mb, mok)
			n3 := node3DFromEngines(eng, a.Luid)
			line := fmt.Sprintf("    3D engine from the counters: node %d; WDDM engine clocks:", n3)
			if k := openKmt(a.Luid); k != nil {
				for n := uint32(0); n < 8; n++ {
					f, st := k.nodeFreq(n)
					if st == 0 {
						line += fmt.Sprintf(" node %d = %.0f MHz;", n, f)
						if n == 0 {
							nd := kmtNodePerfData{Node: 0}
							k.query(kmtNodePerf, unsafe.Pointer(&nd), unsafe.Sizeof(nd))
							line += fmt.Sprintf(" (max %.0f MHz)", float64(nd.MaxFreq)/1e6)
						}
					} else if n == 0 || int32(n) == n3 {
						line += fmt.Sprintf(" node %d status 0x%08x;", n, st)
					}
				}
			}
			p("%s", line)
		}
		q.close()
	}
	p("")

	p("== NVIDIA driver (NVML) ==")
	if n := loadNVML(); !n.ok {
		p("not available")
	} else {
		var s samples.Sample
		n.read(&s)
		p("%s: clock %.0f MHz, memory clock %.0f MHz, temp %.0f °C, power %.1f W, usage %.0f%%, VRAM %.0f / %.0f MB, slowdown bits 0x%x",
			n.name, s.GpuClockMHz, s.MemClockMHz, s.GpuTempC, s.GpuPowerW, s.GpuUtilPct, s.VramUsedMB, s.VramTotalMB, s.Throttle)
	}
	p("")

	p("== AMD driver (ADLX) ==")
	amd := 0
	for _, a := range ads {
		if vendorOf(a.Vendor) != "amd" {
			continue
		}
		amd++
		p("starting ADLX for %s ...", a.Name)
		g, note := openADLX(a, false)
		if g == nil {
			p("    %s", note)
			continue
		}
		p("    ADLX card: %q, vendor id %s, integrated %v, total VRAM %.0f MB", g.name, g.vendorID, g.integrated, g.vramMB)
		p("    supported: %s", g.supportedText())
		for i := 0; i < 3; i++ {
			r := g.current()
			p("    reading %d: ok %v, usage %.1f%%, clock %.0f MHz, memory clock %.0f MHz, temp %.1f °C, hotspot %.1f °C, GPU power %.1f W, board power %.1f W, fan %.0f rpm, VRAM %.0f MB",
				i+1, r.OK, r.Usage, r.Clock, r.VClock, r.Temp, r.Hot, r.Power, r.TBP, r.Fan, r.Vram)
			time.Sleep(500 * time.Millisecond)
		}
	}
	if amd == 0 {
		p("no AMD adapter")
	}
	p("")

	p("== Each card as if the game ran on it (the same code Frame Lab uses) ==")
	for _, a := range ads {
		if vendorOf(a.Vendor) == "nvidia" {
			continue // NVIDIA cards use NVML, shown above
		}
		g := sourceFor(a, true, false)
		p("%s | source: %s | gives: %s | note: %s", g.info.Name, g.info.Source, strings.Join(g.info.Reads, ", "), g.info.Note)
		q := newPdh()
		if q != nil {
			q.add("eng", `\GPU Engine(*)\Utilization Percentage`)
			q.add("ad", `\GPU Adapter Memory(*)\Dedicated Usage`)
			q.collect()
		}
		for i := 0; i < 3; i++ {
			time.Sleep(700 * time.Millisecond)
			var s samples.Sample
			if q != nil {
				q.collect()
				g.read(&s, q.array("eng"), q.array("ad"))
			} else {
				g.read(&s, nil, nil)
			}
			p("    reading %d: clock %.0f MHz, temp %.1f °C, power %.1f W, usage %.1f%%, VRAM %.0f / %.0f MB", i+1, s.GpuClockMHz, s.GpuTempC, s.GpuPowerW, s.GpuUtilPct, s.VramUsedMB, s.VramTotalMB)
		}
		if q != nil {
			q.close()
		}
	}
	p("")

	p("== What Frame Lab will use ==")
	gi := GPU()
	p("card: %s | vendor %s | source: %s | gives: %s", gi.Name, gi.Vendor, gi.Source, strings.Join(gi.Reads, ", "))
	if gi.Note != "" {
		p("note: %s", gi.Note)
	}
	for i := 0; i < 3; i++ {
		p("live reading %d: %v", i+1, LiveGPU())
		time.Sleep(700 * time.Millisecond)
	}
	p("")
	p("Done.")
}
