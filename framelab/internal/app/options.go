package app

// Game options, one at a time (2.5). The game has hundreds of options of its own that mods set through
// Cyber Engine Tweaks ("Streaming" / "MaxNodesPerFrame"). Frame Lab can put one of them on trial:
//
//   - the markers mod of a test recording reads the list below once, so Frame Lab knows which of them
//     this version of the game has and what they are set to (after every mod has set its own)
//   - a trial is a round of the Optimise schedule with exactly one change: the option. It is set by the
//     markers mod in the retest recordings only (nothing is written to the game's files), and a
//     recording counts only when the option held from start to end
//   - when the retest is in, the routes before and after are put next to each other and the player
//     decides: Keep (a small CET mod of Frame Lab's then sets it at every start, and it can be taken
//     out again) or Drop (nothing stays behind, and the old numbers stay the baseline)
//
// Frame Lab does not know which option helps on a build. That is what the trial is for.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"framelab/internal/analysis"
	"framelab/internal/gamefs"
)

// OptionDef: one trial Frame Lab offers. Value is the value to try; Factor (instead) tries that many
// times the value the option has now.
type OptionDef struct {
	ID     string  `json:"id"`
	Group  string  `json:"group"`
	Cat    string  `json:"cat"`
	Item   string  `json:"item"`
	Name   string  `json:"name"`
	What   string  `json:"what"` // what the option does and what the trial changes
	Cost   string  `json:"cost"` // what you give up
	Value  string  `json:"value,omitempty"`
	Factor float64 `json:"factor,omitempty"`
}

// DefaultOptions: the trials Frame Lab offers out of the box. Every name is one the game's own exe
// contains (2.5); whether a build's game has it is read in the test recordings, and only those are offered.
// data\optimise\options.json replaces the list.
func DefaultOptions() []OptionDef {
	const st, de, li, fx = "Loading the world while you move", "Small details", "Shadows and light", "Effects"
	return []OptionDef{
		{ID: "stream-nodes-fewer", Group: st, Cat: "Streaming", Item: "MaxNodesPerFrame", Factor: 0.5, Name: "Bring in fewer pieces of the world per frame",
			What: "The game loads the world around you piece by piece, up to a set number per frame. This halves that number, so a single frame has less loading to do.",
			Cost: "Things can appear later when you drive fast."},
		{ID: "stream-nodes-more", Group: st, Cat: "Streaming", Item: "MaxNodesPerFrame", Factor: 2, Name: "Bring in more pieces of the world per frame",
			What: "The opposite: doubles the number of world pieces a frame may bring in, so the world fills in sooner.",
			Cost: "A single frame may have more loading to do."},
		{ID: "stream-time-less", Group: st, Cat: "Streaming", Item: "TimeLimitStreamedPerFrame", Factor: 0.5, Name: "Give loading less time per frame",
			What: "The game gives loading the world a time allowance in every frame. This halves it.",
			Cost: "Things can appear later when you drive fast."},
		{ID: "stream-time-more", Group: st, Cat: "Streaming", Item: "TimeLimitStreamedPerFrame", Factor: 2, Name: "Give loading more time per frame",
			What: "The opposite: doubles the time a frame may spend loading the world.",
			Cost: "A single frame may take longer."},
		{ID: "stream-distance-shorter", Group: st, Cat: "Streaming", Item: "MaxStreamingDistance", Factor: 0.75, Name: "Load the world less far out",
			What: "The furthest distance at which anything is loaded, cut by a quarter: less to load while you move.",
			Cost: "Far-away objects can be missing or appear late."},
		{ID: "stream-ahead-off", Group: st, Cat: "Streaming", Item: "ObserverVelocityOffsetEnabled", Value: "false", Name: "Don't load ahead of where you are heading",
			What: "When you move fast the game loads the world ahead of you instead of around you. This switches that off.",
			Cost: "More things appearing in front of the car at speed."},
		{ID: "decals-closer", Group: de, Cat: "LevelOfDetail", Item: "DecalsHideDistance", Factor: 0.5, Name: "Hide wall and road markings sooner",
			What: "Stains, cracks, posters and road markings are drawn up to a set distance. This halves it.",
			Cost: "Surfaces further away look plainer."},
		{ID: "dyn-decals-closer", Group: de, Cat: "LevelOfDetail", Item: "DynamicDecalsHideDistance", Factor: 0.5, Name: "Hide bullet holes and blood sooner",
			What: "Marks left by shots and fights are drawn up to a set distance. This halves it.",
			Cost: "Those marks disappear sooner as you walk away."},
		{ID: "dyn-decals-off", Group: de, Cat: "Developer/FeatureToggles", Item: "DynamicDecals", Value: "false", Name: "No bullet holes and blood marks",
			What: "Switches the marks left by shots and fights off altogether.",
			Cost: "No bullet holes, no blood on the ground."},
		{ID: "contact-shadows-off", Group: li, Cat: "Developer/FeatureToggles", Item: "ContactShadows", Value: "false", Name: "No contact shadows",
			What: "Switches off the small sharp shadows where things touch the ground or each other.",
			Cost: "Objects can look a little as if they float. With path tracing it may change nothing."},
		{ID: "distant-shadows-off", Group: li, Cat: "Developer/FeatureToggles", Item: "DistantShadows", Value: "false", Name: "No shadows on far-away scenery",
			What: "Switches off the shadows the game draws on distant buildings and hills.",
			Cost: "The far distance looks flatter."},
		{ID: "distant-gi-off", Group: li, Cat: "Developer/FeatureToggles", Item: "DistantGI", Value: "false", Name: "No bounce light on far-away scenery",
			What: "Switches off the indirect light the game works out for the far distance.",
			Cost: "Distant scenery can look darker or flatter."},
		{ID: "light-blockers-off", Group: li, Cat: "Developer/FeatureToggles", Item: "CharacterLightBlockers", Value: "false", Name: "No light blockers on characters",
			What: "The game adds invisible blockers so characters aren't lit through walls. This switches them off.",
			Cost: "Faces can catch light they shouldn't."},
		{ID: "tangent-update-off", Group: li, Cat: "Developer/FeatureToggles", Item: "RuntimeTangentUpdate", Value: "false", Name: "No shading update on moving characters",
			What: "The game recalculates how light falls on skin and clothes as characters move. This switches that off.",
			Cost: "Shading on moving characters can look slightly off."},
		{ID: "vol-fog-off", Group: fx, Cat: "Developer/FeatureToggles", Item: "VolumetricFog", Value: "false", Name: "No volumetric fog",
			What: "Switches off the fog you can see light shine through (light shafts, haze around lamps).",
			Cost: "A clearly different look: the air is clean and flat."},
		{ID: "vol-clouds-off", Group: fx, Cat: "Developer/FeatureToggles", Item: "VolumetricClouds", Value: "false", Name: "No volumetric clouds",
			What: "Switches off the clouds that have depth.",
			Cost: "A plainer sky."},
		{ID: "ssr-off", Group: fx, Cat: "Developer/FeatureToggles", Item: "ScreenSpaceReflection", Value: "false", Name: "No screen-space reflections",
			What: "Switches off the cheap reflections the game makes from what is on screen.",
			Cost: "Wet roads and glass reflect less. With ray-traced reflections or path tracing it may change nothing."},
	}
}

