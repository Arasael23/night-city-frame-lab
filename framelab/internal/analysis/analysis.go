package analysis

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"framelab/internal/hrec"
	"framelab/internal/modmap"
	"framelab/internal/pm"
	"framelab/internal/samples"
)

// Inputs that live outside the session folder.
type Env struct {
	Library *modmap.Library       // may be nil
	Mods    *modmap.BuildArchives // mod archives of the build; may be nil
	Names   modmap.NamesFile      // may be ""
}

// ---------------------------------------------------------------- output

type Result struct {
	Version   int             `json:"version"`
	Generated time.Time       `json:"generated"`
	Meta      *Meta           `json:"meta"`
	KPI       KPI             `json:"kpi"`
	Layers    []LayerState    `json:"layers"`
	Timeline  Timeline        `json:"timeline"`
	Markers   []Marker        `json:"markers"`
	Spikes    []Spike         `json:"spikes"`
	Causes    []CauseCount    `json:"causes"`
	Mods      []ModRow        `json:"mods"`
	Engine    []KindRow       `json:"engine"`
	Shaders   ShaderInfo      `json:"shaders"`
	Advice    []Advice        `json:"advice"`
	Warnings  []string        `json:"warnings"`
	Pacing    *Pacing         `json:"pacing,omitempty"`   // how evenly frames reached the screen (1.4+)
	Windows   *WinSummary     `json:"windows,omitempty"`  // Windows event recorder (1.4+)
	GpuApps   *GpuAppsSummary `json:"gpu_apps,omitempty"` // other programs on the graphics card (1.4+)
}

type KPI struct {
	DurationS      float64 `json:"duration_s"` // gameplay time (loading screens and menus left out when the markers are there)
	SessionS       float64 `json:"session_s"`  // the whole recording
	NotPlayingS    float64 `json:"not_playing_s"`
	LoadScreens    int     `json:"loading_screens"`
	Frames         int     `json:"frames"`
	MedianMs       float64 `json:"median_ms"`
	AvgFps         float64 `json:"avg_fps"`
	Low1Fps        float64 `json:"low1_fps"`
	Low01Fps       float64 `json:"low01_fps"`
	DisplayedFps   float64 `json:"displayed_fps"` // includes frame generation
	Spikes         int     `json:"spikes"`
	SpikeMs        float64 `json:"spike_ms"`
	GpuBoundPct    float64 `json:"gpu_bound_pct"` // -1 = unknown
	VramPeakMB     float64 `json:"vram_peak_mb"`
	VramBudgetMB   float64 `json:"vram_budget_mb"`
	FrameSource    string  `json:"frame_source"`
	ScriptRedMsS   float64 `json:"redscript_ms_per_s"`
	ScriptCetMsS   float64 `json:"cet_ms_per_s"`
	GpuTempMax     float64 `json:"gpu_temp_max"`
	ThrottleText   string  `json:"throttle"`
	ThrottleBad    bool    `json:"throttle_serious"`            // heat / hardware slowdown, not just the power limit
	VramPlayPeakMB float64 `json:"vram_play_peak_mb,omitempty"` // peak outside the screen resets (1.3.4+)
	Resets         int     `json:"resets,omitempty"`            // screen resets while playing
	RamFreeMinMB   float64 `json:"ram_free_min_mb,omitempty"`   // least free system RAM while playing
	RamFreeTypMB   float64 `json:"ram_free_typ_mb,omitempty"`   // median free system RAM while playing
	CommitPeakMB   float64 `json:"commit_peak_mb,omitempty"`
	CommitLimitMB  float64 `json:"commit_limit_mb,omitempty"`
	OthersVramMB   float64 `json:"others_vram_mb,omitempty"` // graphics memory other programs held on the game's card while playing (median, 1.4+)
	SlowdownKnown  bool    `json:"slowdown_known"`           // the card's driver says why it lowers its clocks (NVIDIA)
}

// a screen reset (the game re-applying full screen / rebuilding its screen buffers) and what followed
type ResetMark struct {
	T            float64 `json:"t"`
	Clock        string  `json:"clock"` // local time of day
	Source       string  `json:"source"`
	Text         string  `json:"text"`
	Playing      bool    `json:"playing"`
	VramBefore   float64 `json:"vram_before_mb,omitempty"`
	VramAfter    float64 `json:"vram_after_mb,omitempty"`
	SharedBefore float64 `json:"shared_before_mb,omitempty"`
	SharedAfter  float64 `json:"shared_after_mb,omitempty"`
	RamFreeMin   float64 `json:"ram_free_min_mb,omitempty"`
	SlowS        float64 `json:"slow_s,omitempty"`   // length of the slow frame(s) it started
	EndT         float64 `json:"end_t,omitempty"`    // when the game's VRAM was back to where it was
	EndedBy      string  `json:"ended_by,omitempty"` // clock time of the later reset that gave the memory back
	Release      bool    `json:"release,omitempty"`  // this reset gave back what an earlier one took
}

type LayerState struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type Timeline struct {
	T            []float64    `json:"t"`        // seconds since capture start
	FrameMs      []float64    `json:"frame_ms"` // max frame time in the bucket
	GpuMs        []float64    `json:"gpu_ms"`   // GPU busy (avg), -1 unknown
	VramT        []float64    `json:"vram_t"`
	VramMB       []float64    `json:"vram_mb"`
	BudgetMB     []float64    `json:"budget_mb"`
	SampT        []float64    `json:"samp_t"`
	ClockMHz     []float64    `json:"clock_mhz"`
	TempC        []float64    `json:"temp_c"`
	PowerW       []float64    `json:"power_w"`
	DiskMBs      []float64    `json:"disk_mbs"`
	CpuPct       []float64    `json:"cpu_pct"`
	OtherGpu     []float64    `json:"other_gpu_pct"`
	CpuClock     []float64    `json:"cpu_clock_mhz,omitempty"` // average effective CPU clock (1.3+)
	CoresBusy    []float64    `json:"cores_busy,omitempty"`    // logical cores at 90%+
	DpcPct       []float64    `json:"dpc_pct,omitempty"`       // driver DPC + interrupt time, % of the CPU
	SharedMB     []float64    `json:"shared_mb,omitempty"`     // game GPU memory kept in system RAM
	RamFreeMB    []float64    `json:"ram_free_mb,omitempty"`   // free system RAM (1.3.4+)
	Resets       []ResetMark  `json:"resets,omitempty"`        // screen resets from ReShade's / CET's log (1.3.4+)
	DispT        []float64    `json:"disp_t,omitempty"`        // per second: frames that reached the screen (1.4+)
	DispFps      []float64    `json:"disp_fps,omitempty"`
	DispHeld     []float64    `json:"disp_held,omitempty"`      // % of them shown for 2+ refreshes (fixed refresh)
	VramOthersMB []float64    `json:"vram_others_mb,omitempty"` // at each vram_t: what every other program held on the card (1.4+)
	Gaps         [][2]float64 `json:"gaps"`                     // paused stretches (not recorded)
	NotPlaying   [][2]float64 `json:"not_playing"`              // loading screens, main menu: shown, left out of the numbers
	Focus        [][2]float64 `json:"focus"`                    // parts marked with F11 (session mode)
	T0Qpc        int64        `json:"t0_qpc"`                   // QPC at t = 0 (for the timeline export)
}

type Marker struct {
	T    float64 `json:"t"`
	Kind string  `json:"kind"` // "mark", "start", "stop"
}

type Part struct {
	Label string  `json:"label"`
	Ms    float64 `json:"ms"`
	Group string  `json:"group"` // shader, world, redscript, cet, gpu, vram, disk, other
}

type Item struct {
	Label string  `json:"label"`
	Kind  string  `json:"kind"`
	Ms    float64 `json:"ms"`
	Main  bool    `json:"main"`
	Mod   string  `json:"mod,omitempty"`
}

type Spike struct {
	ID         int       `json:"id"`
	T          float64   `json:"t"`
	Ms         float64   `json:"ms"`
	Frames     int       `json:"frames"`
	Cause      string    `json:"cause"`
	Group      string    `json:"group"`
	Parts      []Part    `json:"parts"`
	Items      []Item    `json:"items"`
	GpuMs      float64   `json:"gpu_ms"`
	VramMB     float64   `json:"vram_mb"`
	BudgetMB   float64   `json:"budget_mb"`
	DiskMBs    float64   `json:"disk_mbs"`
	CpuPct     float64   `json:"cpu_pct"`
	MaxCore    float64   `json:"max_core_pct"`
	Throttle   string    `json:"throttle,omitempty"`
	OtherGpu   string    `json:"other_gpu,omitempty"`
	DpcPct     float64   `json:"dpc_pct,omitempty"`     // driver DPC + interrupt time around the spike (1.3+)
	SharedMB   float64   `json:"shared_mb,omitempty"`   // game GPU memory in system RAM
	PagesSec   float64   `json:"pages_sec,omitempty"`   // hard page faults per second
	RamFreeMB  float64   `json:"ram_free_mb,omitempty"` // least free system RAM around the spike
	Reset      string    `json:"reset,omitempty"`       // the screen reset this spike started with or followed
	AfterReset bool      `json:"after_reset,omitempty"` // inside the time a screen reset kept the extra memory
	Win        []WinItem `json:"win,omitempty"`         // what Windows was doing (event recorder, 1.4+)
	CpuClock   float64   `json:"cpu_clock_mhz,omitempty"`
	Verdict    string    `json:"verdict"`
	NearMark   bool      `json:"near_mark"`
	Focus      bool      `json:"focus"` // inside a part marked with F11
}

