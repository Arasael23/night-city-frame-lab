package analysis

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// "Do this next" (2.5): every advice item that calls for something gets one instruction, where its
// button leads, and the number that should move if it worked. The next session of the same build
// compares those numbers (FollowUp), so a change gets a verdict instead of a feeling.

// Do: the one thing to do about an advice item.
type Do struct {
	Text   string   `json:"text"`             // the instruction
	Effect string   `json:"effect,omitempty"` // what it should change
	Button string   `json:"button,omitempty"` // label of the button, "" for none
	Go     string   `json:"go,omitempty"`     // where the button leads: "exp:<id>" (Experiments), "vram", "optimise", "nexus:<mod id>"
	Metric string   `json:"metric,omitempty"` // the number the next session is compared on
	E      bool     `json:"e,omitempty"`      // Frame Lab 2.5: 2.5E (Experimental) can try to do this for you
	Title  string   `json:"title,omitempty"`  // 2.5 Start here: the step in a few words (Text stays the full sentence)
	Steps  []string `json:"steps,omitempty"`  // 2.5 Start here: the clicks, one per line
	Bar    *Bar     `json:"bar,omitempty"`    // 2.5 Start here: the number behind the step, drawn against its limit
}

// Bar: a value against its limit (memory promised vs Windows' limit, VRAM vs budget)
type Bar struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Max   float64 `json:"max"`
	Unit  string  `json:"unit"`
}

func gbBar(label string, mb, maxMB float64) *Bar {
	if mb <= 0 || maxMB <= 0 {
		return nil
	}
	return &Bar{Label: label, Value: math.Round(mb/1024*10) / 10, Max: math.Round(maxMB/1024*10) / 10, Unit: "GB"}
}

// ExpNames: the Experiments settings advice points at (id -> the name a session lists when it is on).
var ExpNames = map[string]string{
	"pagefile":    "Fixed page file on the fastest drive",
	"standbyauto": "Clear standby memory before each session",
	"defender":    "Defender exclusion for the game",
	"quiet":       "Quiet session",
}

// expOn: that Experiments setting was on in the session.
func expOn(m *Meta, id string) bool {
	if m == nil {
		return false
	}
	name := ExpNames[id]
	for _, e := range m.Experiments {
		if e == name {
			return true
		}
	}
	return false
}

func expDo(id, effect, metric string) *Do {
	if !Experimental {
		d := diyDo[id]
		return &Do{Text: d.text, Title: d.title, Steps: d.steps, Effect: effect, Button: d.button, Go: d.go_, Metric: metric, E: true}
	}
	return &Do{Text: "Turn on \"" + ExpNames[id] + "\" in Experiments.", Title: "Turn on " + ExpNames[id], Effect: effect, Button: "Open Experiments", Go: "exp:" + id, Metric: metric}
}

// diyDo: the Experiments settings as do-it-yourself steps (Frame Lab 2.5)
type diy struct {
	title, text string
	steps       []string
	button, go_ string
}

var diyDo = map[string]diy{
	"pagefile": {"Give Windows a fixed page file on your fastest drive",
		"Give Windows a fixed page file on your fastest drive: Virtual memory, Change, untick \"Automatically manage\", pick the fastest drive, Custom size 16384 to 32768 MB, Set, OK, then restart.",
		[]string{"Open Virtual memory with the button below.", "Change, then untick \"Automatically manage paging file size\".", "Pick your fastest drive and choose Custom size: 16384 to 32768 MB.", "Set, OK, then restart Windows."},
		"Open Virtual memory", "setting:pagefile"},
	"standbyauto": {"Restart the PC before you play", "Restart the PC before you play (it starts with nothing held in standby memory), and close browsers and launchers.",
		[]string{"Restart Windows.", "Before starting the game, close browsers and launchers you don't need."}, "", ""},
	"defender": {"Add the game folder as a Defender exclusion",
		"Add the game folder as a Defender exclusion: Virus & threat protection settings, Exclusions, Add an exclusion, Folder, pick the game folder. Remove it again if it doesn't help.",
		[]string{"Open Defender settings with the button below.", "Exclusions: Add or remove exclusions.", "Add an exclusion, Folder, and pick the game folder.", "Take it out again later if it made no difference."},
		"Open Defender settings", "setting:defender"},
	"quiet": {"Pause Windows Update while you play",
		"Before you play, pause Windows Update for a week and let any update finish first, so nothing downloads or installs while you play.",
		[]string{"Open Windows Update with the button below.", "Let any update that is waiting finish.", "Pause updates for a week."},
		"Open Windows Update", "setting:update"},
}

