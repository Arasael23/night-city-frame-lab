package app

// Experiments in the game's own settings file (UserSettings.json): crowd density, SMT on Ryzen,
// hybrid CPU cores. Each one changes only the option's value and index in place, so the rest of
// the file stays byte for byte as the game wrote it, and Undo writes back exactly what was there.

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"framelab/internal/platform"
)

type usOption struct {
	Group string // "/gameplay/performance"
	Name  string // "CrowdDensity"
}

// usExp: the options each experiment sets, and to what (value, index)
var usExp = map[string]struct {
	Opts  []usOption
	Value string
	Index int
}{
	"crowd":  {[]usOption{{"/gameplay/performance", "CrowdDensity"}, {"/graphics/performance", "CrowdDensity"}}, "Low", 0},
	"smt":    {[]usOption{{"/utilities", "RyzenSMTUtilization"}}, "On", 1},
	"hybrid": {[]usOption{{"/utilities", "HybridUtilization"}}, "PrioritizePCores", 1},
}

func isGameFileExp(id string) bool { _, ok := usExp[id]; return ok }

// usFind: the byte range of the option's object in the file, or -1
func usFind(s string, o usOption) (int, int) {
	g := strings.Index(s, `"group_name": "`+o.Group+`"`)
	if g < 0 {
		return -1, -1
	}
	// the group ends where the next group starts
	end := strings.Index(s[g+1:], `"group_name": "`)
	if end < 0 {
		end = len(s)
	} else {
		end += g + 1
	}
	n := strings.Index(s[g:end], `"name": "`+o.Name+`"`)
	if n < 0 {
		return -1, -1
	}
	n += g
	a := strings.LastIndex(s[:n], "{")
	b := strings.Index(s[n:end], "}")
	if a < 0 || b < 0 {
		return -1, -1
	}
	return a, n + b + 1
}

var (
	reUSValue = regexp.MustCompile(`"value": ("[^"]*"|[-0-9.]+|true|false)`)
	reUSIndex = regexp.MustCompile(`"index": (-?[0-9]+)`)
)

// usGet: the option's raw value and index text ("" when missing)
func usGet(s string, o usOption) (value, index string) {
	a, b := usFind(s, o)
	if a < 0 {
		return "", ""
	}
	obj := s[a:b]
	if m := reUSValue.FindStringSubmatch(obj); m != nil {
		value = m[1]
	}
	if m := reUSIndex.FindStringSubmatch(obj); m != nil {
		index = m[1]
	}
	return
}

// usSet replaces the option's value and index (raw text, as found by usGet)
func usSet(s string, o usOption, value, index string) (string, error) {
	a, b := usFind(s, o)
	if a < 0 {
		return s, fmt.Errorf("%s isn't in UserSettings.json (start the game once)", o.Name)
	}
	obj := s[a:b]
	if value != "" {
		obj = reUSValue.ReplaceAllLiteralString(obj, `"value": `+value)
	}
	if index != "" {
		obj = reUSIndex.ReplaceAllLiteralString(obj, `"index": `+index)
	}
	return s[:a] + obj + s[b:], nil
}

func usRead(id string) (on bool, detail string, ok bool) {
	e := usExp[id]
	switch id {
	case "smt":
		if !strings.Contains(strings.ToUpper(platform.CPU().Name), "RYZEN") {
			return false, "only for AMD Ryzen CPUs", false
		}
	case "hybrid":
		if !platform.CPU().Hybrid {
			return false, "only for CPUs with performance and efficiency cores", false
		}
	}
	p := platform.UserSettingsPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return false, "no UserSettings.json yet (start the game once)", false
	}
	v, _ := usGet(string(b), e.Opts[0])
	if v == "" {
		return false, e.Opts[0].Name + " isn't in UserSettings.json", false
	}
	return strings.Trim(v, `"`) == e.Value, "now: " + strings.Trim(v, `"`), true
}

func usOn(id string) (map[string]string, error) {
	if len(platform.FindGame()) > 0 {
		return nil, errors.New("close the game first (it rewrites the file when it quits)")
	}
	e := usExp[id]
	p := platform.UserSettingsPath()
	if ro, exists := platform.FileReadOnly(p); exists && ro {
		return nil, errors.New("UserSettings.json is read-only (see Checks)")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, errors.New("no UserSettings.json yet (start the game once)")
	}
	s := string(b)
	prev := map[string]string{}
	for i, o := range e.Opts {
		v, ix := usGet(s, o)
		prev["v"+strconv.Itoa(i)], prev["i"+strconv.Itoa(i)] = v, ix
		if s, err = usSet(s, o, `"`+e.Value+`"`, strconv.Itoa(e.Index)); err != nil {
			return nil, err
		}
	}
	return prev, os.WriteFile(p, []byte(s), 0o644)
}

func usRestore(id string, prev map[string]string) error {
	if len(platform.FindGame()) > 0 {
		return errors.New("close the game first (it rewrites the file when it quits)")
	}
	e := usExp[id]
	p := platform.UserSettingsPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil // gone: nothing to put back
	}
	s := string(b)
	for i, o := range e.Opts {
		v, ix := prev["v"+strconv.Itoa(i)], prev["i"+strconv.Itoa(i)]
		if v == "" && ix == "" {
			continue
		}
		if s, err = usSet(s, o, v, ix); err != nil {
			return err
		}
	}
	return os.WriteFile(p, []byte(s), 0o644)
}