type CauseCount struct {
	Cause   string  `json:"cause"`
	Group   string  `json:"group"`
	Count   int     `json:"count"`
	Pct     float64 `json:"pct"`
	TotalMs float64 `json:"total_ms"`
}

type ModRow struct {
	Name     string   `json:"name"`
	Zip      string   `json:"zip,omitempty"`
	Kinds    []string `json:"kinds"`
	RedMsS   float64  `json:"redscript_ms_per_s"`
	CetMsS   float64  `json:"cet_ms_per_s"`
	Files    int      `json:"files_loaded"`
	MB       float64  `json:"mb_loaded"` // unpacked size of its files the game loaded (each file once)
	LoadMs   float64  `json:"load_ms"`
	InSpikes int      `json:"in_spikes"`
	SpikeMs  float64  `json:"spike_ms"` // its share inside spikes
	Score    float64  `json:"score"`
	Note     string   `json:"note,omitempty"`
}

type KindRow struct {
	Kind    string  `json:"kind"`
	Count   int     `json:"count"`
	Unique  int     `json:"unique"`
	MainPct float64 `json:"main_pct"`
	AvgMs   float64 `json:"avg_ms"`
	MaxMs   float64 `json:"max_ms"`
	Over5   int     `json:"over_5ms"`
	Over16  int     `json:"over_16ms"`
}

type ShaderInfo struct {
	Recorded     bool    `json:"recorded"`
	Total        int     `json:"total"`
	AfterWarmup  int     `json:"after_warmup"`
	SlowAfter    int     `json:"slow_after_warmup"`
	RtBuilds     int     `json:"rt_builds"`
	RtSlowestMs  float64 `json:"rt_slowest_ms"`
	CacheLookups int     `json:"cache_lookups"`
	CacheMisses  int     `json:"cache_misses"`
	CachedBlobs  int     `json:"cached_blobs"`
	Slowest      []Item  `json:"slowest"`
}

// ---------------------------------------------------------------- loading

type data struct {
	meta    *Meta
	hr      *hrec.File
	pmc     *pm.Capture
	samp    []samples.Sample
	reds    *ScriptLayer // REDscript time per mod
	cetl    *ScriptLayer // CET Lua time per mod
	freq    float64
	anchorQ int64
	anchorU float64
}

func (d *data) qpcToUnix(q int64) float64 { return d.anchorU + float64(q-d.anchorQ)*1000.0/d.freq }
func (d *data) unixToQpc(u float64) int64 { return d.anchorQ + int64((u-d.anchorU)*d.freq/1000.0) }

