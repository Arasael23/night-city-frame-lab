package analysis

// F10 "I felt a stutter" marks (2.0): Frame Lab writes the time of each F10 press to felt.txt; the
// report shows what happened in the 2 seconds before each one.

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const FileFelt = "felt.txt"

type FeltMark struct {
	T       float64 `json:"t"`
	Clock   string  `json:"clock"`
	WorstMs float64 `json:"worst_ms"`           // slowest frame in the 2 s before the press
	Spikes  []int   `json:"spikes,omitempty"`   // stutters (spike ids) in those 2 s
	Cause   string  `json:"cause"`              // what the report thinks it was
	HeldPct float64 `json:"held_pct,omitempty"` // frames shown for 2+ refreshes in that second
}

// readFelt: unix ms per line.
func readFelt(dir string) []float64 {
	f, err := os.Open(filepath.Join(dir, FileFelt))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, err := strconv.ParseFloat(strings.TrimSpace(sc.Text()), 64); err == nil && v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func (d *data) feltMarks(dir string, res *Result, t0 int64) []FeltMark {
	presses := readFelt(dir)
	if len(presses) == 0 || d.freq <= 0 {
		return nil
	}
	tl := &res.Timeline
	var out []FeltMark
	for _, u := range presses {
		t := float64(d.unixToQpc(u)-t0) / d.freq
		m := FeltMark{T: round3(t), Clock: time.UnixMilli(int64(u)).Format("15:04:05")}
		for i, bt := range tl.T {
			if bt >= t-2 && bt <= t+0.2 && i < len(tl.FrameMs) {
				m.WorstMs = math.Max(m.WorstMs, tl.FrameMs[i])
			}
		}
		m.WorstMs = round2(m.WorstMs)
		var worst *Spike
		for i := range res.Spikes {
			s := &res.Spikes[i]
			if s.T >= t-2 && s.T <= t+0.2 {
				m.Spikes = append(m.Spikes, s.ID)
				if worst == nil || s.Ms > worst.Ms {
					worst = s
				}
			}
		}
		for i, dt := range tl.DispT {
			if dt <= t && t < dt+1 && i < len(tl.DispHeld) {
				m.HeldPct = tl.DispHeld[i]
			}
		}
		loading := false
		for _, np := range tl.NotPlaying {
			if t >= np[0] && t <= np[1] {
				loading = true
			}
		}
		switch {
		case worst != nil:
			m.Cause = fmt.Sprintf("a %.0f ms stutter %.1f s before: %s", worst.Ms, math.Max(0, t-worst.T), worst.Cause)
		case m.HeldPct >= 5:
			m.Cause = fmt.Sprintf("no single slow frame, but %.0f%% of the frames in that second stayed on screen for two refreshes (uneven pacing)", m.HeldPct)
		case m.WorstMs > 0:
			m.Cause = fmt.Sprintf("nothing over the stutter limit: the slowest frame was %.0f ms. Uneven pacing or input lag you can feel but the frame times don't show", m.WorstMs)
		default:
			m.Cause = "outside the recorded part of the session"
		}
		if loading && m.WorstMs > 0 {
			m.Cause = "during a loading screen or the menu (left out of the play numbers): " + m.Cause
		}
		out = append(out, m)
		res.Markers = append(res.Markers, Marker{m.T, "felt"})
	}
	return out
}
