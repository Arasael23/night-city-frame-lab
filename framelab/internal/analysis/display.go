package analysis

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Screen resets: the game re-applying full screen or rebuilding its screen buffers
// (IDXGISwapChain::SetFullscreenState / ResizeBuffers). Each one makes the game rebuild every
// screen-size buffer; on a full graphics card the extra goes into system RAM and frames crawl
// until it is released. HitchRecorder can't see these, but two logs most modded games have can:
// ReShade's (to the millisecond) and CET's (to the second). Frame Lab reads both after the game
// closes and keeps what falls inside the session in display_events.txt.

const FileDisplay = "display_events.txt"

type DisplayEvent struct {
	UnixMs float64
	Source string // "ReShade" or "CET"
	Text   string
}

// 17:56:49:318 [ 6388] | INFO  | Redirecting IDXGISwapChain::SetFullscreenState(this = 0000027BB5888E70, Fullscreen = TRUE, pTarget = ...) ...
var reReShadeTime = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2}):(\d{3}) `)
var reReShadeFull = regexp.MustCompile(`IDXGISwapChain::SetFullscreenState\(.*Fullscreen = (TRUE|FALSE)`)
var reReShadeResize = regexp.MustCompile(`IDXGISwapChain\d?::ResizeBuffers1?\(.*Width = (\d+), Height = (\d+)`)

// [2026-10-31 17:56:49 UTC+11:00] [info] [void *__cdecl D3D12::CRenderGlobal_Resize(...)] [6388] CRenderGlobal::Resize() called with ...
var reCETResize = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) UTC([+-])(\d{2}):(\d{2})\].*CRenderGlobal::Resize\(\) called`)

// ReadDisplayLogs reads the screen resets the game's ReShade and CET logs show at or after 'after';
// found says whether either log was there to read.
func ReadDisplayLogs(game string, after time.Time) (ev []DisplayEvent, found bool) {
	if game == "" {
		return nil, false
	}
	x64 := filepath.Join(game, "bin", "x64")
	var rs []DisplayEvent
	for _, name := range []string{"ReShade.log", "dxgi.log", "d3d12.log"} {
		p := filepath.Join(x64, name)
		if st, err := os.Stat(p); err == nil {
			found = true
			if !st.ModTime().Before(after) {
				rs = append(rs, readReShadeLog(p, after)...)
			}
		}
	}
	cetLog := filepath.Join(x64, "plugins", "cyber_engine_tweaks", "cyber_engine_tweaks.log")
	if _, err := os.Stat(cetLog); err == nil {
		found = true
	}
	cet := readCETResizes(cetLog, after)
	// the same reset seen by both: keep ReShade's line (milliseconds, and it says what was called)
	out := rs
	for _, c := range cet {
		dup := false
		for _, r := range rs {
			if r.UnixMs >= c.UnixMs-1500 && r.UnixMs <= c.UnixMs+2500 {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UnixMs < out[j].UnixMs })
	return out, found
}

// ReShade's log has times without a date and is started fresh with each game run.
func readReShadeLog(path string, after time.Time) []DisplayEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	loc := after.Location()
	day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, loc)
	var out []DisplayEvent
	var lastT time.Time
	var dayAdd time.Duration
	pendingFull := -1 // index in out of a SetFullscreenState not yet joined with its ResizeBuffers
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		m := reReShadeTime.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		s, _ := strconv.Atoi(m[3])
		ms, _ := strconv.Atoi(m[4])
		t := day.Add(time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute + time.Duration(s)*time.Second + time.Duration(ms)*time.Millisecond)
		if first {
			// the run started on the day of 'after' or just after midnight
			if t.Before(after.Add(-time.Hour)) {
				dayAdd = 24 * time.Hour
			}
			first = false
		}
		t = t.Add(dayAdd)
		if !lastT.IsZero() && t.Before(lastT.Add(-time.Hour)) { // passed midnight while playing
			dayAdd += 24 * time.Hour
			t = t.Add(24 * time.Hour)
		}
		lastT = t
		if t.Before(after) {
			continue
		}
		u := float64(t.UnixMilli())
		if fm := reReShadeFull.FindStringSubmatch(line); fm != nil {
			txt := "the game re-applied full screen"
			if fm[1] == "FALSE" {
				txt = "the game left full screen"
			}
			out = append(out, DisplayEvent{u, "ReShade", txt})
			pendingFull = len(out) - 1
			continue
		}
		if rm := reReShadeResize.FindStringSubmatch(line); rm != nil {
			size := rm[1] + "x" + rm[2]
			if pendingFull >= 0 && u-out[pendingFull].UnixMs <= 2000 {
				out[pendingFull].Text += " and rebuilt its " + size + " screen buffers"
				pendingFull = -1
				continue
			}
			out = append(out, DisplayEvent{u, "ReShade", "the game rebuilt its " + size + " screen buffers"})
			pendingFull = -1
		}
	}
	return out
}

func readCETResizes(path string, after time.Time) []DisplayEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []DisplayEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "CRenderGlobal::Resize()") {
			continue
		}
		m := reCETResize.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		hh, _ := strconv.Atoi(m[3])
		mm, _ := strconv.Atoi(m[4])
		off := hh*3600 + mm*60
		if m[2] == "-" {
			off = -off
		}
		t, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.FixedZone("cet", off))
		if err != nil || t.Before(after.Add(-time.Second)) {
			continue
		}
		// CET has whole seconds: the middle of that second
		out = append(out, DisplayEvent{float64(t.UnixMilli()) + 500, "CET", "the game rebuilt its screen (CRenderGlobal::Resize)"})
	}
	return out
}

func WriteDisplay(dir string, ev []DisplayEvent) error {
	var b strings.Builder
	for _, e := range ev {
		fmt.Fprintf(&b, "%.0f\t%s\t%s\n", e.UnixMs, e.Source, e.Text)
	}
	return os.WriteFile(filepath.Join(dir, FileDisplay), []byte(b.String()), 0o644)
}

func ReadDisplay(dir string) ([]DisplayEvent, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileDisplay))
	if err != nil {
		return nil, err
	}
	var out []DisplayEvent
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(p) < 3 {
			continue
		}
		v, err := strconv.ParseFloat(p[0], 64)
		if err != nil {
			continue
		}
		out = append(out, DisplayEvent{v, p[1], p[2]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UnixMs < out[j].UnixMs })
	return out, nil
}