// ---------------------------------------------------------------- the numbers a change is judged by

type metricDef struct {
	Label string
	Unit  string
	Less  bool    // lower is better
	Min   float64 // the smallest change that counts as a change
}

var metricDefs = map[string]metricDef{
	"spikes_10":          {"Stutters per 10 minutes of play", "", true, 1},
	"low1_fps":           {"1% low", "fps", false, 2},
	"commit_headroom_mb": {"Memory left before Windows' limit", "MB", false, 1024},
	"ram_free_min_mb":    {"Free RAM at the lowest", "MB", false, 500},
	"vram_stutters_10":   {"VRAM stutters per 10 minutes", "", true, 0.5},
	"vram_peak_pct":      {"VRAM in use, share of the game's budget", "%", true, 3},
	"others_vram_mb":     {"Graphics memory held by other programs", "MB", true, 150},
	"freezes_10":         {"Long freezes with no cause inside the game, per 10 minutes", "", true, 0.5},
	"paging_10":          {"Stutters from memory read back from disk, per 10 minutes", "", true, 0.5},
	"driver_10":          {"Stutters with a driver holding the CPU, per 10 minutes", "", true, 0.5},
	"slow_reads_10":      {"Stutters with slow disk reads, per 10 minutes", "", true, 0.5},
	"reset_slow_s":       {"Crawling frames after screen resets", "s", true, 1},
	"held_pct":           {"Frames held for two refreshes", "%", true, 1},
	"low1_ratio":         {"1% low as a share of the average", "%", false, 5},
	"cpu_temp":           {"Hottest CPU temperature", "°C", true, 3},
	"gpu_temp":           {"Hottest GPU temperature", "°C", true, 3},
}

const modMetric = "mod_ms:" // + mod name: that mod's script time

func metricOf(key string) (metricDef, bool) {
	if strings.HasPrefix(key, modMetric) {
		return metricDef{"Script time of " + key[len(modMetric):], "ms per second", true, 0.5}, true
	}
	d, ok := metricDefs[key]
	return d, ok
}

