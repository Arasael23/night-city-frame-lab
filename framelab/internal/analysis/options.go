package analysis

// The game's own options (2.5): the ones mods set through Cyber Engine Tweaks ("Streaming" /
// "MaxNodesPerFrame"). In a test recording Frame Lab's markers mod reads a list of them once, and one
// may be on trial: set for that run of the game only, and checked every 5 seconds. Both end up as
// lines in the markers:
//
//	option <category>|<item> = <value>             what the game has (after every mod set its options)
//	options read: 3 of 24
//	trial <category>|<item> was <value> want <v>   before it was set
//	trial <category>|<item> set <value>            the game took it
//	trial <category>|<item> again <v> from <v2>    V entered the world and it had another value: set again
//	trial <category>|<item> refused <value>        the game didn't take it
//	trial <category>|<item> missing                the game has no such option
//	trial <category>|<item> lost <value>           something else changed it while playing: left alone

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// OptionValue: one option as the game had it in a recording.
type OptionValue struct {
	Cat   string `json:"cat"`
	Item  string `json:"item"`
	Value string `json:"value"`
}

// OptionTrial: an option that was on trial in a recording, and whether it held.
type OptionTrial struct {
	Cat    string  `json:"cat"`
	Item   string  `json:"item"`
	Was    string  `json:"was"`               // its value before the trial set it
	Want   string  `json:"want"`              // the value on trial
	Set    bool    `json:"set"`               // the game took the value
	Got    string  `json:"got,omitempty"`     // what the game showed after setting
	From   string  `json:"from,omitempty"`    // the value found when V entered the world, before it was set again (another mod's)
	Again  int     `json:"again,omitempty"`   // times it was set again when V entered the world (another mod sets it on the way in)
	Lost   string  `json:"lost,omitempty"`    // something else changed it to this while playing
	LostMs float64 `json:"lost_ms,omitempty"` // when (Unix ms)
	Off    string  `json:"off,omitempty"`     // why the trial didn't hold, in plain words ("" when it did)
}

// OptionsReport: the game's options in one recording.
type OptionsReport struct {
	Asked  int           `json:"asked"`            // options the markers mod looked for
	Values []OptionValue `json:"values,omitempty"` // the ones this game has
	Trials []OptionTrial `json:"trials,omitempty"`
}

const FileOptions = "options.json"

// OptionKey: how an option is named in the markers and in Frame Lab's lists
func OptionKey(cat, item string) string { return cat + "|" + item }

// SameOption: the same option value (numbers are compared as numbers: the game writes 0.25 as 0.250000)
func SameOption(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if strings.EqualFold(a, b) {
		return true
	}
	x, e1 := strconv.ParseFloat(a, 64)
	y, e2 := strconv.ParseFloat(b, 64)
	if e1 != nil || e2 != nil {
		return false
	}
	return math.Abs(x-y) <= 0.0001*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))
}

// OptionText: an option value as it is shown ("0.250000" -> "0.25", "400" -> "400", "true" -> "on")
func OptionText(v string) string {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "true":
		return "on"
	case "false":
		return "off"
	case "":
		return "not read"
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return v
}

