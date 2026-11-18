package app

// Game options page (2.5E): the game's own options (the ones mods set through Cyber Engine Tweaks),
// set by hand with a slider or a switch, with no test round needed. Optimise's trials stay the way to
// measure what one does; this page is for setting them.
//
// Frame Lab's small CET mod (NightCityFrameLabOptions, the same one that keeps an option after a trial)
// sets them. It reads its options.txt again every two seconds while the game runs, so a change made
// here while playing is in the game within moments, and Reset gives the option the value it had back.
// It writes read.txt: what the options are in the game, and what they were before it set them.
// Nothing is written to the game's files or to the in-game settings.

import (
	"bufio"
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

// GameOpt: one option on the Game options page.
type GameOpt struct {
	ID     string   `json:"id"`
	Group  string   `json:"group"`
	Cat    string   `json:"cat"`
	Item   string   `json:"item"`
	Name   string   `json:"name"`
	What   string   `json:"what"`
	Cost   string   `json:"cost"`
	Try    string   `json:"try,omitempty"` // how to see what it does
	Kind   string   `json:"kind"`          // num | bool
	Min    float64  `json:"min,omitempty"`
	Max    float64  `json:"max,omitempty"`
	Step   float64  `json:"step,omitempty"`
	Unit   string   `json:"unit,omitempty"`
	Low    string   `json:"low,omitempty"`  // what the low end of the slider does
	High   string   `json:"high,omitempty"` // and the high end
	Own    string   `json:"own,omitempty"`  // the game's own value when it isn't read yet (from the game's base files)
	Start  string   `json:"start,omitempty"` // where the slider starts when the option is off by default (mip bias)
	Enable []string `json:"enable,omitempty"` // cat, item of an on/off option that is switched on while this one is set
	OffText string  `json:"off_text,omitempty"` // how "not set" reads for an option that is off unless set
}

// GameOptions: the options on the page. Every category / item name is one the game's exe contains (2.5).
func GameOptions() []GameOpt {
	const far, st, li, fx = "Seeing further", "Loading the world while you move", "Shadows and light", "Effects"
	return []GameOpt{
		{ID: "sign-sharpness", Group: far, Cat: "Editor/MipBias", Item: "ForceCustomMipBias", Kind: "num", Min: -3, Max: 1, Step: 0.25,
			Enable: []string{"Editor/MipBias", "EnableCustomMipBias"}, Start: "-1", OffText: "off",
			Name: "Sharp text and textures further away (street signs)",
			What: "Far away, the game draws every texture from a smaller copy of it, which is why the text on street and shop signs only gets readable close up. This makes the game pick the bigger copies further out. Below 0 is sharper: -1 is a clear step, -2 a big one.",
			Cost: "More graphics memory (Ultra+ warns against it on cards with 12 GB or less), and fine patterns far away can shimmer.",
			Try:  "Stand where a street sign is just too blurry to read, then move the slider and watch it.",
			Low:  "sharper", High: "softer"},
		{ID: "decals-distance", Group: far, Cat: "LevelOfDetail", Item: "DecalsHideDistance", Kind: "num", Min: 10, Max: 200, Step: 5, Unit: "m", Own: "30",
			Name: "Road markings, posters and wall details: how far out they are drawn",
			What: "Painted road lines and arrows, posters, graffiti, stains and cracks are drawn up to this distance and fade out beyond it.",
			Cost: "A little more work for the graphics card the further out.",
			Low:  "closer", High: "further"},
		{ID: "stream-distance", Group: far, Cat: "Streaming", Item: "MaxStreamingDistance", Kind: "num", Min: 10000, Max: 60000, Step: 1000, Own: "23170.251953",
			Name: "How far out the world is loaded",
			What: "The furthest distance at which pieces of the world are loaded at all.",
			Cost: "Further out: more to load while you move and more graphics memory. Closer: far-away objects can be missing or pop in.",
			Low:  "closer", High: "further"},
		{ID: "dyn-decals-distance", Group: far, Cat: "LevelOfDetail", Item: "DynamicDecalsHideDistance", Kind: "num", Min: 5, Max: 150, Step: 5, Unit: "m",
			Name: "Bullet holes and blood: how far out they stay visible",
			What: "Marks left by shots and fights are drawn up to this distance.",
			Cost: "A little more work for the graphics card the further out.",
			Low:  "closer", High: "further"},
		{ID: "stream-nodes", Group: st, Cat: "Streaming", Item: "MaxNodesPerFrame", Kind: "num", Min: 50, Max: 1500, Step: 25, Own: "300",
			Name: "Pieces of the world brought in per frame",
			What: "The game loads the world around you piece by piece, up to this many per frame.",
			Cost: "Fewer: things appear later when you drive fast. More: a single frame can have more loading to do.",
			Low:  "fewer", High: "more"},
		{ID: "stream-time", Group: st, Cat: "Streaming", Item: "TimeLimitStreamedPerFrame", Kind: "num", Min: 1, Max: 20, Step: 0.5, Unit: "ms", Own: "6.0",
			Name: "Time for loading the world in every frame",
			What: "How long a frame may spend bringing in the world.",
			Cost: "Less: things appear later when you drive fast. More: a single frame can take longer.",
			Low:  "less", High: "more"},
		{ID: "stream-ahead", Group: st, Cat: "Streaming", Item: "ObserverVelocityOffsetEnabled", Kind: "bool",
			Name: "Load ahead of where you are heading",
			What: "When you move fast the game loads the world ahead of you instead of around you.",
			Cost: "Off: more things appear in front of the car at speed."},
		{ID: "dyn-decals", Group: fx, Cat: "Developer/FeatureToggles", Item: "DynamicDecals", Kind: "bool",
			Name: "Bullet holes and blood marks", What: "The marks left by shots and fights.", Cost: "Off: no bullet holes, no blood on the ground."},
		{ID: "contact-shadows", Group: li, Cat: "Developer/FeatureToggles", Item: "ContactShadows", Kind: "bool",
			Name: "Contact shadows", What: "The small sharp shadows where things touch the ground or each other.",
			Cost: "Off: objects can look as if they float a little. With path tracing it may change nothing."},
		{ID: "distant-shadows", Group: li, Cat: "Developer/FeatureToggles", Item: "DistantShadows", Kind: "bool",
			Name: "Shadows on far-away scenery", What: "The shadows the game draws on distant buildings and hills.", Cost: "Off: the far distance looks flatter."},
		{ID: "distant-gi", Group: li, Cat: "Developer/FeatureToggles", Item: "DistantGI", Kind: "bool",
			Name: "Bounce light on far-away scenery", What: "The indirect light the game works out for the far distance.", Cost: "Off: distant scenery can look darker or flatter."},
		{ID: "light-blockers", Group: li, Cat: "Developer/FeatureToggles", Item: "CharacterLightBlockers", Kind: "bool",
			Name: "Light blockers on characters", What: "Invisible blockers so characters aren't lit through walls.", Cost: "Off: faces can catch light they shouldn't."},
		{ID: "tangent-update", Group: li, Cat: "Developer/FeatureToggles", Item: "RuntimeTangentUpdate", Kind: "bool",
			Name: "Shading update on moving characters", What: "Recalculates how light falls on skin and clothes as characters move.", Cost: "Off: shading on moving characters can look slightly off."},
		{ID: "vol-fog", Group: fx, Cat: "Developer/FeatureToggles", Item: "VolumetricFog", Kind: "bool",
			Name: "Volumetric fog", What: "The fog you can see light shine through (light shafts, haze around lamps).", Cost: "Off: a clearly different look, the air is clean and flat."},
		{ID: "vol-clouds", Group: fx, Cat: "Developer/FeatureToggles", Item: "VolumetricClouds", Kind: "bool",
			Name: "Volumetric clouds", What: "Clouds that have depth.", Cost: "Off: a plainer sky."},
		{ID: "ssr", Group: fx, Cat: "Developer/FeatureToggles", Item: "ScreenSpaceReflection", Kind: "bool",
			Name: "Screen-space reflections", What: "The cheap reflections made from what is on screen.",
			Cost: "Off: wet roads and glass reflect less. With ray-traced reflections or path tracing it may change nothing."},
	}
}

// ---------------------------------------------------------------- what the game has

// optRead: one option as the options mod last found it in the game (read.txt)
type optRead struct {
	Now   string // the value in the game
	Own   string // the value before the mod set it ("" when it never did)
	State string // set (the mod's value holds), lost (another mod keeps setting it), free (not set by the mod), missing
}

type modRead struct {
	Serial string
	At     time.Time
	Vals   map[string]optRead // OptionKey -> value
}

// readModRead: read.txt of the build's options mod (nil when there is none yet)
func readModRead(game string) *modRead {
	p := filepath.Join(keptDir(game), "read.txt")
	b, err := gamefs.ReadFile(p)
	if err != nil {
		return nil
	}
	r := &modRead{Vals: map[string]optRead{}}
	if fi, err := gamefs.Stat(p); err == nil {
		r.At = fi.ModTime()
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "|")
		switch {
		case len(f) == 2 && f[0] == "serial":
			r.Serial = f[1]
		case len(f) == 5:
			r.Vals[analysis.OptionKey(f[0], f[1])] = optRead{Now: f[2], Own: f[3], State: f[4]}
		}
	}
	return r
}