// MetricValue: one of those numbers for a session; false when the session doesn't have it.
func MetricValue(r *Result, key string) (float64, bool) {
	k := r.KPI
	per10 := func(n int) (float64, bool) {
		if k.DurationS <= 0 {
			return 0, false
		}
		return float64(n) / (k.DurationS / 600), true
	}
	count := func(f func(s *Spike) bool) int {
		n := 0
		for i := range r.Spikes {
			if f(&r.Spikes[i]) {
				n++
			}
		}
		return n
	}
	win := r.Windows != nil && r.Windows.Recorded
	switch key {
	case "spikes_10":
		return per10(k.Spikes)
	case "low1_fps":
		return k.Low1Fps, k.Low1Fps > 0
	case "commit_headroom_mb":
		return k.CommitLimitMB - k.CommitPeakMB, k.CommitLimitMB > 0 && k.CommitPeakMB > 0
	case "ram_free_min_mb":
		return k.RamFreeMinMB, k.RamFreeMinMB > 0
	case "vram_stutters_10":
		return per10(count(func(s *Spike) bool { return s.Group == "vram" && !s.AfterReset }))
	case "vram_peak_pct":
		peak := k.VramPeakMB
		if k.VramPlayPeakMB > 0 {
			peak = k.VramPlayPeakMB
		}
		if k.VramBudgetMB <= 0 || peak <= 0 {
			return 0, false
		}
		return 100 * peak / k.VramBudgetMB, true
	case "others_vram_mb":
		if r.GpuApps == nil || !r.GpuApps.Recorded {
			return 0, false
		}
		return r.GpuApps.OthersTypMB, true
	case "freezes_10":
		return per10(count(func(s *Spike) bool { return s.Group == "unexplained" && s.Ms >= 150 }))
	case "paging_10":
		if !win {
			return 0, false
		}
		return per10(r.Windows.PagingSpk)
	case "driver_10":
		if !win {
			return 0, false
		}
		return per10(r.Windows.DriverSpk)
	case "slow_reads_10":
		if !win {
			return 0, false
		}
		return per10(count(func(s *Spike) bool { return s.ReadMaxMs >= SlowReadMs }))
	case "reset_slow_s":
		sum := 0.0
		for _, m := range r.Timeline.Resets {
			if m.Playing && !m.Release {
				sum += m.SlowS
			}
		}
		return sum, true
	case "held_pct":
		if r.Pacing == nil || r.Pacing.Samples < 200 || r.Pacing.VRR {
			return 0, false
		}
		return r.Pacing.HeldPct, true
	case "low1_ratio":
		if k.AvgFps <= 0 || k.Low1Fps <= 0 {
			return 0, false
		}
		return 100 * k.Low1Fps / k.AvgFps, true
	case "cpu_temp":
		return k.CpuTempMax, k.CpuTempMax > 0
	case "gpu_temp":
		return k.GpuTempMax, k.GpuTempMax > 0
	}
	if strings.HasPrefix(key, modMetric) {
		if len(r.Mods) == 0 {
			return 0, false // no per-mod script time in this session
		}
		name := key[len(modMetric):]
		for _, m := range r.Mods {
			if m.Name == name {
				return m.RedMsS + m.CetMsS, true
			}
		}
		return 0, true // the mod is gone
	}
	return 0, false
}

// judge: better, same or worse, for a change of at least the metric's smallest step or a tenth of the old value
func judge(d metricDef, was, now float64) string {
	diff := now - was
	if d.Less {
		diff = -diff
	}
	lim := math.Max(d.Min, 0.1*math.Abs(was))
	switch {
	case diff >= lim:
		return "better"
	case diff <= -lim:
		return "worse"
	}
	return "same"
}

// ---------------------------------------------------------------- the verdict line

// actions: how many advice items call for something (crash, hardware, stutter causes, things worth a test)
func actions(r *Result) int {
	n := 0
	for _, a := range r.Advice {
		if a.Do != nil && a.Level != "info" && a.Level != "good" {
			n++
		}
	}
	return n
}

func playTime(s float64) string {
	if s < 90 {
		return fmt.Sprintf("%.0f s", s)
	}
	return fmt.Sprintf("%.0f min", s/60)
}

// VerdictLine: the session in one sentence, and how many things there are to do.
func VerdictLine(r *Result) string {
	k := r.KPI
	var b strings.Builder
	rate := 0.0
	if k.DurationS > 0 {
		rate = float64(k.Spikes) / (k.DurationS / 600)
	}
	switch {
	case r.Crash != nil:
		b.WriteString("The game crashed in this session.")
	case k.DurationS < 30:
		b.WriteString("Under half a minute of play recorded: too short to judge.")
	case k.Spikes == 0:
		fmt.Fprintf(&b, "Smooth: no stutters in %s of play.", playTime(k.DurationS))
	case rate < 5 || k.Spikes <= 2:
		fmt.Fprintf(&b, "Smooth: %d stutter%s in %s of play.", k.Spikes, plural(k.Spikes), playTime(k.DurationS))
	case rate < 20:
		fmt.Fprintf(&b, "Some stutter: %d in %s of play (%.0f per 10 minutes).", k.Spikes, playTime(k.DurationS), rate)
	default:
		fmt.Fprintf(&b, "Rough: %d stutters in %s of play (%.0f per 10 minutes).", k.Spikes, playTime(k.DurationS), rate)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "no markers from the game") && k.Spikes > 0 {
			b.WriteString(" Loading screens and menus count as play here (no markers from the game), so that number is too high.")
			break
		}
	}
	switch n := actions(r); n {
	case 0:
		b.WriteString(" Nothing needs doing.")
	case 1:
		b.WriteString(" One thing to do.")
	default:
		fmt.Fprintf(&b, " %d things to do.", n)
	}
	return b.String()
}