// Run analyses the session folder and writes analysis.json.
func Run(dir string, env Env) (*Result, error) {
	meta, err := ReadMeta(dir)
	if err != nil {
		return nil, fmt.Errorf("session.json: %w", err)
	}
	d := &data{meta: meta}
	res := &Result{Version: 1, Generated: time.Now(), Meta: meta}
	warn := func(f string, a ...any) { res.Warnings = append(res.Warnings, fmt.Sprintf(f, a...)) }

	if f, err := hrec.Read(filepath.Join(dir, FileHitch)); err == nil {
		d.hr = f
	}
	if c, err := pm.Read(filepath.Join(dir, FilePresentMon), "Cyberpunk2077.exe"); err == nil && len(c.Frames) > 0 {
		d.pmc = c
	}
	if s, err := samples.Read(filepath.Join(dir, FileSamples)); err == nil && len(s) > 0 {
		d.samp = s
	}
	// screen resets from ReShade's / CET's log: kept with the session since 1.3.4; for older
	// sessions the game's logs are read now (they only count if they fall inside the recording)
	disp, derr := ReadDisplay(dir)
	dispFromFile, dispLogs := derr == nil, derr == nil
	if !dispFromFile {
		disp, dispLogs = ReadDisplayLogs(meta.Game, meta.Created.Add(-10*time.Second))
	}

	// ---- clock
	switch {
	case meta.QpcFreq > 0:
		d.freq = float64(meta.QpcFreq)
	case d.hr != nil:
		d.freq = float64(d.hr.Header.QpcFreq)
	default:
		d.freq = 10_000_000
	}
	switch {
	case meta.AnchorQpc != 0 && meta.AnchorMs > 0:
		d.anchorQ, d.anchorU = meta.AnchorQpc, meta.AnchorMs
	case d.hr != nil:
		d.anchorQ, d.anchorU = int64(d.hr.Header.Qpc0), d.hr.Header.UnixMs0()
	}
	if d.anchorQ == 0 {
		warn("No shared clock between the layers; times may not line up.")
	}
	// script time per mod: HitchRecorder's REDscript and CET layers
	if s := d.ourScripts(dir); s != nil {
		d.reds = s
	}
	if c := d.ourCet(dir); c != nil {
		d.cetl = c
	}

	spikeMs := meta.SpikeMs
	if spikeMs <= 0 {
		spikeMs = 50
	}
	wl := loadWinLayer(dir, d.freq) // Windows event recorder (1.4+)

	// ---- capture windows (QPC)
	windows := d.captureWindows()
	if len(windows) == 0 {
		return nil, fmt.Errorf("nothing was recorded in this session")
	}
	t0 := windows[0][0]
	res.Timeline.T0Qpc = t0
	tsec := func(q int64) float64 { return float64(q-t0) / d.freq }
	inWin := func(q int64) bool {
		for _, w := range windows {
			if q >= w[0] && q <= w[1] {
				return true
			}
		}
		return false
	}
	for i := 1; i < len(windows); i++ {
		res.Timeline.Gaps = append(res.Timeline.Gaps, [2]float64{tsec(windows[i-1][1]), tsec(windows[i][0])})
	}
	totalS := 0.0
	for _, w := range windows {
		totalS += float64(w[1]-w[0]) / d.freq
	}
	res.KPI.SessionS = totalS

	// ---- not playing (loading screens, menu) from the session markers, as QPC spans
	var notPlay [][2]int64
	marksFound := false
	if marks, err := ReadMarks(dir); err == nil && len(marks) > 0 && d.anchorQ != 0 {
		marksFound = true
		startMs, endMs := d.qpcToUnix(windows[0][0]), d.qpcToUnix(windows[len(windows)-1][1])
		for _, sp := range notPlaying(marks, startMs, endMs) {
			a, b := d.unixToQpc(sp[0]), d.unixToQpc(sp[1])
			if b < windows[0][0] || a > windows[len(windows)-1][1] {
				continue
			}
			a, b = maxI64(a, windows[0][0]), minI64(b, windows[len(windows)-1][1])
			notPlay = append(notPlay, [2]int64{a, b})
			res.Timeline.NotPlaying = append(res.Timeline.NotPlaying, [2]float64{round3(tsec(a)), round3(tsec(b))})
			res.KPI.NotPlayingS += float64(b-a) / d.freq
		}
		for _, m := range marks {
			if m.Text == "loading screen on" {
				res.KPI.LoadScreens++
			}
		}
	}
	playing := func(q int64) bool {
		if !inWin(q) {
			return false
		}
		for _, s := range notPlay {
			if q >= s[0] && q <= s[1] {
				return false
			}
		}
		return true
	}
	totalS -= res.KPI.NotPlayingS
	if totalS < 0 {
		totalS = 0
	}
	res.KPI.NotPlayingS = round2(res.KPI.NotPlayingS)
	res.KPI.DurationS = totalS

	// ---- parts marked with F11 (session mode)
	var focus [][2]int64
	if d.hr != nil {
		var open int64 = -1
		for i := range d.hr.Recs {
			r := &d.hr.Recs[i]
			if r.Kind != hrec.KMark {
				continue
			}
			q := d.hr.Header.Qpc(r.TUs)
			if r.Extra == hrec.MarkFocusStart {
				open = q
			} else if r.Extra == hrec.MarkFocusStop && open >= 0 {
				focus = append(focus, [2]int64{open, q})
				open = -1
			}
		}
		if open >= 0 {
			focus = append(focus, [2]int64{open, windows[len(windows)-1][1]})
		}
		for _, f := range focus {
			res.Timeline.Focus = append(res.Timeline.Focus, [2]float64{round3(tsec(f[0])), round3(tsec(f[1]))})
		}
	}
	inFocus := func(a, b int64) bool {
		for _, f := range focus {
			if a <= f[1] && b >= f[0] {
				return true
			}
		}
		return false
	}
	res.KPI.SpikeMs = spikeMs
	res.KPI.GpuBoundPct = -1

	// ---- layers
	res.Layers = d.layerStates(windows, len(focus) > 0)
	if d.hr != nil && d.hr.Header.CaptureMode == 2 {
		if len(notPlay) > 0 {
			res.Layers = append(res.Layers, LayerState{"Loading screens + menu", true,
				fmt.Sprintf("%d loading screen(s), %.0f s in all, shown on the timeline and left out of the numbers", res.KPI.LoadScreens, res.KPI.NotPlayingS)})
		} else {
			det := "no markers from the game (CET's gamelog.log missing or empty), so loading screens and the menu count as play"
			if marksFound {
				det = "the game's markers don't fall inside this recording (clock mismatch?), so loading screens and the menu count as play"
			}
			res.Layers = append(res.Layers, LayerState{"Loading screens + menu", false, det})
		}
	}
	for _, l := range res.Layers {
		if !l.OK && l.Detail != "" {
			warn("%s: %s", l.Name, l.Detail)
		}
	}

	// ---- frames
	type frame struct {
		q   int64
		ms  float64
		gpu float64 // -1 unknown
	}
	var frames []frame
	displayed := 0
	if d.pmc != nil && d.pmc.HasQpc {
		app := d.pmc.AppFrames()
		for i, f := range app {
			if f.Qpc < 0 || !inWin(f.Qpc) {
				continue
			}
			ms := f.FrameMs
			if i+1 < len(app) && app[i+1].Qpc > f.Qpc {
				ms = float64(app[i+1].Qpc-f.Qpc) * 1000.0 / d.freq
			}
			g := -1.0
			if d.pmc.HasGPU {
				g = f.GPUBusyMs
			}
			frames = append(frames, frame{f.Qpc, ms, g})
		}
		for _, f := range d.pmc.Frames {
			if f.Qpc >= 0 && inWin(f.Qpc) && f.DisplayMs != 0 {
				displayed++
			}
		}
		res.KPI.FrameSource = "PresentMon (frames the game rendered)"
		if d.pmc.Generated > 0 {
			res.KPI.FrameSource += fmt.Sprintf("; frame generation on (%d generated frames not counted as game frames)", d.pmc.Generated)
		}
	}
	// frame generation without frame-type info doubles the present rate: fall back to the game's own loop
	if len(frames) > 0 && d.hr != nil && d.pmc != nil && d.pmc.Generated == 0 {
		hrN := 0
		for i := range d.hr.Recs {
			if d.hr.Recs[i].Kind == hrec.KFrame && inWin(d.hr.Header.Qpc(d.hr.Recs[i].TUs)) {
				hrN++
			}
		}
		if hrN > 100 && float64(len(frames)) > 1.6*float64(hrN) {
			warn("PresentMon saw about %.1fx more frames than the game rendered (frame generation without frame-type info); frame times come from the game's main loop instead.", float64(len(frames))/float64(hrN))
			frames = frames[:0]
		}
	}
	if len(frames) == 0 && d.hr != nil {
		for i := range d.hr.Recs {
			r := &d.hr.Recs[i]
			if r.Kind == hrec.KFrame {
				q := d.hr.Header.Qpc(r.TUs)
				if inWin(q) {
					frames = append(frames, frame{q, float64(r.DurUs) / 1000, -1})
				}
			}
		}
		res.KPI.FrameSource = "HitchRecorder (game main loop)"
		displayed = len(frames)
	}
	if len(frames) == 0 {
		warn("No frame times recorded.")
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].q < frames[j].q })

	// frames while playing (loading screens / menu left out): numbers and spikes come from these
	play := frames
	if len(notPlay) > 0 {
		play = nil
		for _, f := range frames {
			if playing(f.q) {
				play = append(play, f)
			}
		}
	}
	if len(frames) > 0 && len(play) == 0 {
		warn("No frames while playing (only loading screens or the menu were recorded).")
	}

	// KPIs from frames
	if n := len(play); n > 0 {
		ms := make([]float64, n)
		sum := 0.0
		gpuKnown, gpuBound := 0, 0
		for i, f := range play {
			ms[i] = f.ms
			sum += f.ms
			if f.gpu >= 0 {
				gpuKnown++
				if f.gpu >= 0.9*f.ms {
					gpuBound++
				}
			}
		}
		sorted := append([]float64(nil), ms...)
		sort.Float64s(sorted)
		res.KPI.Frames = n
		res.KPI.MedianMs = pctl(sorted, 50)
		res.KPI.AvgFps = float64(n) / (sum / 1000)
		res.KPI.Low1Fps = 1000 / pctl(sorted, 99)
		res.KPI.Low01Fps = 1000 / pctl(sorted, 99.9)
		if totalS > 0 {
			res.KPI.DisplayedFps = float64(displayed) / (totalS + res.KPI.NotPlayingS)
		}
		if gpuKnown > n/2 {
			res.KPI.GpuBoundPct = 100 * float64(gpuBound) / float64(gpuKnown)
		}
	}
	// ---- display pacing: PresentMon's time between display changes
	if d.pmc != nil && d.pmc.HasQpc {
		var playMs, allT, allMs []float64
		for _, f := range d.pmc.Frames {
			if f.Qpc < 0 || f.DisplayMs <= 0 || !inWin(f.Qpc) {
				continue
			}
			allT = append(allT, tsec(f.Qpc))
			allMs = append(allMs, f.DisplayMs)
			if playing(f.Qpc) {
				playMs = append(playMs, f.DisplayMs)
			}
		}
		if len(playMs) > 0 {
			p := pacingOf(playMs, totalS)
			res.Pacing = &p
			res.Timeline.DispT, res.Timeline.DispFps, res.Timeline.DispHeld = pacingSeconds(allT, allMs, p.RefreshHz)
		}
	}
	// timeline: every recorded frame (loading screens included, shaded in the report)
	if n := len(frames); n > 0 {
		// timeline buckets (max frame time per bucket so spikes survive downsampling)
		const maxPts = 12000
		step := 1
		if n > maxPts {
			step = (n + maxPts - 1) / maxPts
		}
		for i := 0; i < n; i += step {
			mx, gs, gc := 0.0, 0.0, 0
			for j := i; j < i+step && j < n; j++ {
				if frames[j].ms > mx {
					mx = frames[j].ms
				}
				if frames[j].gpu >= 0 {
					gs += frames[j].gpu
					gc++
				}
			}
			res.Timeline.T = append(res.Timeline.T, round3(tsec(frames[i].q)))
			res.Timeline.FrameMs = append(res.Timeline.FrameMs, round2(mx))
			if gc > 0 {
				res.Timeline.GpuMs = append(res.Timeline.GpuMs, round2(gs/float64(gc)))
			} else {
				res.Timeline.GpuMs = append(res.Timeline.GpuMs, -1)
			}
		}
	}

	// ---- VRAM (game's own use, from inside the game)
	type vs struct {
		q            int64
		used, budget float64
	}
	var vram []vs
	if d.hr != nil {
		for i := range d.hr.Recs {
			r := &d.hr.Recs[i]
			if r.Kind == hrec.KVram {
				q := d.hr.Header.Qpc(r.TUs)
				if inWin(q) {
					vram = append(vram, vs{q, float64(r.Hash) / 1048576, float64(r.DurUs)})
				}
			}
		}
	}
	sort.Slice(vram, func(i, j int) bool { return vram[i].q < vram[j].q })
	for _, v := range vram {
		res.Timeline.VramT = append(res.Timeline.VramT, round3(tsec(v.q)))
		res.Timeline.VramMB = append(res.Timeline.VramMB, math.Round(v.used))
		res.Timeline.BudgetMB = append(res.Timeline.BudgetMB, v.budget)
		if v.used > res.KPI.VramPeakMB {
			res.KPI.VramPeakMB = math.Round(v.used)
			res.KPI.VramBudgetMB = v.budget
		}
	}
	// ---- everything else on the game's graphics card: the whole card (NVIDIA driver) minus the
	// game's own (1.4.1; 1.4.0 added up the programs' own numbers, which count the game's memory again
	// for overlays and the desktop)
	type othS struct {
		q  int64
		mb float64
	}
	var others []othS
	var othPlay []float64
	for _, s := range d.samp {
		if s.VramUsedMB > 0 && s.GameDedicatedMB > 0 && inWin(s.Qpc) {
			o := othS{s.Qpc, math.Max(0, s.VramUsedMB-s.GameDedicatedMB)}
			others = append(others, o)
			if playing(s.Qpc) {
				othPlay = append(othPlay, o.mb)
			}
		}
	}
	othTyp, othMax := 0.0, 0.0
	if len(othPlay) > 0 {
		sort.Float64s(othPlay)
		othTyp, othMax = pctl(othPlay, 50), othPlay[len(othPlay)-1]
	}
	if len(others) > 0 && len(vram) > 0 {
		res.Timeline.VramOthersMB = make([]float64, len(vram))
		j := 0
		for i, v := range vram {
			for j+1 < len(others) && others[j+1].q <= v.q {
				j++
			}
			res.Timeline.VramOthersMB[i] = math.Round(others[j].mb)
		}
	}
	res.KPI.OthersVramMB = math.Round(othTyp)
	if _, gs := loadGpuApps(dir, inWin, playing, othTyp, othMax); gs != nil { // per program (1.4+)
		res.GpuApps = gs
		res.KPI.OthersVramMB = gs.OthersTypMB
	}
	vramAt := func(q int64) (used, budget float64, ok bool) {
		best := -1
		for i := range vram {
			if absI64(vram[i].q-q) < int64(1.5*d.freq) && (best < 0 || vram[i].used > vram[best].used) {
				best = i
			}
		}
		if best < 0 {
			return 0, 0, false
		}
		return vram[best].used, vram[best].budget, true
	}

	// ---- PC / GPU samples
	var samp []samples.Sample
	var throttleAll uint64
	for _, s := range d.samp {
		if inWin(s.Qpc) {
			samp = append(samp, s)
		}
	}
	hasCpu, hasRam := false, false // samples from Frame Lab 1.3+ have the CPU / Windows columns
	for _, s := range samp {
		if s.CpuClockMHz > 0 {
			hasCpu = true
		}
		if s.AvailRamMB > 0 {
			hasRam = true
		}
	}
	var ramPlay []float64
	for _, s := range samp {
		if hasRam {
			res.Timeline.RamFreeMB = append(res.Timeline.RamFreeMB, math.Round(s.AvailRamMB))
		}
		if !playing(s.Qpc) {
			continue
		}
		if s.AvailRamMB > 0 {
			ramPlay = append(ramPlay, s.AvailRamMB)
		}
		if s.CommitMB > res.KPI.CommitPeakMB {
			res.KPI.CommitPeakMB, res.KPI.CommitLimitMB = math.Round(s.CommitMB), math.Round(s.CommitLimitMB)
		}
	}
	if len(ramPlay) > 0 {
		srt := append([]float64(nil), ramPlay...)
		sort.Float64s(srt)
		res.KPI.RamFreeMinMB, res.KPI.RamFreeTypMB = math.Round(srt[0]), math.Round(pctl(srt, 50))
	}
	for _, s := range samp {
		res.Timeline.SampT = append(res.Timeline.SampT, round3(tsec(s.Qpc)))
		res.Timeline.ClockMHz = append(res.Timeline.ClockMHz, s.GpuClockMHz)
		res.Timeline.TempC = append(res.Timeline.TempC, s.GpuTempC)
		res.Timeline.PowerW = append(res.Timeline.PowerW, math.Round(s.GpuPowerW))
		res.Timeline.DiskMBs = append(res.Timeline.DiskMBs, round2(s.GameReadMBs))
		res.Timeline.CpuPct = append(res.Timeline.CpuPct, round2(s.GameCpuPct))
		res.Timeline.OtherGpu = append(res.Timeline.OtherGpu, round2(s.OtherGpuPct))
		if hasCpu {
			res.Timeline.CpuClock = append(res.Timeline.CpuClock, math.Round(s.CpuClockMHz))
			res.Timeline.CoresBusy = append(res.Timeline.CoresBusy, s.CoresBusy)
			res.Timeline.DpcPct = append(res.Timeline.DpcPct, round2(s.DpcPct+s.IntPct))
			res.Timeline.SharedMB = append(res.Timeline.SharedMB, math.Round(s.GameSharedMB))
		}
		if s.GpuTempC > res.KPI.GpuTempMax {
			res.KPI.GpuTempMax = s.GpuTempC
		}
		throttleAll |= s.Throttle & samples.ThrottleSlowdownMask
	}
	res.KPI.ThrottleText = samples.ThrottleText(throttleAll)
	res.KPI.ThrottleBad = samples.Serious(throttleAll)
	res.KPI.SlowdownKnown = meta.GpuVendor == "" || meta.GpuVendor == "nvidia" // sessions before 1.1: NVIDIA only
	// readings this card's driver doesn't give stay out of the charts instead of showing as 0
	hasData := func(v []float64) bool {
		for _, x := range v {
			if x > 0 {
				return true
			}
		}
		return false
	}
	if !hasData(res.Timeline.ClockMHz) {
		res.Timeline.ClockMHz = nil
	}
	if !hasData(res.Timeline.TempC) {
		res.Timeline.TempC = nil
	}
	if !hasData(res.Timeline.PowerW) {
		res.Timeline.PowerW = nil
	}
	sampAround := func(q0, q1 int64, sp *Spike) (disk, cpu, core float64, thr uint64, other string) {
		pad := int64(0.5 * d.freq)
		otherPct := 0.0
		for _, s := range samp {
			if s.Qpc >= q0-pad && s.Qpc <= q1+pad {
				sp.DpcPct = math.Max(sp.DpcPct, round2(s.DpcPct+s.IntPct))
				sp.SharedMB = math.Max(sp.SharedMB, math.Round(s.GameSharedMB))
				sp.PagesSec = math.Max(sp.PagesSec, math.Round(s.PagesSec))
				if s.AvailRamMB > 0 && (sp.RamFreeMB == 0 || s.AvailRamMB < sp.RamFreeMB) {
					sp.RamFreeMB = math.Round(s.AvailRamMB)
				}
				if s.CpuClockMHz > 0 && (sp.CpuClock == 0 || s.CpuClockMHz < sp.CpuClock) {
					sp.CpuClock = math.Round(s.CpuClockMHz)
				}
				disk = math.Max(disk, s.GameReadMBs)
				cpu = math.Max(cpu, s.GameCpuPct)
				core = math.Max(core, s.MaxCorePct)
				thr |= s.Throttle & samples.ThrottleSlowdownMask
				if s.OtherGpuPct > otherPct && s.OtherGpuPct >= 5 {
					otherPct = s.OtherGpuPct
					other = fmt.Sprintf("%s %.0f%%", s.OtherGpuName, s.OtherGpuPct)
				}
			}
		}
		return
	}

	// ---- markers
	if d.hr != nil {
		for i := range d.hr.Recs {
			r := &d.hr.Recs[i]
			if r.Kind != hrec.KMark {
				continue
			}
			q := d.hr.Header.Qpc(r.TUs)
			switch r.Extra {
			case hrec.MarkKey:
				if inWin(q) {
					res.Markers = append(res.Markers, Marker{round3(tsec(q)), "mark"})
				}
			case hrec.MarkCaptureStart:
				res.Markers = append(res.Markers, Marker{round3(tsec(q)), "start"})
			case hrec.MarkCaptureStop:
				res.Markers = append(res.Markers, Marker{round3(tsec(q)), "stop"})
			}
		}
	}

	// ---- engine events (sorted by start) for overlap queries
	type ev struct {
		q0, q1 int64
		r      *hrec.Rec
	}
	var evs []ev
	var maxDurQ int64
	if d.hr != nil {
		for i := range d.hr.Recs {
			r := &d.hr.Recs[i]
			// per-mod script time (40-43) is 50 ms bucket totals, not events: it has its own layers
			if r.Kind == hrec.KFrame || r.Kind == hrec.KMark || r.Kind == hrec.KVram || r.Kind >= hrec.KScript {
				continue
			}
			q0 := d.hr.Header.Qpc(r.TUs)
			q1 := q0 + int64(float64(r.DurUs)*d.freq/1e6)
			evs = append(evs, ev{q0, q1, r})
			if q1-q0 > maxDurQ {
				maxDurQ = q1 - q0
			}
		}
		sort.Slice(evs, func(i, j int) bool { return evs[i].q0 < evs[j].q0 })
	}
	overlapping := func(a, b int64, fn func(e *ev, ovMs float64)) {
		lo := sort.Search(len(evs), func(i int) bool { return evs[i].q0 >= a-maxDurQ })
		for i := lo; i < len(evs) && evs[i].q0 < b; i++ {
			e := &evs[i]
			if e.q1 <= a && !(e.q1 == e.q0 && e.q0 >= a) {
				continue
			}
			s, t := maxI64(e.q0, a), minI64(e.q1, b)
			ov := float64(t-s) * 1000 / d.freq
			if ov < 0 {
				ov = 0
			}
			fn(e, ov)
		}
	}

	// script buckets: owner time inside [a, b] (Unix ms)
	scriptIn := func(r *ScriptLayer, ua, ub float64) map[string]float64 {
		out := map[string]float64{}
		if r == nil {
			return out
		}
		lo := sort.Search(len(r.Buckets), func(i int) bool { return r.Buckets[i].EndUnix > ua })
		for i := lo; i < len(r.Buckets) && r.Buckets[i].StartUnix < ub; i++ {
			bk := &r.Buckets[i]
			w := bk.EndUnix - bk.StartUnix
			if w <= 0 {
				continue
			}
			ov := math.Min(bk.EndUnix, ub) - math.Max(bk.StartUnix, ua)
			if ov <= 0 {
				continue
			}
			out[bk.Owner] += bk.ExclusiveMs * ov / w
		}
		return out
	}
	for _, r := range []*ScriptLayer{d.reds, d.cetl} {
		if r != nil {
			sort.Slice(r.Buckets, func(i, j int) bool { return r.Buckets[i].StartUnix < r.Buckets[j].StartUnix })
		}
	}

	// ---- spikes: merge slow frames that touch each other
	type spk struct {
		q0, q1 int64
		ms     float64
		gpu    float64
		n      int
	}
	var spks []spk
	for _, f := range play {
		if f.ms < spikeMs {
			continue
		}
		q1 := f.q + int64(f.ms*d.freq/1000)
		if n := len(spks); n > 0 && f.q-spks[n-1].q1 < int64(0.02*d.freq) {
			s := &spks[n-1]
			s.q1 = maxI64(s.q1, q1)
			if f.ms > s.ms {
				s.ms, s.gpu = f.ms, f.gpu
			}
			s.n++
			continue
		}
		spks = append(spks, spk{f.q, q1, f.ms, f.gpu, 1})
	}
	res.KPI.Spikes = len(spks)

	// ---- screen resets: what each one did to the game's memory, and how long frames crawled
	type rst struct {
		q, end int64
		idx    int // in res.Timeline.Resets
		play   bool
	}
	var resets []rst
	if len(disp) > 0 && d.anchorQ != 0 {
		uStart, uEnd := d.qpcToUnix(windows[0][0])-5000, d.qpcToUnix(windows[len(windows)-1][1])+5000
		var kept []DisplayEvent
		for _, e := range disp {
			if e.UnixMs >= uStart && e.UnixMs <= uEnd {
				kept = append(kept, e)
			}
		}
		if !dispFromFile && len(kept) > 0 {
			WriteDisplay(dir, kept) // keep them with the session (the game's logs get overwritten)
		}
		median := func(v []float64) float64 {
			if len(v) == 0 {
				return 0
			}
			sort.Float64s(v)
			return v[len(v)/2]
		}
		loc := meta.Created.Location()
		sec := func(x float64) int64 { return int64(x * d.freq) }
		for _, e := range kept {
			q := d.unixToQpc(e.UnixMs)
			m := ResetMark{T: round3(tsec(q)), Clock: time.UnixMilli(int64(e.UnixMs)).In(loc).Format("15:04:05"),
				Source: e.Source, Text: e.Text, Playing: playing(q)}
			var vb, sb []float64
			for _, v := range vram {
				if v.q >= q-sec(6) && v.q <= q-sec(1) {
					vb = append(vb, v.used)
				}
				if v.q >= q && v.q <= q+sec(10) {
					m.VramAfter = math.Max(m.VramAfter, v.used)
				}
			}
			for _, sm := range samp {
				if sm.Qpc >= q-sec(6) && sm.Qpc <= q-sec(1) {
					sb = append(sb, sm.GameSharedMB)
				}
				if sm.Qpc >= q && sm.Qpc <= q+sec(15) {
					m.SharedAfter = math.Max(m.SharedAfter, sm.GameSharedMB)
					if sm.AvailRamMB > 0 && (m.RamFreeMin == 0 || sm.AvailRamMB < m.RamFreeMin) {
						m.RamFreeMin = sm.AvailRamMB
					}
				}
			}
			m.VramBefore, m.SharedBefore = math.Round(median(vb)), math.Round(median(sb))
			m.VramAfter, m.SharedAfter, m.RamFreeMin = math.Round(m.VramAfter), math.Round(m.SharedAfter), math.Round(m.RamFreeMin)
			// the extra memory stays until the game's VRAM is back near where it was (often the next reset)
			back := q + sec(3)
			if m.VramBefore > 0 && m.VramAfter > m.VramBefore+500 {
				// back = from where the game's VRAM stays near where it was for 3 s (it dips now and then before that)
				lim := q + sec(300)
				back = lim
				for i, v := range vram {
					if v.q <= q+sec(3) || v.used > m.VramBefore+300 {
						continue
					}
					if v.q > lim {
						break
					}
					var w []float64
					for j := i; j < len(vram) && vram[j].q <= v.q+sec(3); j++ {
						w = append(w, vram[j].used)
					}
					if median(w) <= m.VramBefore+300 {
						back = v.q
						break
					}
				}
			}
			m.EndT = round3(tsec(back))
			end := back + sec(10) // memory moving back in, for a few more seconds
			for _, sk := range spks {
				if sk.q0 >= q-sec(2) && sk.q0 <= q+sec(2) {
					m.SlowS += float64(sk.q1-sk.q0) / d.freq
				}
			}
			m.SlowS = round2(m.SlowS)
			resets = append(resets, rst{q, end, len(res.Timeline.Resets), m.Playing})
			res.Timeline.Resets = append(res.Timeline.Resets, m)
			if m.Playing {
				res.KPI.Resets++
			}
		}
		// pair a reset that took memory with the later one that gave it back
		rm := res.Timeline.Resets
		for i := range rm {
			if !(rm[i].VramBefore > 0 && rm[i].VramAfter > rm[i].VramBefore+500) {
				continue
			}
			for j := i + 1; j < len(rm); j++ {
				if rm[j].T > rm[i].T && rm[j].T <= rm[i].EndT+2 {
					rm[i].EndedBy, rm[j].Release = rm[j].Clock, true
					break
				}
			}
		}
	}
	// VRAM peak in normal play: leave out the time a screen reset held extra memory
	res.KPI.VramPlayPeakMB = res.KPI.VramPeakMB
	if len(resets) > 0 {
		res.KPI.VramPlayPeakMB = 0
		for _, v := range vram {
			if !playing(v.q) {
				continue
			}
			in := false
			for _, rs := range resets {
				if v.q >= rs.q-int64(d.freq) && v.q <= rs.end {
					in = true
					break
				}
			}
			if !in && v.used > res.KPI.VramPlayPeakMB {
				res.KPI.VramPlayPeakMB = math.Round(v.used)
			}
		}
	}
	{
		det := "no ReShade or CET log in the game folder, so screen resets can't be seen"
		ok := dispLogs
		if dispLogs {
			from := "read from ReShade's / CET's log after the session"
			if !dispFromFile {
				from = "read from the game's ReShade / CET log when this report was made"
			}
			det = fmt.Sprintf("%d while playing (%s)", res.KPI.Resets, from)
		}
		res.Layers = append(res.Layers, LayerState{"Screen resets", ok, det})
	}

	// names wanted for items
	wantNames := map[uint64]bool{}
	modArchive := func(h uint64) string {
		if env.Mods == nil {
			return ""
		}
		return env.Mods.ByHash[h]
	}
	modLabelForArchive := func(rel string) (string, string) {
		if rel == "" {
			return "", ""
		}
		if env.Library != nil {
			if lab, z := env.Library.Find("archive:" + strings.ToLower(filepath.Base(rel))); lab != "" {
				return lab, z
			}
		}
		return modmap.ArchiveLabel(rel), ""
	}

	causeCount := map[string]*CauseCount{}
	driverSpk := map[string]int{}        // stutters put down to each driver
	modSpike := map[string]*[2]float64{} // mod key -> [spikes, ms]
	addModSpike := func(key string, ms float64) {
		m := modSpike[key]
		if m == nil {
			m = &[2]float64{}
			modSpike[key] = m
		}
		m[0]++
		m[1] += ms
	}
	markQ := []int64{}
	for _, m := range res.Markers {
		if m.Kind == "mark" {
			markQ = append(markQ, t0+int64(m.T*d.freq))
		}
	}

	for i, s := range spks {
		L := float64(s.q1-s.q0) * 1000 / d.freq
		sp := Spike{ID: i + 1, T: round3(tsec(s.q0)), Ms: round2(s.ms), Frames: s.n, GpuMs: s.gpu}
		groupMs := map[string]float64{}
		kindMs := map[string]float64{}    // label -> ms (for parts)
		spikeMods := map[string]float64{} // mod key -> ms inside this spike
		var shaderMax float64
		var items []Item
		overlapping(s.q0, s.q1, func(e *ev, ov float64) {
			k := e.r.Kind
			main := e.r.Main()
			if hrec.IsShader(k) {
				if ov > shaderMax {
					shaderMax = ov
				}
				kindMs[label(k, main)] += ov
			} else if hrec.IsGpuMem(k) || k == hrec.KPsoLib {
				kindMs[label(k, main)] += ov
				if main {
					groupMs["world"] += ov * 0.5
				}
			} else {
				kindMs[label(k, main)] += ov
				if main {
					groupMs["world"] += ov
				}
			}
			if ov >= 0.5 {
				it := Item{Kind: hrec.KindName[k], Ms: round2(ov), Main: main}
				if hrec.IsPath(k) && e.r.Hash != 0 {
					it.Label = fmt.Sprintf("#%016x", e.r.Hash)
					wantNames[e.r.Hash] = true
					if a := modArchive(e.r.Hash); a != "" {
						lab, _ := modLabelForArchive(a)
						it.Mod = lab
						spikeMods["archive:"+strings.ToLower(filepath.Base(a))] += ov
					}
				} else if (k == hrec.KPsoGfx || k == hrec.KPsoComp) && e.r.Hash != 0 {
					it.Label = fmt.Sprintf("shader %016x", e.r.Hash)
				}
				items = append(items, it)
			}
		})
		groupMs["shader"] = shaderMax
		if groupMs["world"] > L {
			groupMs["world"] = L
		}
		ua, ub := d.qpcToUnix(s.q0), d.qpcToUnix(s.q1)
		for owner, ms := range scriptIn(d.reds, ua, ub) {
			groupMs["redscript"] += ms
			if ms >= 0.3 {
				items = append(items, Item{Label: owner, Kind: "REDscript", Ms: round2(ms), Main: true, Mod: owner})
			}
			if ms >= 0.5 {
				spikeMods["reds:"+strings.ToLower(owner)] += ms
			}
		}
		for owner, ms := range scriptIn(d.cetl, ua, ub) {
			groupMs["cet"] += ms
			if ms >= 0.3 {
				items = append(items, Item{Label: owner, Kind: "CET Lua", Ms: round2(ms), Main: true, Mod: owner})
			}
			if ms >= 0.5 {
				spikeMods["cet:"+strings.ToLower(owner)] += ms
			}
		}
		for k, ms := range spikeMods {
			addModSpike(k, ms)
		}
		if s.gpu >= 0 && s.gpu >= 0.8*s.ms {
			groupMs["gpu"] = s.gpu
		}
		if u, b, ok := vramAt(s.q0); ok {
			sp.VramMB, sp.BudgetMB = math.Round(u), b
			if b > 0 && u > b {
				groupMs["vram"] = 0.6 * L
			}
		}
		var thr uint64
		sp.DiskMBs, sp.CpuPct, sp.MaxCore, thr, sp.OtherGpu = sampAround(s.q0, s.q1, &sp)
		var spReset *ResetMark
		for _, rs := range resets {
			if !rs.play {
				continue
			}
			if rs.q >= s.q0-int64(2*d.freq) && rs.q <= s.q0+int64(2*d.freq) {
				spReset = &res.Timeline.Resets[rs.idx]
				groupMs["reset"] = L
				sp.Reset = spReset.Clock + ": " + spReset.Text
				break
			}
			if s.q0 > rs.q && s.q0 < rs.end {
				sp.AfterReset = true
				sp.Reset = res.Timeline.Resets[rs.idx].Clock + ": " + res.Timeline.Resets[rs.idx].Text
			}
		}
		if sp.RamFreeMB > 0 && sp.RamFreeMB < 500 {
			groupMs["ram"] = math.Max(groupMs["ram"], 0.7*L)
		}
		topDriver := ""
		if wl != nil {
			var drvMs, pagMs float64
			sp.Win, drvMs, _, pagMs, topDriver = wl.in(s.q0, s.q1)
			if drvMs > 0 {
				groupMs["driver"] = math.Min(L, drvMs)
			}
			if pagMs > 0 {
				groupMs["paging"] = math.Min(L, pagMs)
			}
		}
		if samples.Serious(thr) {
			sp.Throttle = samples.ThrottleText(thr)
		}
		if sp.DiskMBs >= 300 {
			groupMs["disk"] = math.Min(L, sp.DiskMBs/1000*L) // rough: heavy reads during the frame
		}
		// the game reserving GPU memory on another thread while the frame waits: with VRAM nearly full
		// that is what holds the frame (the wait itself isn't recorded, the allocation is)
		memMs := 0.0
		for lab, ms := range kindMs {
			if strings.HasPrefix(lab, "GPU memory heap") || strings.HasPrefix(lab, "GPU texture/buffer") {
				memMs += ms
			}
		}
		if memMs >= 0.4*L {
			groupMs["vram"] = math.Max(groupMs["vram"], memMs)
		}
		for _, mq := range markQ {
			if mq >= s.q0-int64(3*d.freq) && mq <= s.q1+int64(5*d.freq) {
				sp.NearMark = true
			}
		}
		sp.Focus = inFocus(s.q0, s.q1)

		// parts (what the evidence says, in ms)
		for lab, ms := range kindMs {
			if ms >= 0.2 {
				sp.Parts = append(sp.Parts, Part{Label: lab, Ms: round2(ms), Group: groupOfLabel(lab)})
			}
		}
		if v := groupMs["redscript"]; v >= 0.2 {
			sp.Parts = append(sp.Parts, Part{"REDscript mods", round2(v), "redscript"})
		}
		if v := groupMs["cet"]; v >= 0.2 {
			sp.Parts = append(sp.Parts, Part{"CET Lua mods", round2(v), "cet"})
		}
		if s.gpu >= 0 {
			sp.Parts = append(sp.Parts, Part{"GPU busy on that frame", round2(s.gpu), "gpu"})
		}
		sort.Slice(sp.Parts, func(i, j int) bool { return sp.Parts[i].Ms > sp.Parts[j].Ms })
		sort.Slice(items, func(i, j int) bool { return items[i].Ms > items[j].Ms })
		if len(items) > 12 {
			items = items[:12]
		}
		sp.Items = items

		// cause = biggest explained group
		best, bestMs := "", 0.0
		for _, g := range []string{"shader", "world", "redscript", "cet", "gpu", "vram", "disk", "ram", "driver", "paging", "reset"} {
			if groupMs[g] > bestMs {
				best, bestMs = g, groupMs[g]
			}
		}
		if bestMs < 0.25*L {
			best = "unexplained"
		}
		sp.Group = best
		sp.Cause = causeLabel[best]
		sp.Verdict = verdict(best, groupMs, L, sp)
		if best == "reset" && spReset != nil {
			sp.Verdict = resetVerdict(spReset)
		}
		if best == "driver" || best == "paging" {
			sp.Verdict = winVerdict(best, sp.Win, topDriver)
			if best == "driver" {
				driverSpk[topDriver]++
			}
		}
		cc := causeCount[best]
		if cc == nil {
			cc = &CauseCount{Cause: causeLabel[best], Group: best}
			causeCount[best] = cc
		}
		cc.Count++
		cc.TotalMs += L
		res.Spikes = append(res.Spikes, sp)
	}
	// Windows event recorder: the whole session, and how many stutters it explained
	if wl != nil {
		ws := wl.summary(playing, totalS)
		for i := range ws.Drivers {
			ws.Drivers[i].Spikes = driverSpk[ws.Drivers[i].Name]
		}
		if c := causeCount["driver"]; c != nil {
			ws.DriverSpk = c.Count
		}
		if c := causeCount["paging"]; c != nil {
			ws.PagingSpk = c.Count
		}
		if c := causeCount["unexplained"]; c != nil {
			ws.Unexplained = c.Count
		}
		res.Windows = ws
		det := "drivers' interrupt / DPC time, hard page faults and disk use per program, in 10 ms steps"
		if wl.stats != nil {
			var n uint64
			for _, v := range wl.stats.Events {
				n += v
			}
			det = fmt.Sprintf("%s events recorded", fmtCount(n))
			if wl.stats.EventsLost > 0 {
				det += fmt.Sprintf(", %d lost", wl.stats.EventsLost)
			}
			det += ": driver interrupts / DPCs, hard page faults, disk use per program"
			if wl.stats.Error != "" {
				det += " (error: " + wl.stats.Error + ")"
			}
		}
		res.Layers = append(res.Layers, LayerState{"Windows events", true, det})
	} else {
		res.Layers = append(res.Layers, LayerState{"Windows events", false, "not recorded in this session (needs Frame Lab as administrator and Windows events on in Settings)"})
	}
	if res.GpuApps != nil {
		res.Layers = append(res.Layers, LayerState{"Other programs on the graphics card", true, fmt.Sprintf("every 2 s: %d program%s held 20 MB or more", len(res.GpuApps.Apps), plural(len(res.GpuApps.Apps)))})
	} else {
		res.Layers = append(res.Layers, LayerState{"Other programs on the graphics card", false, "not recorded in this session"})
	}
	for _, g := range causeOrder {
		if c := causeCount[g]; c != nil {
			c.Pct = round2(100 * float64(c.Count) / float64(len(spks)))
			c.TotalMs = math.Round(c.TotalMs)
			res.Causes = append(res.Causes, *c)
		}
	}
	sort.SliceStable(res.Causes, func(i, j int) bool { return res.Causes[i].Count > res.Causes[j].Count })

	// ---- engine table + shaders + per-archive load totals
	type kacc struct {
		n, main, o5, o16 int
		sum, max         float64
		uniq             map[uint64]bool
	}
	kinds := map[uint8]*kacc{}
	type macc struct {
		files map[uint64]bool
		ms    float64
	}
	archLoads := map[string]*macc{}
	sh := &res.Shaders
	warmupQ := t0 + int64(120*d.freq)
	if len(play) > 0 && len(notPlay) > 0 {
		warmupQ = play[0].q + int64(120*d.freq) // two minutes after V is first in the game
	}
	type slow struct {
		ms   float64
		k    uint8
		h    uint64
		main bool
	}
	var slowSh []slow
	for i := range evs {
		e := &evs[i]
		if !playing(e.q0) {
			continue
		}
		r := e.r
		a := kinds[r.Kind]
		if a == nil {
			a = &kacc{uniq: map[uint64]bool{}}
			kinds[r.Kind] = a
		}
		ms := float64(r.DurUs) / 1000
		a.n++
		a.sum += ms
		a.max = math.Max(a.max, ms)
		if r.Main() {
			a.main++
		}
		if ms >= 5 {
			a.o5++
		}
		if ms >= 16 {
			a.o16++
		}
		if len(a.uniq) < 200000 {
			a.uniq[r.Hash] = true
		}
		if hrec.IsShader(r.Kind) {
			sh.Recorded = true
			sh.Total++
			if r.Flags&4 != 0 {
				sh.CachedBlobs++
			}
			if r.Kind == hrec.KStateObj || r.Kind == hrec.KAddStateObj {
				sh.RtBuilds++
				sh.RtSlowestMs = math.Max(sh.RtSlowestMs, ms)
			}
			if e.q0 > warmupQ {
				sh.AfterWarmup++
				if ms >= 5 {
					sh.SlowAfter++
				}
			}
			if ms >= 2 {
				slowSh = append(slowSh, slow{ms, r.Kind, r.Hash, r.Main()})
			}
		}
		if r.Kind == hrec.KPsoLib {
			sh.CacheLookups++
			if r.Failed() {
				sh.CacheMisses++
			}
		}
		if (r.Kind == hrec.KDeser || r.Kind == hrec.KLoad) && r.Hash != 0 {
			if arch := modArchive(r.Hash); arch != "" {
				m := archLoads[arch]
				if m == nil {
					m = &macc{files: map[uint64]bool{}}
					archLoads[arch] = m
				}
				m.files[r.Hash] = true
				if r.Kind == hrec.KDeser {
					m.ms += ms
				}
			}
		}
	}
	for _, k := range []uint8{hrec.KLoad, hrec.KDeser, hrec.KSerLoad, hrec.KLoaderUpdate, hrec.KSector, hrec.KEntity,
		hrec.KPsoGfx, hrec.KPsoComp, hrec.KPsoStream, hrec.KStateObj, hrec.KAddStateObj, hrec.KPsoLib,
		hrec.KResCommit, hrec.KResPlaced, hrec.KHeap} {
		a := kinds[k]
		if a == nil {
			continue
		}
		res.Engine = append(res.Engine, KindRow{Kind: hrec.KindName[k], Count: a.n, Unique: len(a.uniq),
			MainPct: round2(100 * float64(a.main) / float64(a.n)), AvgMs: round3(a.sum / float64(a.n)), MaxMs: round2(a.max),
			Over5: a.o5, Over16: a.o16})
	}
	sort.Slice(slowSh, func(i, j int) bool { return slowSh[i].ms > slowSh[j].ms })
	for i, s := range slowSh {
		if i >= 15 {
			break
		}
		lab := hrec.KindName[s.k]
		if s.h != 0 && (s.k == hrec.KPsoGfx || s.k == hrec.KPsoComp) {
			lab = fmt.Sprintf("shader %016x", s.h)
		}
		sh.Slowest = append(sh.Slowest, Item{Label: lab, Kind: hrec.KindName[s.k], Ms: round2(s.ms), Main: s.main})
	}
	if d.hr != nil && !sh.Recorded {
		if d.hr.LatestHooks()&(1<<8) == 0 {
			warn("HitchRecorder's shader recording was off or could not hook D3D12 this session (check the RED4ext log).")
		}
	}

	// ---- mods table
	rows := map[string]*ModRow{}
	rowFor := func(key, fallback string) *ModRow {
		name, zip := "", ""
		if env.Library != nil {
			name, zip = env.Library.Find(key)
		}
		if name == "" {
			name = fallback
		}
		id := strings.ToLower(name)
		r := rows[id]
		if r == nil {
			r = &ModRow{Name: name, Zip: zip}
			rows[id] = r
		}
		if m := modSpike[key]; m != nil {
			r.InSpikes += int(m[0])
			r.SpikeMs += m[1]
		}
		return r
	}
	addKind := func(r *ModRow, k string) {
		for _, x := range r.Kinds {
			if x == k {
				return
			}
		}
		r.Kinds = append(r.Kinds, k)
	}
	if d.reds != nil {
		res.KPI.ScriptRedMsS = round2(d.reds.MsPerSec)
		for _, o := range d.reds.Owners {
			if o.MsPerSec < 0.05 {
				continue
			}
			r := rowFor("reds:"+strings.ToLower(o.Owner), o.Owner)
			r.RedMsS += o.MsPerSec
			addKind(r, "REDscript")
			switch o.Owner {
			case "game scripts":
				r.Note = "the game's own scripts, incl. what mods' wrappers call"
			case "scripts without a source file":
				r.Note = "incl. game functions CET mods observe"
			}
		}
	}
	if d.cetl != nil {
		res.KPI.ScriptCetMsS = round2(d.cetl.MsPerSec)
		for _, o := range d.cetl.Owners {
			if o.MsPerSec < 0.05 {
				continue
			}
			r := rowFor("cet:"+strings.ToLower(o.Owner), o.Owner)
			r.CetMsS += o.MsPerSec
			addKind(r, "CET Lua")
			if o.Owner == "CET itself" {
				r.Note = "CET's console and its own scripts"
			}
		}
	}
	for arch, m := range archLoads {
		lab, _ := modLabelForArchive(arch)
		r := rowFor("archive:"+strings.ToLower(filepath.Base(arch)), lab)
		r.Files += len(m.files)
		r.LoadMs += m.ms
		if env.Mods != nil {
			var b float64
			for h := range m.files {
				b += float64(env.Mods.Size[h])
			}
			r.MB = round2(r.MB + b/(1<<20))
		}
		addKind(r, "files")
	}
	for _, r := range rows {
		r.RedMsS, r.CetMsS, r.LoadMs, r.SpikeMs = round3(r.RedMsS), round3(r.CetMsS), round2(r.LoadMs), round2(r.SpikeMs)
		loadPerS := 0.0
		if totalS > 0 {
			loadPerS = r.LoadMs / totalS
		}
		r.Score = round3(r.RedMsS + r.CetMsS + loadPerS + r.SpikeMs/math.Max(totalS, 1)*10)
		res.Mods = append(res.Mods, *r)
	}
	sort.Slice(res.Mods, func(i, j int) bool { return res.Mods[i].Score > res.Mods[j].Score })
	if len(res.Mods) > 150 {
		res.Mods = res.Mods[:150]
	}

	// ---- file names
	if env.Names != "" && env.Names.Exists() && len(wantNames) > 0 {
		names, _ := env.Names.Resolve(wantNames)
		for i := range res.Spikes {
			for j := range res.Spikes[i].Items {
				it := &res.Spikes[i].Items[j]
				if strings.HasPrefix(it.Label, "#") {
					var h uint64
					fmt.Sscanf(it.Label[1:], "%x", &h)
					if n, ok := names[h]; ok {
						it.Label = n
					} else if it.Mod != "" {
						it.Label = "file from " + it.Mod
					}
				}
			}
		}
	} else {
		for i := range res.Spikes {
			for j := range res.Spikes[i].Items {
				it := &res.Spikes[i].Items[j]
				if strings.HasPrefix(it.Label, "#") && it.Mod != "" {
					it.Label = "file from " + it.Mod
				}
			}
		}
	}

	if res.Timeline.T == nil {
		res.Timeline.T = []float64{}
	}
	res.Advice = Advise(res)
	b, _ := json.Marshal(res)
	os.WriteFile(filepath.Join(dir, FileAnalysis), b, 0o644)
	return res, nil
}

