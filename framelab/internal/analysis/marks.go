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

// Game markers come from Frame Lab's small session script (FrameLab.Markers.reds), which writes
// "[Frame Lab] <text>" lines to CET's gamelog.log: loading screen on/off, in game, left the game.
// They are used to keep loading screens and the main menu out of the stutter list.

const FileMarks = "markers.txt"

type GameMark struct {
	UnixMs float64
	Text   string
}

// [2026-10-29 18:52:43 UTC+11:00] [36276] [Frame Lab] loading screen on
var reCETLine = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) UTC([+-])(\d{2}):(\d{2})\] \[\d+\] \[Frame Lab\] (.+?)\s*$`)

// ParseCETLog reads the Frame Lab lines of CET's gamelog.log written at or after 'after'.
func ParseCETLog(path string, after time.Time) ([]GameMark, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []GameMark
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "[Frame Lab]") {
			continue
		}
		m := reCETLine.FindStringSubmatch(line)
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
		if err != nil || t.Before(after) {
			continue
		}
		out = append(out, GameMark{UnixMs: float64(t.UnixMilli()), Text: m[5]})
	}
	return out, sc.Err()
}

func WriteMarks(dir string, marks []GameMark) error {
	var b strings.Builder
	for _, m := range marks {
		fmt.Fprintf(&b, "%.0f\t%s\n", m.UnixMs, m.Text)
	}
	return os.WriteFile(filepath.Join(dir, FileMarks), []byte(b.String()), 0o644)
}

func ReadMarks(dir string) ([]GameMark, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileMarks))
	if err != nil {
		return nil, err
	}
	var out []GameMark
	for _, line := range strings.Split(string(b), "\n") {
		i := strings.IndexByte(line, '\t')
		if i <= 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[:i], 64)
		if err != nil {
			continue
		}
		out = append(out, GameMark{UnixMs: v, Text: strings.TrimSpace(line[i+1:])})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UnixMs < out[j].UnixMs })
	return out, nil
}

// notPlaying turns the markers into Unix-ms spans that are not gameplay: everything before V is
// first in the game, each loading screen, and the time between leaving the game and coming back.
// The CET log has whole seconds, so the spans are padded a little.
func notPlaying(marks []GameMark, startMs, endMs float64) [][2]float64 {
	if len(marks) == 0 {
		return nil
	}
	const padBefore, padAfter = 1000.0, 3000.0
	var spans [][2]float64
	inGame := false
	outFrom := startMs // not playing until the first "in game"
	loadFrom := -1.0
	for _, m := range marks {
		switch m.Text {
		case "in game":
			if !inGame && outFrom >= 0 {
				spans = append(spans, [2]float64{outFrom, m.UnixMs + padAfter})
				outFrom = -1
			}
			inGame = true
		case "left the game":
			if inGame {
				outFrom = m.UnixMs - padBefore
			}
			inGame = false
		case "loading screen on":
			if loadFrom < 0 {
				loadFrom = m.UnixMs - padBefore
			}
		case "loading screen off":
			if loadFrom >= 0 {
				spans = append(spans, [2]float64{loadFrom, m.UnixMs + padAfter})
				loadFrom = -1
			}
		}
	}
	if loadFrom >= 0 {
		spans = append(spans, [2]float64{loadFrom, endMs})
	}
	if !inGame && outFrom >= 0 {
		spans = append(spans, [2]float64{outFrom, endMs})
	}
	// merge
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	var out [][2]float64
	for _, s := range spans {
		if s[1] <= s[0] {
			continue
		}
		if n := len(out); n > 0 && s[0] <= out[n-1][1] {
			if s[1] > out[n-1][1] {
				out[n-1][1] = s[1]
			}
			continue
		}
		out = append(out, s)
	}
	return out
}
