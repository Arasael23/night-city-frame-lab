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

// Game markers: loading screen on/off, in game, left the game, and (2.5) every 5 seconds of play
// "at <district> / <parent> <foot|car> <fight|calm>". They keep loading screens and the main menu
// out of the stutter list, and tell which test route a recording covers.
// On game 2.5+ they come from Frame Lab's small CET mod, which writes "<unix seconds>\t<text>" lines
// to its own markers.log and flushes each one (CET's own gamelog.log is written in blocks, so its last
// lines never reach the disk when the game closes badly). Older games use the session script
// (FrameLab.Markers.reds), which writes "[Frame Lab] <text>" lines to CET's gamelog.log.

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

// ParseLuaMarks reads the markers.log of Frame Lab's CET mod ("<unix seconds>\t<text>") written at or after 'after'.
func ParseLuaMarks(path string, after time.Time) ([]GameMark, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []GameMark
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		i := strings.IndexByte(line, '\t')
		if i <= 0 {
			continue
		}
		sec, err := strconv.ParseInt(line[:i], 10, 64)
		text := strings.TrimSpace(line[i+1:])
		if err != nil || text == "" || sec < after.Unix() {
			continue
		}
		out = append(out, GameMark{UnixMs: float64(sec) * 1000, Text: text})
	}
	return out, nil
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
// titleMenu: the start screen and the menus before a save is loaded (the markers mod's menu names)
func titleMenu(text string) bool {
	switch text {
	case "menu open EngagementScreen", "menu open SingleplayerMenu", "menu open MainMenu", "menu open LoadGame":
		return true
	}
	return false
}

func notPlaying(marks []GameMark, startMs, endMs float64) [][2]float64 {
	if len(marks) == 0 {
		return nil
	}
	// the CET log has whole seconds (the real moment is up to 1 s after the logged one) and the game
	// often freezes for a frame just before a loading screen comes up: 2 s before, 3 s after
	const padBefore, padAfter = 2000.0, 3000.0
	var spans [][2]float64
	inGame := false
	outFrom := startMs // not playing until the first "in game"
	loadFrom := -1.0
	// 2.5.2: the CET markers mod says "in game" as soon as it loads, while the game still shows its
	// start screens and the title menu. When the title menu opens before the first "left the game" (the
	// save loading), that first "in game" isn't play: the start counts as not playing until the save is in.
	bootUntil := -1
	for i, m := range marks {
		if m.Text == "left the game" {
			break
		}
		if titleMenu(m.Text) {
			for j := i + 1; j < len(marks); j++ {
				if marks[j].Text == "left the game" {
					bootUntil = j
					break
				}
			}
			break
		}
	}
	for i, m := range marks {
		if i < bootUntil && m.Text == "in game" {
			continue
		}
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