// captureWindows returns the recorded stretches as QPC pairs.
func (d *data) captureWindows() [][2]int64 {
	var w [][2]int64
	if d.hr != nil {
		var open int64 = -1
		for i := range d.hr.Recs {
			r := &d.hr.Recs[i]
			if r.Kind != hrec.KMark {
				continue
			}
			q := d.hr.Header.Qpc(r.TUs)
			if r.Extra == hrec.MarkCaptureStart {
				open = q
			} else if r.Extra == hrec.MarkCaptureStop && open >= 0 {
				w = append(w, [2]int64{open, q})
				open = -1
			}
		}
		if open >= 0 { // game closed while recording: up to the last thing recorded
			last := open
			for i := range d.hr.Recs {
				r := &d.hr.Recs[i]
				end := r.TUs
				if r.Kind != hrec.KVram {
					end += uint64(r.DurUs)
				}
				if q := d.hr.Header.Qpc(end); q > last {
					last = q
				}
			}
			w = append(w, [2]int64{open, last})
		}
		if len(w) == 0 { // always-on recording: from the first to the last frame
			var a, b int64 = -1, -1
			for i := range d.hr.Recs {
				r := &d.hr.Recs[i]
				if r.Kind == hrec.KFrame {
					q := d.hr.Header.Qpc(r.TUs)
					if a < 0 {
						a = q
					}
					b = q + int64(float64(r.DurUs)*d.freq/1e6)
				}
			}
			if a >= 0 {
				w = append(w, [2]int64{a, b})
			}
		}
	}
	if len(w) == 0 && d.reds != nil && len(d.reds.Segments) > 0 {
		for _, s := range d.reds.Segments {
			w = append(w, [2]int64{d.unixToQpc(s[0]), d.unixToQpc(s[1])})
		}
	}
	if len(w) == 0 && d.pmc != nil && d.pmc.HasQpc && len(d.pmc.Frames) > 1 {
		w = append(w, [2]int64{d.pmc.Frames[0].Qpc, d.pmc.Frames[len(d.pmc.Frames)-1].Qpc})
	}
	return w
}