func (s *Server) optionsPath() string { return filepath.Join(s.Paths.Data, "optimise", "options.json") }

// LoadOptions: the trials on offer (data\optimise\options.json when it is there and valid, else the default ones)
func LoadOptions(path string) []OptionDef {
	if b, err := os.ReadFile(path); err == nil {
		var o []OptionDef
		if json.Unmarshal(b, &o) == nil {
			var out []OptionDef
			seen := map[string]bool{}
			for _, d := range o {
				d.ID, d.Cat, d.Item, d.Value = strings.TrimSpace(d.ID), strings.TrimSpace(d.Cat), strings.TrimSpace(d.Item), strings.TrimSpace(d.Value)
				if d.ID == "" || seen[d.ID] || !optionNameOK(d.Cat) || !optionNameOK(d.Item) || (d.Value == "" && d.Factor <= 0) || !optionValueOK(d.Value) {
					continue
				}
				seen[d.ID] = true
				out = append(out, d)
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return DefaultOptions()
}

// optionNameOK: a category or item name as the game has them (letters, digits, / _ . -)
func optionNameOK(s string) bool {
	if s == "" || len(s) > 96 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '/', r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// optionValueOK: "", true, false or a number
func optionValueOK(v string) bool {
	if v == "" || v == "true" || v == "false" {
		return true
	}
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// trialValue: the value a trial of this option would set, given the value it has now. why: "missing"
// (the game has no such option, or it wasn't read yet) or "same" (it already has that value).
func (d OptionDef) trialValue(now string) (value, why string) {
	now = strings.TrimSpace(now)
	if now == "" {
		return "", "missing"
	}
	if d.Value != "" {
		if analysis.SameOption(now, d.Value) {
			return "", "same"
		}
		if (d.Value == "true" || d.Value == "false") != (now == "true" || now == "false") {
			return "", "missing" // the option isn't the kind the trial expects
		}
		return d.Value, ""
	}
	n, err := strconv.ParseFloat(now, 64)
	if err != nil || d.Factor <= 0 {
		return "", "missing"
	}
	v := n * d.Factor
	if !strings.Contains(now, ".") { // a whole number in the game
		v = math.Round(v)
		if v < 1 && n >= 1 {
			v = 1
		}
		value = strconv.FormatFloat(v, 'f', 0, 64)
	} else {
		value = strconv.FormatFloat(math.Round(v*10000)/10000, 'f', -1, 64)
		if !strings.Contains(value, ".") {
			value += ".0"
		}
	}
	if analysis.SameOption(now, value) {
		return "", "same"
	}
	return value, ""
}

// ---------------------------------------------------------------- what goes into the game

// SessionOpts: what the markers mod does with the game's options in one recording.
type SessionOpts struct {
	Probe [][2]string `json:"probe,omitempty"` // category, item: read once and written to the markers
	Set   [][3]string `json:"set,omitempty"`   // category, item, value: on trial in this recording
}

const markerLuaOptions = "options.txt"

// optionsFile: options.txt as the markers mod and the kept-options mod read it
func optionsFile(o SessionOpts) string {
	var b strings.Builder
	for _, p := range o.Probe {
		if optionNameOK(p[0]) && optionNameOK(p[1]) {
			fmt.Fprintf(&b, "probe|%s|%s\n", p[0], p[1])
		}
	}
	for _, p := range o.Set {
		if optionNameOK(p[0]) && optionNameOK(p[1]) && p[2] != "" && optionValueOK(p[2]) {
			fmt.Fprintf(&b, "set|%s|%s|%s\n", p[0], p[1], p[2])
		}
	}
	return b.String()
}

// TrialText: the options on trial in a recording, as the log shows them ("Streaming / MaxNodesPerFrame = 200")
func (o SessionOpts) TrialText() string {
	var out []string
	for _, p := range o.Set {
		out = append(out, fmt.Sprintf("%s / %s = %s", p[0], p[1], analysis.OptionText(p[2])))
	}
	return strings.Join(out, ", ")
}

// KeptOption: a game option kept after its trial. Frame Lab's small CET mod sets it at every start.
type KeptOption struct {
	ID    string    `json:"id"`
	Cat   string    `json:"cat"`
	Item  string    `json:"item"`
	Name  string    `json:"name"`
	Was   string    `json:"was"`   // the value the game had by itself
	Value string    `json:"value"` // the value that is kept
	When  time.Time `json:"when"`
	Round int       `json:"round"`          // the round its trial was
	Page  bool      `json:"page,omitempty"` // set on the Game options page, not kept after a trial
}

// keptDir: Frame Lab's kept-options mod in a build (only there while an option is kept)
func keptDir(game string) string {
	return filepath.Join(game, "bin", "x64", "plugins", "cyber_engine_tweaks", "mods", "NightCityFrameLabOptions")
}

// writeKept makes the kept-options mod of a build match the list: written when there is something to
// keep (or the Game options page put the mod in), taken out when there isn't. The game must be closed
// (CET reads its mods when the game starts).
func writeKept(comp Components, game string, kept []KeptOption) error {
	dir := keptDir(game)
	if len(kept) == 0 && !fileExists(filepath.Join(dir, pageMark)) {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("could not take %s out: %w", dir, err)
		}
		return nil
	}
	_, err := writeOptionsMod(comp, game, kept, false)
	return err
}

// ---------------------------------------------------------------- who else sets an option

var (
	ouMu    sync.Mutex
	ouCache = map[string]ouEntry{} // game folder -> who sets which option
	canMu   sync.Mutex
	canMemo = map[string]canEntry{} // game exe -> the markers mod can run there
)

type ouEntry struct {
	at  time.Time
	who map[string][]string
}

type canEntry struct {
	stamp time.Time
	size  int64
	ok    bool
}

// optionUsers: for every option on the list, the CET mods and config files of the build that set it too
// ("Ultra+", "memory.ini"). Read from the mods' Lua files and the game's ini files; kept for ten minutes.
func optionUsers(game string, defs []OptionDef) map[string][]string {
	type opt struct{ key, cat, item string }
	var opts []opt
	seen := map[string]bool{}
	var keys []string
	for _, d := range defs {
		k := analysis.OptionKey(d.Cat, d.Item)
		if !seen[k] {
			seen[k] = true
			opts = append(opts, opt{k, d.Cat, d.Item})
			keys = append(keys, k)
		}
	}
	ck := game + "\n" + strings.Join(keys, ",") // Optimise's list and the Game options page ask for different options
	ouMu.Lock()
	if e, ok := ouCache[ck]; ok && time.Since(e.at) < 10*time.Minute {
		ouMu.Unlock()
		return e.who
	}
	ouMu.Unlock()
	who := map[string][]string{}
	add := func(k, name string) {
		for _, n := range who[k] {
			if n == name {
				return
			}
		}
		who[k] = append(who[k], name)
	}
	// CET mods: a Lua file that names both the category and the item in quotes, or any of their files
	// (Lua, ini, json) that names "Category/Item" (Ultra+ keeps its option lists in ini files like that)
	mods := filepath.Join(game, "bin", "x64", "plugins", "cyber_engine_tweaks", "mods")
	ents, _ := os.ReadDir(mods)
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(strings.ToLower(e.Name()), "nightcityframelab") {
			continue
		}
		files := 0
		filepath.WalkDir(filepath.Join(mods, e.Name()), func(p string, d os.DirEntry, err error) error {
			ext := strings.ToLower(filepath.Ext(p))
			if err != nil || d.IsDir() || (ext != ".lua" && ext != ".ini" && ext != ".json") || files >= 400 {
				return nil
			}
			if fi, err := d.Info(); err != nil || fi.Size() > 2<<20 {
				return nil
			}
			files++
			b, err := os.ReadFile(gamefs.Real(p))
			if err != nil {
				return nil
			}
			src := string(b)
			for _, o := range opts {
				if (ext == ".lua" && quoted(src, o.item) && quoted(src, o.cat)) || namedPath(src, o.cat+"/"+o.item) {
					add(o.key, e.Name())
				}
			}
			return nil
		})
	}
	// the game's ini files: [Category] and Item = value
	for _, dir := range []string{filepath.Join(game, "engine", "config", "platform", "pc"), filepath.Join(game, "engine", "config")} {
		inis, _ := os.ReadDir(dir)
		for _, e := range inis {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".ini") {
				continue
			}
			f, err := os.Open(gamefs.Real(filepath.Join(dir, e.Name())))
			if err != nil {
				continue
			}
			sect := ""
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				switch {
				case strings.HasPrefix(line, "[") && strings.Contains(line, "]"):
					sect = strings.TrimSpace(line[1:strings.IndexByte(line, ']')])
				case line == "" || line[0] == ';' || line[0] == '#':
				default:
					name, _, ok := strings.Cut(line, "=")
					if !ok {
						continue
					}
					name = strings.TrimSpace(name)
					for _, o := range opts {
						if strings.EqualFold(o.cat, sect) && strings.EqualFold(o.item, name) {
							add(o.key, e.Name())
						}
					}
				}
			}
			f.Close()
		}
	}
	for k := range who {
		sort.Strings(who[k])
	}
	ouMu.Lock()
	ouCache[ck] = ouEntry{time.Now(), who}
	ouMu.Unlock()
	return who
}

// namedPath: "Category/Item" appears in the source as a whole name (not as the start of a longer one)
func namedPath(src, name string) bool {
	for i := 0; ; {
		j := strings.Index(src[i:], name)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(name)
		before := j == 0 || !isNameByte(src[j-1])
		after := end >= len(src) || !isNameByte(src[end])
		if before && after {
			return true
		}
		i = j + 1
	}
}

func isNameByte(c byte) bool {
	return c == '_' || c == '/' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// quoted: the name appears in the source as a whole string ('Name' or "Name")
func quoted(src, name string) bool {
	return strings.Contains(src, "'"+name+"'") || strings.Contains(src, `"`+name+`"`)
}

// optionsPossible: the markers mod that reads and sets options can run in this build (game 2.5 or later
// with Cyber Engine Tweaks); else why not.
func optionsPossible(game string) (bool, string) {
	if !HasCET(game) {
		return false, "Game options need Cyber Engine Tweaks in the build: it is what reads and sets them."
	}
	exe := filepath.Join(game, "bin", "x64", "Cyberpunk2077.exe")
	fi, err := os.Stat(exe)
	if err != nil {
		return false, "Cyberpunk2077.exe isn't in this build's folder."
	}
	canMu.Lock()
	e, ok := canMemo[exe]
	canMu.Unlock()
	if !ok || !e.stamp.Equal(fi.ModTime()) || e.size != fi.Size() {
		e = canEntry{fi.ModTime(), fi.Size(), gameAtLeast25(exe)}
		canMu.Lock()
		canMemo[exe] = e
		canMu.Unlock()
	}
	if !e.ok {
		return false, "Game options need game version 2.5 or later."
	}
	return true, ""
}

// ---------------------------------------------------------------- the trial in the schedule

// OptionRow: one trial on offer, as the Optimise tab shows it.
type OptionRow struct {
	OptionDef
	Now   string   `json:"now"`             // the value the game has ("400", "on"), "" when it wasn't read
	Try   string   `json:"try"`             // the value the trial would set
	State string   `json:"state"`           // "" (can be tried), unread, missing, same, trial, kept, taken (another value of this option is kept or on trial)
	Users []string `json:"users,omitempty"` // mods and ini files of the build that set this option too
	Past  string   `json:"past,omitempty"`  // how its last trial went
}

// OptionsStatus: the game options of a build for the Optimise tab.
type OptionsStatus struct {
	Can   bool         `json:"can"`            // the build can run trials at all (CET, game 2.5+)
	Why   string       `json:"why,omitempty"`  // why not
	Open  bool         `json:"open"`           // a trial can be started now
	Wait  string       `json:"wait,omitempty"` // why not now
	Read  string       `json:"read,omitempty"` // where the values come from ("the recording \"walk 2\" (15 Nov 14:02)")
	Rows  []OptionRow  `json:"rows"`
	Trial *OptTrial    `json:"trial,omitempty"`
	Kept  []KeptOption `json:"kept"`
	Notes []string     `json:"notes"`
}

// keptNow: the options the build's kept-options mod sets right now (its options.txt is what counts),
// with the names and dates the schedule remembers.
func keptNow(game string, sc *Schedule, defs []OptionDef) []KeptOption {
	out := []KeptOption{}
	b, err := os.ReadFile(gamefs.Real(filepath.Join(keptDir(game), markerLuaOptions)))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 4 || f[0] != "set" {
			continue
		}
		k := KeptOption{ID: "kept:" + analysis.OptionKey(f[1], f[2]), Cat: f[1], Item: f[2], Value: f[3], Name: f[1] + " / " + f[2]}
		known := false
		if sc != nil {
			for _, x := range sc.Kept {
				if x.Cat == k.Cat && x.Item == k.Item {
					k.ID, k.Name, k.Was, k.When, k.Round, known = x.ID, x.Name, x.Was, x.When, x.Round, true
				}
			}
		}
		if !known { // kept in an earlier schedule: the list's own name for it, when it has one
			for _, d := range defs {
				if d.Cat == k.Cat && d.Item == k.Item && d.Value != "" && analysis.SameOption(d.Value, k.Value) {
					k.Name = d.Name
					break
				}
			}
			for _, g := range GameOptions() { // set on the Game options page
				if g.Cat == k.Cat && g.Item == k.Item && k.Name == k.Cat+" / "+k.Item {
					k.Name = g.Name
				} else if len(g.Enable) == 2 && g.Enable[0] == k.Cat && g.Enable[1] == k.Item && k.Name == k.Cat+" / "+k.Item {
					k.Name = g.Name + " (switch)"
				}
			}
		}
		out = append(out, k)
	}
	return out
}

// optionValues: the game's options as the newest test recording of the build read them, or else as
// Frame Lab's options mod read them in the game (Game options page), with the game's own value for the
// ones the mod sets itself.
func (s *Server) optionValues(b BuildCfg, sc *Schedule) (rep *analysis.OptionsReport, from string) {
	for _, ts := range s.testSessions(b.Path, sc.Started) { // newest first
		if ts.Options != nil && len(ts.Options.Values) > 0 {
			return ts.Options, fmt.Sprintf("the recording \"%s\" (%s)", ts.Title, ts.Created.Format("2 Jan 15:04"))
		}
	}
	if rd := readModRead(b.Path); rd != nil && len(rd.Vals) > 0 {
		r := &analysis.OptionsReport{}
		for k, v := range rd.Vals {
			cat, item, ok := strings.Cut(k, "|")
			if !ok || v.State == "missing" || v.Now == "" {
				continue
			}
			val := v.Now
			if v.Own != "" {
				val = v.Own
			}
			r.Values = append(r.Values, analysis.OptionValue{Cat: cat, Item: item, Value: val})
		}
		sort.Slice(r.Values, func(i, j int) bool { return r.Values[i].Cat+r.Values[i].Item < r.Values[j].Cat+r.Values[j].Item })
		r.Asked = len(r.Values)
		if len(r.Values) > 0 {
			return r, "the game, by Frame Lab's options mod (" + rd.At.Format("2 Jan 15:04") + ")"
		}
	}
	return nil, ""
}

// optionsStatus: the trials on offer for a build, and what can be done now.
func (s *Server) optionsStatus(b BuildCfg, st *SchedStatus) *OptionsStatus {
	out := &OptionsStatus{Rows: []OptionRow{}, Kept: []KeptOption{}, Notes: []string{}, Trial: st.Trial}
	out.Can, out.Why = optionsPossible(b.Path)
	defs := LoadOptions(s.optionsPath())
	schedMu.Lock()
	sc := s.loadSchedule(b.Name)
	schedMu.Unlock()
	out.Kept = keptNow(b.Path, sc, defs)
	// options set on the Game options page: the game's own value from the options mod or the config files
	if len(out.Kept) > 0 {
		rd, ini := readModRead(b.Path), iniValues(b.Path)
		for i := range out.Kept {
			k := &out.Kept[i]
			if !strings.HasPrefix(k.ID, "kept:") {
				continue
			}
			k.Page = true
			key := analysis.OptionKey(k.Cat, k.Item)
			if rd != nil && rd.Vals[key].Own != "" {
				k.Was = rd.Vals[key].Own
			} else if iv := ini[strings.ToLower(key)]; iv.Build != "" {
				k.Was = iv.Build
			}
		}
	}
	if !out.Can || sc == nil {
		return out
	}
	vals, from := s.optionValues(b, sc)
	out.Read = from
	switch {
	case st.Step == "decide":
		out.Wait = "Keep or drop the option that was on trial first."
	case st.Trial != nil:
		out.Wait = "One option at a time: this one is on trial now."
	case st.Step == "retest":
		out.Wait = "This round already has changes. Record its retest first: an option is tried on its own, so the result is the option's and nothing else's."
	case st.Step != "fix":
		out.Wait = "Options can be tried once the build is measured and analysed."
	case vals == nil:
		out.Wait = "The game's options are read during a test recording, and this build's recordings were made before Frame Lab did that. Start the test round again to have them read."
	default:
		out.Open = true
	}
	var users map[string][]string
	if !gameRunning(b.Path) {
		users = optionUsers(b.Path, defs)
	}
	for _, d := range defs {
		row := OptionRow{OptionDef: d, Users: users[analysis.OptionKey(d.Cat, d.Item)]}
		now := vals.Value(d.Cat, d.Item)
		var kept *KeptOption
		for i := range out.Kept {
			if out.Kept[i].Cat == d.Cat && out.Kept[i].Item == d.Item {
				kept = &out.Kept[i]
			}
		}
		if kept != nil && kept.Was != "" {
			now = kept.Was // what the game has by itself; the kept value is shown with the kept option
		}
		try, why := d.trialValue(now)
		if now != "" {
			row.Now = analysis.OptionText(now)
		}
		if try != "" {
			row.Try = analysis.OptionText(try)
		}
		switch {
		case st.Trial != nil && st.Trial.ID == d.ID:
			row.State, row.Try = "trial", analysis.OptionText(st.Trial.Value)
		case st.Trial != nil && st.Trial.Cat == d.Cat && st.Trial.Item == d.Item:
			row.State = "taken"
		case kept != nil && kept.ID == d.ID:
			row.State, row.Try = "kept", analysis.OptionText(kept.Value)
		case kept != nil:
			row.State = "taken"
		case vals == nil:
			row.State = "unread"
		case why != "":
			row.State = why
		}
		// how it went the last time it was tried
		for i := len(sc.Rounds) - 1; i >= 0; i-- {
			r := &sc.Rounds[i]
			if r.Trial == nil || r.Trial.ID != d.ID || r.Trial.Outcome == "" {
				continue
			}
			what := "Dropped."
			if r.Trial.Outcome == "kept" {
				what = "Kept."
				if kept == nil {
					what = "Kept then, and taken out again since."
				}
			}
			row.Past = fmt.Sprintf("Tried on %s (%s instead of %s): %s %s", r.Trial.When.Format("2 Jan"), analysis.OptionText(r.Trial.Value), analysis.OptionText(r.Trial.Was), r.Verdict, what)
			break
		}
		out.Rows = append(out.Rows, row)
	}
	// a kept option that isn't at its value in the newest recording: something else sets it after Frame Lab.
	// When the values come from the options mod itself, its own reading says whether its value held.
	if rd := readModRead(b.Path); vals != nil && strings.HasPrefix(out.Read, "the game, by Frame Lab's options mod") && rd != nil {
		for _, k := range out.Kept {
			if v, ok := rd.Vals[analysis.OptionKey(k.Cat, k.Item)]; ok && v.State == "lost" {
				out.Notes = append(out.Notes, fmt.Sprintf("%s is kept at %s, but another mod keeps setting it to %s, so Frame Lab's value doesn't hold. Change it in that mod's own settings instead.",
					k.Name, analysis.OptionText(k.Value), analysis.OptionText(v.Now)))
			}
		}
	} else if vals != nil {
		for _, k := range out.Kept {
			for _, v := range vals.Values {
				if v.Cat == k.Cat && v.Item == k.Item && !analysis.SameOption(v.Value, k.Value) && (st.Trial == nil || st.Trial.Cat != k.Cat || st.Trial.Item != k.Item) {
					out.Notes = append(out.Notes, fmt.Sprintf("%s is kept at %s, but in %s the game had %s: something else sets it after Frame Lab does (or the recording is from before it was kept).",
						k.Name, analysis.OptionText(k.Value), from, analysis.OptionText(v.Value)))
				}
			}
		}
	}
	return out
}

// optionTry puts one option on trial: the open round gets that one change, and the next recordings set it.
func (s *Server) optionTry(i int, id string) error {
	b, err := s.buildAt(i)
	if err != nil {
		return err
	}
	if ok, why := optionsPossible(b.Path); !ok {
		return errors.New(why)
	}
	var def *OptionDef
	defs := LoadOptions(s.optionsPath())
	for k := range defs {
		if defs[k].ID == id {
			def = &defs[k]
		}
	}
	if def == nil {
		return errors.New("no such option on the list")
	}
	schedMu.Lock()
	defer schedMu.Unlock()
	st := s.schedStatusLocked(b)
	switch {
	case st.Step == "decide":
		return errors.New("Keep or drop the option that was on trial first.")
	case st.Trial != nil:
		return fmt.Errorf("One option at a time: \"%s\" is on trial now. Finish its retest or drop it first.", st.Trial.Name)
	case st.Step == "retest":
		return errors.New("This round already has changes. Record its retest first: an option is tried on its own, so the result is the option's and nothing else's.")
	case st.Step != "fix":
		return errors.New(st.Why)
	}
	sc := s.loadSchedule(b.Name)
	if sc == nil || len(sc.Rounds) == 0 {
		return errors.New("the build isn't measured yet")
	}
	for _, k := range keptNow(b.Path, sc, defs) {
		if k.Cat == def.Cat && k.Item == def.Item {
			return fmt.Errorf("This option is kept at %s. Take it out first to try another value.", analysis.OptionText(k.Value))
		}
	}
	vals, _ := s.optionValues(b, sc)
	now := vals.Value(def.Cat, def.Item)
	value, why := def.trialValue(now)
	switch {
	case vals == nil:
		return errors.New("The game's options haven't been read in this build yet: they are read during a test recording. Start the test round again to have them read.")
	case why == "missing":
		return errors.New("This game doesn't have that option (it wasn't there when the options were read).")
	case why == "same":
		return fmt.Errorf("The option already has that value (%s), so there is nothing to try.", analysis.OptionText(now))
	}
	cur := &sc.Rounds[len(sc.Rounds)-1]
	t := time.Now()
	cur.Trial = &OptTrial{ID: def.ID, Cat: def.Cat, Item: def.Item, Name: def.Name, Was: now, Value: value, When: t}
	cur.Changes = append(cur.Changes, SchedChange{When: t, Fix: "option:" + def.ID,
		Text: fmt.Sprintf("On trial: %s (%s / %s = %s; the game's own value is %s). Set in the retest recordings only.", def.Name, def.Cat, def.Item, analysis.OptionText(value), analysis.OptionText(now))})
	cur.Changed = t
	s.Ctl.diag(fmt.Sprintf("optimise option on trial in %s: %s / %s = %s (was %s)", b.Name, def.Cat, def.Item, value, now))
	return s.saveSchedule(sc)
}

// optionDrop ends a trial without keeping the option. While its retest is still running the round
// simply goes back to what it was (nothing in the build was changed); once the retest is in, the
// result stays on record and the next round starts from the numbers before the trial.
func (s *Server) optionDrop(i int) error {
	b, err := s.buildAt(i)
	if err != nil {
		return err
	}
	schedMu.Lock()
	defer schedMu.Unlock()
	sc := s.loadSchedule(b.Name)
	if sc == nil || len(sc.Rounds) == 0 {
		return errors.New("no option is on trial")
	}
	last := &sc.Rounds[len(sc.Rounds)-1]
	if last.Trial == nil || last.Trial.Outcome != "" {
		return errors.New("no option is on trial")
	}
	tr := last.Trial
	if last.Closed.IsZero() {
		var keep []SchedChange
		last.Changed = time.Time{}
		for _, c := range last.Changes {
			if c.Fix == "option:"+tr.ID {
				continue
			}
			keep = append(keep, c)
			if c.When.After(last.Changed) {
				last.Changed = c.When
			}
		}
		last.Changes, last.Trial = keep, nil
		s.Ctl.diag(fmt.Sprintf("optimise option trial dropped before its retest was complete in %s: %s / %s", b.Name, tr.Cat, tr.Item))
		return s.saveSchedule(sc)
	}
	tr.Outcome, tr.Decided = "dropped", time.Now()
	// the option was only ever set in the retest recordings, so the build is what it was before the trial:
	// the next round is measured against the numbers from before, with the mod files from before
	sc.Rounds = append(sc.Rounds, SchedRound{N: last.N + 1, Opened: time.Now(), Base: last.Base, Settings: last.Settings, Scripts: last.Scripts, Mods: last.Mods})
	s.Ctl.diag(fmt.Sprintf("optimise option dropped after its trial in %s: %s / %s (%s)", b.Name, tr.Cat, tr.Item, sc.Rounds[len(sc.Rounds)-2].Verdict))
	return s.saveSchedule(sc)
}

// optionKeep keeps the option whose trial is in: Frame Lab's kept-options mod goes into the build (or
// gets one more line) and sets it at every start from now on. The next round starts from the retest numbers.
func (s *Server) optionKeep(i int) error {
	b, err := s.buildAt(i)
	if err != nil {
		return err
	}
	if gameRunning(b.Path) {
		return errors.New("close the game first: Cyber Engine Tweaks reads its mods when the game starts")
	}
	schedMu.Lock()
	defer schedMu.Unlock()
	sc := s.loadSchedule(b.Name)
	if sc == nil || len(sc.Rounds) == 0 {
		return errors.New("no option is waiting for a decision")
	}
	last := &sc.Rounds[len(sc.Rounds)-1]
	if last.Trial == nil || last.Trial.Outcome != "" || last.Closed.IsZero() {
		return errors.New("no option is waiting for a decision (a trial has to finish its retest first)")
	}
	tr := last.Trial
	kept := []KeptOption{}
	for _, k := range keptNow(b.Path, sc, nil) {
		if k.Cat != tr.Cat || k.Item != tr.Item {
			kept = append(kept, k)
		}
	}
	kept = append(kept, KeptOption{ID: tr.ID, Cat: tr.Cat, Item: tr.Item, Name: tr.Name, Was: tr.Was, Value: tr.Value, When: time.Now(), Round: last.N})
	if err := writeKept(FindComponents(s.Paths, s.Settings.Get()), b.Path, kept); err != nil {
		return err
	}
	sc.Kept = kept
	tr.Outcome, tr.Decided = "kept", time.Now()
	buildModsForget(b.Path)
	sc.Rounds = append(sc.Rounds, SchedRound{N: last.N + 1, Opened: time.Now(), Base: last.After, Settings: last.Settings, Scripts: last.Scripts, Mods: buildMods(b.Path)})
	s.Ctl.diag(fmt.Sprintf("optimise option kept in %s: %s / %s = %s (was %s); set by %s", b.Name, tr.Cat, tr.Item, tr.Value, tr.Was, keptDir(b.Path)))
	return s.saveSchedule(sc)
}

// optionRemove takes a kept option out again. That is a change like any other: in an open round the
// same routes are recorded again; before the build is measured, the routes recorded so far were made
// with the option and are asked for again.
func (s *Server) optionRemove(i int, id string) error {
	b, err := s.buildAt(i)
	if err != nil {
		return err
	}
	if gameRunning(b.Path) {
		return errors.New("close the game first: Cyber Engine Tweaks reads its mods when the game starts")
	}
	schedMu.Lock()
	st := s.schedStatusLocked(b)
	if st.Trial != nil {
		schedMu.Unlock()
		return errors.New("An option is on trial: finish its retest or drop it first, so its result stays its own.")
	}
	sc := s.loadSchedule(b.Name)
	var gone *KeptOption
	kept := []KeptOption{}
	for _, k := range keptNow(b.Path, sc, LoadOptions(s.optionsPath())) {
		if k.ID == id && gone == nil {
			k := k
			gone = &k
			continue
		}
		kept = append(kept, k)
	}
	if gone == nil {
		schedMu.Unlock()
		return errors.New("that option isn't kept in this build")
	}
	if err := writeKept(FindComponents(s.Paths, s.Settings.Get()), b.Path, kept); err != nil {
		schedMu.Unlock()
		return err
	}
	buildModsForget(b.Path)
	if sc != nil {
		sc.Kept = kept
		if len(sc.Rounds) == 0 {
			sc.Started, sc.Conflicts, sc.Analysed = time.Now(), time.Time{}, time.Time{} // recordings made with the option aren't of the build as it is now
		}
		s.saveSchedule(sc)
	}
	schedMu.Unlock()
	s.Ctl.diag(fmt.Sprintf("optimise kept option taken out of %s: %s / %s (was kept at %s)", b.Name, gone.Cat, gone.Item, gone.Value))
	s.schedChange(b, fmt.Sprintf("Taken out: %s (%s / %s is the game's own again)", gone.Name, gone.Cat, gone.Item), "option:"+gone.ID, true)
	return nil
}