// iniVal: an option as the game's config files start it
type iniVal struct {
	Base  string // engine\config\base (the game's own)
	Build string // what it ends up at after the other config files (mods put theirs in platform\pc)
	File  string // the file the build value comes from
}

// iniValues: every [Category] Item = value in the game's config files, base first, then the rest
func iniValues(game string) map[string]iniVal {
	out := map[string]iniVal{}
	read := func(dir string, base bool) {
		ents, _ := gamefs.ReadDir(filepath.Join(game, dir))
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".ini") {
				continue
			}
			f, err := os.Open(gamefs.Real(filepath.Join(game, dir, e.Name())))
			if err != nil {
				continue
			}
			sect := ""
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if i := strings.IndexByte(line, ';'); i >= 0 {
					line = strings.TrimSpace(line[:i])
				}
				switch {
				case strings.HasPrefix(line, "[") && strings.Contains(line, "]"):
					sect = strings.TrimSpace(line[1:strings.IndexByte(line, ']')])
				case line == "" || line[0] == '#':
				default:
					name, val, ok := strings.Cut(line, "=")
					if !ok || sect == "" {
						continue
					}
					k := strings.ToLower(analysis.OptionKey(sect, strings.TrimSpace(name)))
					v := out[k]
					val = strings.TrimSpace(val)
					if base {
						v.Base = val
					}
					v.Build, v.File = val, e.Name()
					out[k] = v
				}
			}
			f.Close()
		}
	}
	read(filepath.Join("engine", "config", "base"), true)
	read(filepath.Join("engine", "config", "platform", "pc"), false)
	return out
}

