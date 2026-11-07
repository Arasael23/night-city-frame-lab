package analysis

// Load times (2.0): game start to the main menu, and every loading screen (save loads, fast
// travel) from the session markers. Game start comes from RED4ext's log for that run.

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type LoadTimes struct {
	StartToMenuS float64   `json:"start_to_menu_s,omitempty"` // game start until the first marker (main menu / loading)
	ScriptsS     float64   `json:"scripts_s,omitempty"`       // RED4ext: script blob set -> blob ready (redscript compile)
	Screens      []float64 `json:"screens,omitempty"`         // each loading screen, seconds
	FirstLoadS   float64   `json:"first_load_s,omitempty"`    // the first loading screen in game (the save load)
}

var reR4ExtLine = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3})\]`)

// red4extRun: the RED4ext log of the game run that started closest after 'after'.
func red4extRun(game string, after time.Time) (start, blobSet, blobReady time.Time) {
	if game == "" {
		return
	}
	dir := filepath.Join(game, "red4ext", "logs")
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "red4ext-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		t, err := time.ParseInLocation("red4ext-2006-01-02-15-04-05.log", n, time.Local)
		if err != nil || t.Before(after.Add(-10*time.Second)) {
			continue
		}
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for i := 0; sc.Scan() && i < 400; i++ {
			line := sc.Text()
			m := reR4ExtLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			tt, err := time.ParseInLocation("2006-01-02 15:04:05.000", m[1], time.Local)
			if err != nil {
				continue
			}
			if start.IsZero() {
				start = tt
			}
			if blobSet.IsZero() && strings.Contains(line, "Scripts BLOB is set") {
				blobSet = tt
			}
			if blobReady.IsZero() && strings.Contains(line, "blob path was updated") {
				blobReady = tt
			}
		}
		return
	}
	return
}

func loadTimes(dir string, meta *Meta) *LoadTimes {
	marks, err := ReadMarks(dir)
	if err != nil || len(marks) == 0 {
		return nil
	}
	lt := &LoadTimes{}
	start, set, ready := red4extRun(meta.Game, meta.Created)
	if !start.IsZero() {
		first := marks[0].UnixMs
		if s := (first - float64(start.UnixMilli())) / 1000; s > 0 && s < 1800 {
			lt.StartToMenuS = math.Round(s*10) / 10
		}
		if !set.IsZero() && !ready.IsZero() && ready.After(set) {
			lt.ScriptsS = math.Round(ready.Sub(set).Seconds()*10) / 10
		}
	}
	var on float64 = -1
	inGame := false
	for _, m := range marks {
		switch {
		case strings.HasPrefix(m.Text, "in game"):
			inGame = true
		case strings.HasPrefix(m.Text, "left the game"):
			inGame = false
		case strings.HasPrefix(m.Text, "loading screen on"):
			on = m.UnixMs
		case strings.HasPrefix(m.Text, "loading screen off") && on > 0:
			s := (m.UnixMs - on) / 1000
			if s >= 0.5 && s < 600 {
				lt.Screens = append(lt.Screens, math.Round(s*10)/10)
				if inGame && lt.FirstLoadS == 0 && s >= 3 {
					lt.FirstLoadS = math.Round(s*10) / 10
				}
			}
			on = -1
		}
	}
	if lt.StartToMenuS == 0 && len(lt.Screens) == 0 {
		return nil
	}
	return lt
}