// ParseOptions reads the option lines out of a recording's markers (nil when it has none).
func ParseOptions(marks []GameMark) *OptionsReport {
	rep := &OptionsReport{}
	any := false
	trial := func(k string) *OptionTrial {
		cat, item, ok := strings.Cut(k, "|")
		if !ok {
			return nil
		}
		for i := range rep.Trials {
			if rep.Trials[i].Cat == cat && rep.Trials[i].Item == item {
				return &rep.Trials[i]
			}
		}
		rep.Trials = append(rep.Trials, OptionTrial{Cat: cat, Item: item})
		return &rep.Trials[len(rep.Trials)-1]
	}
	for _, m := range marks {
		t := m.Text
		switch {
		case strings.HasPrefix(t, "option "):
			k, v, ok := strings.Cut(t[len("option "):], " = ")
			cat, item, ok2 := strings.Cut(k, "|")
			if !ok || !ok2 {
				continue
			}
			any = true
			found := false
			for i := range rep.Values { // the game was started twice in one recording: the last read stands
				if rep.Values[i].Cat == cat && rep.Values[i].Item == item {
					rep.Values[i].Value, found = v, true
				}
			}
			if !found {
				rep.Values = append(rep.Values, OptionValue{Cat: cat, Item: item, Value: v})
			}
		case strings.HasPrefix(t, "options read: "):
			f := strings.Fields(t[len("options read: "):]) // "3 of 24"
			if len(f) == 3 {
				rep.Asked, _ = strconv.Atoi(f[2])
				any = true
			}
		case strings.HasPrefix(t, "trial "):
			f := strings.Fields(t[len("trial "):])
			if len(f) < 2 {
				continue
			}
			tr := trial(f[0])
			if tr == nil {
				continue
			}
			any = true
			arg := ""
			if len(f) > 2 {
				arg = f[2]
			}
			switch f[1] {
			case "was": // was <v> want <v>
				if tr.Was == "" { // a second start of the game in the same recording finds the trial value: the first one is the game's own
					tr.Was = arg
				}
				if len(f) >= 5 && f[3] == "want" {
					tr.Want = f[4]
				}
			case "set":
				tr.Set, tr.Got = true, arg
			case "again": // again <v> from <v2>
				tr.Again++
				if len(f) >= 5 && f[3] == "from" {
					tr.From = f[4]
				}
			case "refused":
				tr.Set, tr.Got = false, arg
				tr.Off = "the game didn't take the value (it stayed " + OptionText(arg) + ")"
			case "missing":
				tr.Set = false
				tr.Off = "this game has no such option"
			case "lost":
				if tr.Lost == "" {
					tr.Lost, tr.LostMs = arg, m.UnixMs
					tr.Off = "something else changed it to " + OptionText(arg) + " while you played (another mod sets this option too)"
				}
			}
		}
	}
	if !any {
		return nil
	}
	for i := range rep.Trials {
		tr := &rep.Trials[i]
		switch {
		case tr.Off != "":
		case !tr.Set:
			tr.Off = "it was never set (the markers mod didn't get that far)"
		case SameOption(tr.Was, tr.Want):
			tr.Off = "the option already had that value (" + OptionText(tr.Was) + "), so there was nothing to try"
		}
	}
	return rep
}

// Value: an option's value in this recording ("" when it wasn't read). For an option that was on trial
// that is the value it had before the trial set it.
func (r *OptionsReport) Value(cat, item string) string {
	if r == nil {
		return ""
	}
	for i := range r.Trials {
		if t := &r.Trials[i]; t.Cat == cat && t.Item == item && t.Was != "" {
			if t.From != "" {
				return t.From // what another mod sets it to on the way into the world: that is what the build runs with
			}
			return t.Was
		}
	}
	for _, v := range r.Values {
		if v.Cat == cat && v.Item == item {
			return v.Value
		}
	}
	return ""
}

// Trial: the trial of this option in the recording (nil when there was none).
func (r *OptionsReport) Trial(cat, item string) *OptionTrial {
	if r == nil {
		return nil
	}
	for i := range r.Trials {
		if r.Trials[i].Cat == cat && r.Trials[i].Item == item {
			return &r.Trials[i]
		}
	}
	return nil
}

// ReadOptions: the options of an analysed session (nil when it has none).
func ReadOptions(dir string) *OptionsReport {
	b, err := os.ReadFile(filepath.Join(dir, FileOptions))
	if err != nil {
		return nil
	}
	r := &OptionsReport{}
	if json.Unmarshal(b, r) != nil {
		return nil
	}
	return r
}

func writeOptions(dir string, r *OptionsReport) {
	if r == nil {
		os.Remove(filepath.Join(dir, FileOptions))
		return
	}
	if b, err := json.Marshal(r); err == nil {
		os.WriteFile(filepath.Join(dir, FileOptions), b, 0o644)
	}
}