func (d *data) layerStates(win [][2]int64, marked bool) []LayerState {
	var out []LayerState
	if d.hr != nil {
		h := d.hr.LatestHooks()
		det := fmt.Sprintf("HitchRecorder %s", d.hr.Header.Version3())
		if h&(1<<8) != 0 {
			det += ", engine + shader + GPU memory"
		} else {
			det += ", engine only (no shader hooks)"
		}
		out = append(out, LayerState{"Engine recorder", true, det})
	} else {
		out = append(out, LayerState{"Engine recorder", false, "no HitchRecorder recording in this session"})
	}
	if d.pmc != nil {
		det := fmt.Sprintf("%d frames", len(d.pmc.Frames))
		if !d.pmc.HasGPU {
			det += ", no GPU busy column"
		}
		out = append(out, LayerState{"Frames + GPU time (PresentMon)", d.pmc.HasQpc, det})
	} else {
		out = append(out, LayerState{"Frames + GPU time (PresentMon)", false, "PresentMon did not record (needs admin; check the PresentMon path)"})
	}
	if d.samp != nil {
		out = append(out, LayerState{"GPU health + PC side", true, fmt.Sprintf("%d samples", len(d.samp))})
	} else {
		out = append(out, LayerState{"GPU health + PC side", false, "no samples"})
	}
	if d.reds != nil {
		mods := 0
		for _, o := range d.reds.Owners {
			if o.Owner != "game scripts" && o.Owner != "scripts without a source file" {
				mods++
			}
		}
		det := fmt.Sprintf("%.1f ms/s script time in the F11 parts, %d mods + the game's own scripts", d.reds.MsPerSec, mods)
		out = append(out, LayerState{"REDscript per mod", true, det})
	} else {
		det := "not recorded in this session"
		if d.hr != nil && d.hr.Header.CaptureMode == 2 && !marked {
			det = "only measured in parts marked with F11; none this session"
		}
		out = append(out, LayerState{"REDscript per mod", false, det})
	}
	if d.cetl != nil {
		mods := 0
		for _, o := range d.cetl.Owners {
			if o.Owner != "CET itself" {
				mods++
			}
		}
		det := fmt.Sprintf("%.1f ms/s Lua time in the F11 parts, %d CET mods + CET itself", d.cetl.MsPerSec, mods)
		out = append(out, LayerState{"CET Lua per mod", true, det})
	} else {
		det := "not recorded in this session"
		if d.hr != nil && d.hr.Header.CaptureMode == 2 && !marked {
			det = "only measured in parts marked with F11; none this session"
		}
		out = append(out, LayerState{"CET Lua per mod", false, det})
	}
	return out
}