// ---------------------------------------------------------------- the options mod in a build

// the options mod is in the build because of this page (it stays even when nothing is set)
const pageMark = "page.txt"

func optionsModIn(game string) bool { return fileExists(filepath.Join(keptDir(game), "init.lua")) }

// optionProbes: the options the mod reads for the page (and for Optimise's list)
func optionProbes() [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	add := func(cat, item string) {
		if k := analysis.OptionKey(cat, item); !seen[k] {
			seen[k] = true
			out = append(out, [2]string{cat, item})
		}
	}
	for _, g := range GameOptions() {
		add(g.Cat, g.Item)
		if len(g.Enable) == 2 {
			add(g.Enable[0], g.Enable[1])
		}
	}
	for _, d := range DefaultOptions() {
		add(d.Cat, d.Item)
	}
	return out
}

// writeOptionsMod writes the options mod of a build: its init.lua (only with the game closed: CET has
// it loaded while the game runs) and options.txt, with a new serial so Frame Lab can tell when the
// running game has taken it.
func writeOptionsMod(comp Components, game string, set []KeptOption, running bool) (string, error) {
	dir := keptDir(game)
	if !running {
		if comp.OptionsLua == "" {
			return "", errors.New(`the options mod is missing from components\FrameLabOptions`)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		if err := copyFile(filepath.Join(comp.OptionsLua, "init.lua"), filepath.Join(dir, "init.lua")); err != nil {
			return "", err
		}
	} else if !optionsModIn(game) {
		return "", errors.New("close the game first: Frame Lab's options mod has to be in the build when the game starts. After that, changes go in while you play")
	}
	var o SessionOpts
	for _, k := range set {
		o.Set = append(o.Set, [3]string{k.Cat, k.Item, k.Value})
	}
	o.Probe = optionProbes()
	serial := strconv.FormatInt(time.Now().UnixMilli(), 10)
	text := optionsFile(o) + "serial|" + serial + "\n"
	tmp := filepath.Join(dir, markerLuaOptions+".new")
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(dir, markerLuaOptions)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return serial, nil
}

// optionsSerial: the serial of the build's options.txt ("" when there is none)
func optionsSerial(game string) string {
	b, err := gamefs.ReadFile(filepath.Join(keptDir(game), markerLuaOptions))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "serial|"); ok {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------- the page

// GameOptRow: one option for the page, with everything Frame Lab knows of its value.
type GameOptRow struct {
	GameOpt
	Game      string   `json:"game,omitempty"`       // the game's own value
	Build     string   `json:"build,omitempty"`      // what this build's config files start it at, when that isn't the game's own
	BuildFrom string   `json:"build_from,omitempty"` // the file that does
	Now       string   `json:"now,omitempty"`        // as it was in the game when last read
	Set       string   `json:"set,omitempty"`        // what Frame Lab sets it to ("" = not set)
	Lost      bool     `json:"lost,omitempty"`       // another mod keeps setting it, so Frame Lab's value doesn't hold
	Missing   bool     `json:"missing,omitempty"`    // this game doesn't have it
	Users     []string `json:"users,omitempty"`      // mods and config files that set it too
	Kept      string   `json:"kept,omitempty"`       // set by Optimise after a trial (its name there)
}

// GameOptsStatus: the Game options page of a build.
type GameOptsStatus struct {
	Build   string       `json:"build"`
	Can     bool         `json:"can"`
	Why     string       `json:"why,omitempty"`
	Mod     bool         `json:"mod"`               // Frame Lab's options mod is in the build
	Running bool         `json:"running"`           // the game of this build is running
	Live    string       `json:"live,omitempty"`    // "in" (the running game has the newest values), "waiting", "next" (applies at the next start)
	Lock    string       `json:"lock,omitempty"`    // why nothing can be changed now
	Read    string       `json:"read,omitempty"`    // when the values were read in the game
	Rows    []GameOptRow `json:"rows"`
	Changes int          `json:"changes"`           // options Frame Lab sets in this build
}

func (s *Server) gameOptsStatus(i int) (*GameOptsStatus, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	out := &GameOptsStatus{Build: b.Name, Rows: []GameOptRow{}}
	out.Can, out.Why = optionsPossible(b.Path)
	out.Mod = optionsModIn(b.Path)
	out.Running = gameRunning(b.Path)
	sc := s.loadSchedule(b.Name)
	if st, err := s.scheduleStatus(i); err == nil && st.Trial != nil {
		out.Lock = fmt.Sprintf("\"%s\" is on trial in Optimise: finish its retest or drop it first, so its result stays its own.", st.Trial.Name)
	}
	set := map[string]KeptOption{}
	for _, k := range keptNow(b.Path, sc, DefaultOptions()) {
		set[analysis.OptionKey(k.Cat, k.Item)] = k
	}
	rd := readModRead(b.Path)
	if rd != nil && !rd.At.IsZero() {
		out.Read = rd.At.Format("2 Jan 15:04")
	}
	if out.Running && out.Mod {
		gwMu.Lock()
		wrote := gwLast[b.Path]
		gwMu.Unlock()
		switch {
		case rd != nil && rd.Serial != "" && rd.Serial == optionsSerial(b.Path):
			out.Live = "in"
		case wrote.IsZero() || time.Since(wrote) > 12*time.Second:
			// the running game hasn't taken the newest values: the mod wasn't in when it started
			out.Live = "next"
		default:
			out.Live = "waiting"
		}
	} else if out.Running {
		out.Live = "next"
	}
	ini := iniValues(b.Path)
	defs := GameOptions()
	var od []OptionDef
	for _, g := range defs {
		od = append(od, OptionDef{Cat: g.Cat, Item: g.Item})
	}
	users := optionUsers(b.Path, od)
	for _, g := range defs {
		k := analysis.OptionKey(g.Cat, g.Item)
		row := GameOptRow{GameOpt: g, Users: users[k]}
		iv := ini[strings.ToLower(k)]
		row.Game = g.Own
		if iv.Base != "" {
			row.Game = iv.Base
		}
		if iv.Build != "" && !analysis.SameOption(iv.Build, iv.Base) {
			row.Build, row.BuildFrom = iv.Build, iv.File
		}
		if rd != nil {
			if v, ok := rd.Vals[k]; ok {
				if v.State == "missing" {
					row.Missing = true
				}
				row.Now = v.Now
				if v.Own != "" && row.Game == "" {
					row.Game = v.Own
				}
				row.Lost = v.State == "lost"
			}
		}
		if row.Game == "" && g.Kind == "bool" && strings.HasPrefix(g.Cat, "Developer/FeatureToggles") {
			row.Game = "true" // feature toggles are on in the game
		}
		if kv, ok := set[k]; ok {
			row.Set = kv.Value
			if !strings.HasPrefix(kv.ID, "kept:") {
				row.Kept = kv.Name
			}
			out.Changes++
		}
		if len(g.Enable) == 2 && row.Set != "" {
			if ev, ok := set[analysis.OptionKey(g.Enable[0], g.Enable[1])]; !ok || ev.Value != "true" {
				row.Set = "" // the value is there, but the switch that makes the game use it isn't
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

var (
	gwMu   sync.Mutex
	gwLast = map[string]time.Time{} // game folder -> when options.txt was last written while the game ran
)

// gameOptSet sets one option of the page ("" = Reset: the game's own value again).
func (s *Server) gameOptSet(i int, id, value string) (*GameOptsStatus, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	if ok, why := optionsPossible(b.Path); !ok {
		return nil, errors.New(why)
	}
	var g *GameOpt
	defs := GameOptions()
	for k := range defs {
		if defs[k].ID == id {
			g = &defs[k]
		}
	}
	if g == nil {
		return nil, errors.New("no such option on the page")
	}
	value = strings.TrimSpace(value)
	switch {
	case value == "":
	case g.Kind == "bool":
		if value != "true" && value != "false" {
			return nil, errors.New("on or off only")
		}
	default:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errors.New("not a number")
		}
		f = math.Max(g.Min, math.Min(g.Max, f))
		if g.Step > 0 {
			f = g.Min + math.Round((f-g.Min)/g.Step)*g.Step
		}
		value = strconv.FormatFloat(math.Round(f*10000)/10000, 'f', -1, 64)
	}
	if st, err := s.scheduleStatus(i); err == nil && st.Trial != nil {
		return nil, fmt.Errorf("\"%s\" is on trial in Optimise: finish its retest or drop it first, so its result stays its own.", st.Trial.Name)
	}
	running := gameRunning(b.Path)
	sc := s.loadSchedule(b.Name)
	cur := keptNow(b.Path, sc, DefaultOptions())
	keys := map[string]bool{analysis.OptionKey(g.Cat, g.Item): true}
	if len(g.Enable) == 2 {
		keys[analysis.OptionKey(g.Enable[0], g.Enable[1])] = true
	}
	var next []KeptOption
	was := ""
	for _, k := range cur {
		if keys[analysis.OptionKey(k.Cat, k.Item)] {
			if k.Cat == g.Cat && k.Item == g.Item {
				was = k.Value
			}
			continue
		}
		next = append(next, k)
	}
	if value != "" {
		next = append(next, KeptOption{ID: "page:" + g.ID, Cat: g.Cat, Item: g.Item, Name: g.Name, Value: value, When: time.Now()})
		if len(g.Enable) == 2 {
			next = append(next, KeptOption{ID: "page:" + g.ID + ":on", Cat: g.Enable[0], Item: g.Enable[1], Name: g.Name + " (switch)", Value: "true", When: time.Now()})
		}
	}
	if value == was && (value == "" || !running) && optionsModIn(b.Path) {
		return s.gameOptsStatus(i)
	}
	if _, err := writeOptionsMod(FindComponents(s.Paths, s.Settings.Get()), b.Path, next, running); err != nil {
		return nil, err
	}
	if running {
		gwMu.Lock()
		gwLast[b.Path] = time.Now()
		gwMu.Unlock()
	}
	os.WriteFile(filepath.Join(keptDir(b.Path), pageMark), []byte("put in by Frame Lab's Game options page\n"), 0o644)
	if sc != nil {
		var keep []KeptOption
		for _, k := range sc.Kept {
			if !keys[analysis.OptionKey(k.Cat, k.Item)] {
				keep = append(keep, k)
			}
		}
		if len(keep) != len(sc.Kept) {
			sc.Kept = keep
			s.saveSchedule(sc)
		}
	}
	text := fmt.Sprintf("Game option: %s = %s (%s / %s)", g.Name, gameOptText(*g, value), g.Cat, g.Item)
	if value == "" {
		text = fmt.Sprintf("Game option back to the game's own: %s (%s / %s)", g.Name, g.Cat, g.Item)
	}
	s.gameOptNote(b, "gameopt:"+g.ID, text)
	s.Ctl.diag(fmt.Sprintf("game option in %s: %s / %s = %q (was %q)%s", b.Name, g.Cat, g.Item, value, was, map[bool]string{true: ", set while the game runs", false: ""}[running]))
	return s.gameOptsStatus(i)
}

func gameOptText(g GameOpt, v string) string {
	if v == "" {
		return "the game's own"
	}
	t := analysis.OptionText(v)
	if g.Unit != "" && g.Kind == "num" {
		t += " " + g.Unit
	}
	return t
}

// gameOptNote: a change in the open Optimise round (the routes are recorded again after it). Setting the
// same option again before anything was recorded replaces the earlier note.
func (s *Server) gameOptNote(b BuildCfg, fix, text string) {
	schedMu.Lock()
	defer schedMu.Unlock()
	buildModsForget(b.Path)
	sc := s.loadSchedule(b.Name)
	if sc == nil {
		return
	}
	if len(sc.Rounds) == 0 {
		if !sc.Started.IsZero() {
			// routes recorded so far were made with the old value: they are asked for again
			sc.Started, sc.Conflicts, sc.Analysed = time.Now(), time.Time{}, time.Time{}
			s.saveSchedule(sc)
		}
		return
	}
	cur := &sc.Rounds[len(sc.Rounds)-1]
	if !cur.Closed.IsZero() {
		return
	}
	now := time.Now()
	for k := len(cur.Changes) - 1; k >= 0; k-- {
		if c := cur.Changes[k]; c.Fix == fix && len(s.testSessions(b.Path, c.When)) == 0 {
			cur.Changes = append(cur.Changes[:k], cur.Changes[k+1:]...)
			break
		}
	}
	cur.Changes = append(cur.Changes, SchedChange{When: now, Text: text, Fix: fix})
	cur.Changed = now
	s.saveSchedule(sc)
}

// gameOptsMod puts Frame Lab's options mod into a build (so changes can go in while playing) or takes it
// out with everything it sets. The game has to be closed.
func (s *Server) gameOptsMod(i int, on bool) (*GameOptsStatus, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first: Cyber Engine Tweaks reads its mods when the game starts")
	}
	if on {
		if ok, why := optionsPossible(b.Path); !ok {
			return nil, errors.New(why)
		}
		sc := s.loadSchedule(b.Name)
		if _, err := writeOptionsMod(FindComponents(s.Paths, s.Settings.Get()), b.Path, keptNow(b.Path, sc, DefaultOptions()), false); err != nil {
			return nil, err
		}
		os.WriteFile(filepath.Join(keptDir(b.Path), pageMark), []byte("put in by Frame Lab's Game options page\n"), 0o644)
		s.Ctl.diag("game options: Frame Lab's options mod put into " + b.Name)
		return s.gameOptsStatus(i)
	}
	if st, err := s.scheduleStatus(i); err == nil && st.Trial != nil {
		return nil, fmt.Errorf("\"%s\" is on trial in Optimise: finish its retest or drop it first.", st.Trial.Name)
	}
	n := len(keptNow(b.Path, s.loadSchedule(b.Name), nil))
	if err := os.RemoveAll(keptDir(b.Path)); err != nil {
		return nil, fmt.Errorf("could not take %s out: %w", keptDir(b.Path), err)
	}
	if sc := s.loadSchedule(b.Name); sc != nil && len(sc.Kept) > 0 {
		sc.Kept = nil
		s.saveSchedule(sc)
	}
	if n > 0 {
		s.gameOptNote(b, "gameopt:all", "Game options: Frame Lab's options mod taken out, every option is the game's own again")
	}
	s.Ctl.diag(fmt.Sprintf("game options: Frame Lab's options mod taken out of %s (%d set)", b.Name, n))
	return s.gameOptsStatus(i)
}