// ---------------------------------------------------------------- did it help

// Follow: one number of this session next to the same number of the session before.
type Follow struct {
	Text    string  `json:"text,omitempty"` // what was suggested then ("" for the overall rows)
	Label   string  `json:"label"`
	Unit    string  `json:"unit,omitempty"`
	Was     float64 `json:"was"`
	Now     float64 `json:"now"`
	Verdict string  `json:"verdict"`        // better / same / worse
	Done    string  `json:"done,omitempty"` // "yes" / "no" where Frame Lab can tell (Experiments settings), else ""
}

// FollowUp: this session against the one recorded before it in the same build.
type FollowUp struct {
	Session string    `json:"session"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Rows    []Follow  `json:"rows"`
	Changed []string  `json:"changed,omitempty"` // Experiments settings switched on or off in between
	Note    string    `json:"note,omitempty"`
}

// BuildFollowUp compares cur with prev (the session before it in the same build): the overall numbers,
// then the number behind each thing prev's report said to do.
func BuildFollowUp(prev, cur *Result) *FollowUp {
	if prev == nil || cur == nil || prev.Meta == nil || cur.Meta == nil || prev.Meta.ID == cur.Meta.ID {
		return nil
	}
	fu := &FollowUp{Session: prev.Meta.ID, Title: prev.Meta.Title, Created: prev.Meta.Created}
	seen := map[string]bool{}
	row := func(text, key, done string) {
		d, ok := metricOf(key)
		if !ok || seen[key] {
			return
		}
		was, ok1 := MetricValue(prev, key)
		now, ok2 := MetricValue(cur, key)
		if !ok1 || !ok2 {
			return
		}
		seen[key] = true
		fu.Rows = append(fu.Rows, Follow{Text: text, Label: d.Label, Unit: d.Unit, Was: round1(was), Now: round1(now), Verdict: judge(d, was, now), Done: done})
	}
	row("", "spikes_10", "")
	row("", "low1_fps", "")
	for _, a := range prev.Advice {
		if a.Do == nil || a.Do.Metric == "" {
			continue
		}
		done := ""
		if id, ok := strings.CutPrefix(a.Do.Go, "exp:"); ok {
			done = "no"
			if expOn(cur.Meta, id) {
				done = "yes"
			}
		}
		row(a.Do.Text, a.Do.Metric, done)
	}
	was := map[string]bool{}
	for _, e := range prev.Meta.Experiments {
		was[e] = true
	}
	now := map[string]bool{}
	for _, e := range cur.Meta.Experiments {
		now[e] = true
		if !was[e] {
			fu.Changed = append(fu.Changed, "on now: "+e)
		}
	}
	for _, e := range prev.Meta.Experiments {
		if !now[e] {
			fu.Changed = append(fu.Changed, "off now: "+e)
		}
	}
	var notes []string
	if prev.KPI.DurationS < 120 || cur.KPI.DurationS < 120 {
		notes = append(notes, "One of the two sessions has under 2 minutes of play, so the comparison is rough.")
	}
	noMarks := false
	for _, r := range []*Result{prev, cur} {
		for _, w := range r.Warnings {
			noMarks = noMarks || strings.Contains(w, "no markers from the game")
		}
	}
	if noMarks {
		notes = append(notes, "One of the two sessions counts loading screens and menus as play (no markers from the game), which adds stutters that aren't real.")
	}
	fu.Note = strings.Join(notes, " ")
	if len(fu.Rows) == 0 {
		return nil
	}
	return fu
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
