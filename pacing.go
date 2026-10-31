package analysis

import (
	"math"
	"sort"
)

// Display pacing: how evenly frames reached the screen, from PresentMon's "time between display
// changes". On a screen with a fixed refresh rate every frame waits for the next refresh, so with
// fewer new frames than refreshes some frames stay up for two refreshes. That looks uneven
// (judder) even when no frame was slow. With VRR the screen waits for the frame instead.

type Pacing struct {
	Samples   int     `json:"samples"`    // display changes while playing
	ShownFps  float64 `json:"shown_fps"`  // frames that reached the screen per second (frame generation included)
	RefreshHz float64 `json:"refresh_hz"` // 0 = no fixed refresh found
	VRR       bool    `json:"vrr"`        // display changes off the refresh grid: VRR (or no v-sync) in effect
	FitPct    float64 `json:"fit_pct"`    // share of display changes on the refresh grid
	HeldPct   float64 `json:"held_pct"`   // fixed refresh: share of frames shown for 2 or more refreshes
	Held3Pct  float64 `json:"held3_pct"`  // ... for 3 or more
}

var refreshRates = []float64{50, 60, 75, 85, 90, 100, 120, 144, 165, 170, 175, 180, 200, 240, 280, 300, 360, 480, 500}

// pacingOf works out the refresh rate (the slowest rate whose steps fit the display changes as
// well as the best one does: 120 Hz steps also fit a 240 Hz grid) and the share of frames held.
func pacingOf(ms []float64, playS float64) Pacing {
	p := Pacing{Samples: len(ms)}
	if playS > 0 {
		p.ShownFps = math.Round(float64(len(ms))/playS*10) / 10
	}
	if len(ms) < 200 {
		return p
	}
	fit := func(hz float64) float64 {
		per := 1000 / hz
		tol := math.Min(0.35, per*0.06)
		n := 0
		for _, v := range ms {
			k := math.Round(v / per)
			if k >= 1 && math.Abs(v-k*per) <= tol {
				n++
			}
		}
		return float64(n) / float64(len(ms))
	}
	best := 0.0
	fits := make([]float64, len(refreshRates))
	for i, hz := range refreshRates {
		fits[i] = fit(hz)
		best = math.Max(best, fits[i])
	}
	for i, hz := range refreshRates { // slowest rate that fits as well as the best
		if fits[i] >= best-0.02 {
			p.RefreshHz = hz
			p.FitPct = math.Round(fits[i]*1000) / 10
			break
		}
	}
	if best < 0.85 {
		p.VRR = true
		p.RefreshHz = 0
		return p
	}
	per := 1000 / p.RefreshHz
	h2, h3 := 0, 0
	for _, v := range ms {
		k := math.Round(v / per)
		if k >= 2 {
			h2++
		}
		if k >= 3 {
			h3++
		}
	}
	p.HeldPct = math.Round(1000*float64(h2)/float64(len(ms))) / 10
	p.Held3Pct = math.Round(1000*float64(h3)/float64(len(ms))) / 10
	return p
}

// per-second shown frames and share held (for the chart)
func pacingSeconds(ts []float64, ms []float64, refreshHz float64) (t, fps, held []float64) {
	if len(ts) == 0 {
		return
	}
	type acc struct{ n, h int }
	m := map[int]*acc{}
	per := 0.0
	if refreshHz > 0 {
		per = 1000 / refreshHz
	}
	for i, x := range ts {
		s := int(math.Floor(x))
		a := m[s]
		if a == nil {
			a = &acc{}
			m[s] = a
		}
		a.n++
		if per > 0 && math.Round(ms[i]/per) >= 2 {
			a.h++
		}
	}
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		a := m[k]
		t = append(t, float64(k))
		fps = append(fps, float64(a.n))
		if per > 0 {
			held = append(held, math.Round(1000*float64(a.h)/float64(a.n))/10)
		}
	}
	return
}