// ---------------------------------------------------------------- wording

var causeOrder = []string{"reset", "ram", "driver", "paging", "shader", "world", "redscript", "cet", "gpu", "vram", "disk", "unexplained"}

var causeLabel = map[string]string{
	"shader":      "shader / RT pipeline build",
	"world":       "world streaming + entity spawns",
	"redscript":   "REDscript mods",
	"cet":         "CET Lua mods",
	"gpu":         "GPU bound (heavy frame)",
	"vram":        "VRAM full (GPU memory)",
	"disk":        "disk reads",
	"ram":         "system RAM full",
	"driver":      "driver (interrupts / DPCs)",
	"paging":      "game memory read back from disk",
	"reset":       "screen reset (full screen re-applied)",
	"unexplained": "not explained by recorded work",
}

func fmtCount(n uint64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f million", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0f thousand", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// what the Windows events say about one spike
func winVerdict(g string, items []WinItem, topDriver string) string {
	for _, it := range items {
		if g == "driver" && it.Kind == "driver" && it.Name == topDriver {
			what := ""
			if it.What != "" {
				what = " (" + it.What + ")"
			}
			return fmt.Sprintf("The driver %s%s kept a CPU core in interrupts / DPCs for %.1f ms during this frame (longest single run %.1f ms). Drivers run ahead of every program on that core, so the game's thread there had to wait.",
				it.Name, what, it.Ms, it.MaxMs)
		}
		if g == "paging" && it.Kind == "paging" && strings.EqualFold(it.Name, gameExe) {
			return fmt.Sprintf("The game waited on %d hard page faults: %.1f MB of its own memory read back from disk (%.0f ms in all). Windows had moved that memory out of RAM, usually because RAM was short.",
				it.Count, it.MB, it.Ms)
		}
	}
	return "Windows events cover most of this frame."
}

// what a screen reset did, in one line
func resetVerdict(m *ResetMark) string {
	gb := func(mb float64) string { return fmt.Sprintf("%.1f GB", mb/1024) }
	v := fmt.Sprintf("At %s %s (%s's log).", m.Clock, m.Text, m.Source)
	if m.VramBefore > 0 && m.VramAfter > m.VramBefore+300 {
		v += fmt.Sprintf(" Its VRAM went from %s to %s", gb(m.VramBefore), gb(m.VramAfter))
		if m.SharedAfter > m.SharedBefore+300 {
			v += fmt.Sprintf(", and %s of its GPU memory had to go into system RAM", gb(m.SharedAfter))
		}
		v += "."
	}
	if m.RamFreeMin > 0 && m.RamFreeMin < 1500 {
		v += fmt.Sprintf(" Free system RAM fell to %.0f MB.", m.RamFreeMin)
	}
	if m.SlowS >= 1 {
		v += fmt.Sprintf(" Frames crawled for %.0f s.", m.SlowS)
	}
	return v
}

func label(k uint8, main bool) string {
	n := hrec.KindName[k]
	if main {
		return n + " (main thread)"
	}
	return n + " (other threads)"
}

func groupOfLabel(l string) string {
	switch {
	case strings.Contains(l, "pipeline"):
		return "shader"
	case strings.HasPrefix(l, "GPU texture") || strings.HasPrefix(l, "GPU memory"):
		return "other"
	default:
		return "world"
	}
}

func verdict(g string, gm map[string]float64, L float64, s Spike) string {
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", 100*v/L) }
	switch g {
	case "shader":
		return "A shader or ray tracing pipeline was being built (" + fmt.Sprintf("%.0f ms", gm["shader"]) + "). The game stalls until it is ready."
	case "world":
		return "The main thread was busy loading world parts and spawning entities (" + pct(gm["world"]) + " of the frame)."
	case "redscript":
		return "REDscript mods ran for " + fmt.Sprintf("%.1f ms", gm["redscript"]) + " inside this frame."
	case "cet":
		return "CET Lua mods ran for " + fmt.Sprintf("%.1f ms", gm["cet"]) + " inside this frame."
	case "gpu":
		return "The GPU itself took " + fmt.Sprintf("%.0f ms", gm["gpu"]) + " for this frame (heavy scene, e.g. path tracing load)."
	case "vram":
		if s.BudgetMB > 0 && s.VramMB > s.BudgetMB {
			return fmt.Sprintf("VRAM was over budget (%.0f of %.0f MB): Windows moves memory around, which stalls frames.", s.VramMB, s.BudgetMB)
		}
		return fmt.Sprintf("The game was reserving new GPU memory (%.0f ms) with VRAM at %.0f of %.0f MB; the frame waited for it.", gm["vram"], s.VramMB, s.BudgetMB)
	case "disk":
		return fmt.Sprintf("Heavy disk reads at the time (%.0f MB/s).", s.DiskMBs)
	case "ram":
		return fmt.Sprintf("System RAM was nearly full (%.0f MB free): Windows was moving memory out to the page file, and every frame waits for that.", s.RamFreeMB)
	}
	return "Nothing that was recorded explains most of this frame."
}

// ---------------------------------------------------------------- helpers

func pctl(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Round(p / 100 * float64(len(sorted)-1)))
	return sorted[i]
}
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func absI64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
