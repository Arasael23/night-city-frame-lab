package analysis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Test routes (2.5): the kinds of play the Optimise schedule is measured on: a walk, a drive, a fight.
// A recording covers a route when the game's position lines (see marks.go) show V on foot or in a
// vehicle, in a fight or out of one, for long enough. Where is up to the player the first time; the
// districts of that first recording are then pinned (PinRoute), so every test after a change is asked
// for in the same places and a route is judged on the same stretch of play before and after.

// RouteDef: one test route. The set can be replaced with data\optimise\routes.json.
type RouteDef struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	How     string   `json:"how"`              // what to do, as the player reads it
	Seconds float64  `json:"seconds"`          // play time that has to count
	Where   []string `json:"where,omitempty"`  // district names; the district or one above it has to have one of them in its name ("=Name": be exactly that one). Empty: anywhere
	Both    []string `json:"both,omitempty"`   // every one of these has to be visited (a drive from one to the other)
	Mode    string   `json:"mode,omitempty"`   // "foot", "car" or "" for either
	Fight   bool     `json:"fight,omitempty"`  // true: only time in a fight counts. false: only time out of one
	NoPin   bool     `json:"no_pin,omitempty"` // a retest may be anywhere again (a fight is where the enemies are)
}

// DefaultRoutes: the routes Frame Lab ships with. No place is asked for: not everyone knows the map,
// has Phantom Liberty or has left Watson yet.
func DefaultRoutes() []RouteDef {
	return []RouteDef{
		{ID: "walk", Name: "A walk", How: "On foot, anywhere in the city, out of a fight.", Seconds: 180, Mode: "foot"},
		{ID: "drive", Name: "A drive", How: "By car or bike, anywhere.", Seconds: 180, Mode: "car"},
		{ID: "fight", Name: "A fight", How: "On foot, any fight: a gang hideout or an NCPD scanner hustle.", Seconds: 60, Mode: "foot", Fight: true, NoPin: true},
	}
}

// RouteTip: where a route is best played, shown wherever the routes are listed.
const RouteTip = "Any place counts, but busy places tell the most: around megabuilding H10 in Little China, Kabuki, City Center, Dogtown. " +
	"After a change, Frame Lab asks for the walk and the drive in the same districts again, so pick places you can get back to."

// LoadRoutes: the routes in path (a JSON list of RouteDef), or the default ones when the file isn't
// there or doesn't hold at least one usable route.
func LoadRoutes(path string) []RouteDef {
	b, err := os.ReadFile(path)
	if err != nil {
		return DefaultRoutes()
	}
	var in []RouteDef
	if json.Unmarshal(b, &in) != nil {
		return DefaultRoutes()
	}
	var out []RouteDef
	seen := map[string]bool{}
	for _, d := range in {
		d.ID = strings.TrimSpace(d.ID)
		if d.ID == "" || seen[d.ID] || d.Seconds < 10 || strings.TrimSpace(d.Name) == "" {
			continue
		}
		if d.Mode != "foot" && d.Mode != "car" {
			d.Mode = ""
		}
		seen[d.ID] = true
		out = append(out, d)
	}
	if len(out) == 0 {
		return DefaultRoutes()
	}
	return out
}

// Place: one position line of the game.
type Place struct {
	UnixMs float64
	Chain  []string // the district, then the ones above it, as the game's scripts name them
	Car    bool
	Fight  bool
}

// ParsePlace reads "at <district> / <parent> [/ <grandparent>] <foot|car> <fight|calm>".
func ParsePlace(m GameMark) (Place, bool) {
	t, ok := strings.CutPrefix(m.Text, "at ")
	if !ok {
		return Place{}, false
	}
	f := strings.Fields(t)
	if len(f) < 3 {
		return Place{}, false
	}
	mode, fight := f[len(f)-2], f[len(f)-1]
	if (mode != "foot" && mode != "car") || (fight != "fight" && fight != "calm") {
		return Place{}, false
	}
	p := Place{UnixMs: m.UnixMs, Car: mode == "car", Fight: fight == "fight"}
	for _, part := range strings.Split(strings.Join(f[:len(f)-2], " "), "/") {
		if part = strings.TrimSpace(part); part != "" && part != "-" {
			p.Chain = append(p.Chain, part)
		}
	}
	return p, true
}

// normPlace: a district name without case, spaces or punctuation ("Little China", "LittleChina" and
// "little_china" are the same place).
func normPlace(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// in: the place is the named district or inside it. A plain name only has to be part of the game's
// name (it names some smaller areas after the district they are in, like Dogtown_Akebono). "=Name" is
// exactly that district or an area inside it, and "=-" is open country with no district at all: that
// is how the places of a first recording are pinned.
func (p *Place) in(name string) bool {
	if exact, ok := strings.CutPrefix(name, "="); ok {
		n := normPlace(exact)
		if n == "" {
			return len(p.Chain) == 0
		}
		for _, c := range p.Chain {
			if normPlace(c) == n {
				return true
			}
		}
		return false
	}
	n := normPlace(name)
	if n == "" {
		return false
	}
	for _, c := range p.Chain {
		if strings.Contains(normPlace(c), n) {
			return true
		}
	}
	return false
}

// pinName: the district a position counts as when a route is pinned: the one the map shows (Little
// China, Kabuki, Dogtown), not a smaller named area inside it (Dogtown_Akebono counts as Dogtown).
func (p *Place) pinName() string {
	if len(p.Chain) == 0 {
		return "-"
	}
	if strings.Contains(p.Chain[0], "_") && len(p.Chain) > 1 {
		return p.Chain[1]
	}
	return p.Chain[0]
}

// PlaceTitle: a district as the player reads it ("LittleChina" -> "Little China", "=-" -> "open country").
func PlaceTitle(name string) string {
	name = strings.TrimPrefix(name, "=")
	switch normPlace(name) {
	case "":
		return "open country"
	case "japantown":
		return "Japantown"
	case "dogtown":
		return "Dogtown"
	}
	var b strings.Builder
	rs := []rune(strings.ReplaceAll(name, "_", " "))
	for i, r := range rs {
		if i > 0 && r >= 'A' && r <= 'Z' && rs[i-1] >= 'a' && rs[i-1] <= 'z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// PlaceTitles: several districts as one phrase ("Little China, Kabuki or Downtown").
func PlaceTitles(names []string, last string) string {
	var t []string
	for _, n := range names {
		t = append(t, PlaceTitle(n))
	}
	switch len(t) {
	case 0:
		return ""
	case 1:
		return t[0]
	}
	return strings.Join(t[:len(t)-1], ", ") + " " + last + " " + t[len(t)-1]
}

// PinRoute: the route with the districts of its first recording pinned, as every later test is asked
// for. A route that isn't pinned (a fight) stays as it is.
func PinRoute(d RouteDef, first RouteStat) RouteDef {
	if d.NoPin || len(first.Where) == 0 {
		return d
	}
	p := d
	p.Where = nil
	for _, w := range first.Where {
		p.Where = append(p.Where, "="+w.Name)
	}
	p.How = "Again in " + PlaceTitles(p.Where, "or") + ": where you did it the first time."
	return p
}

// Pinned: the route asks for exact districts (those of a first recording)
func (d *RouteDef) Pinned() []string {
	var out []string
	for _, w := range d.Where {
		if strings.HasPrefix(w, "=") {
			out = append(out, w)
		}
	}
	if len(out) != len(d.Where) {
		return nil
	}
	return out
}

// RouteAsk: a route as one line of a to-do list ("A walk, 3 min, in Little China or Kabuki").
func RouteAsk(d RouteDef) string {
	t := fmt.Sprintf("%.0f s", d.Seconds)
	if d.Seconds >= 120 {
		t = fmt.Sprintf("%.0f min", d.Seconds/60)
	}
	out := d.Name + ", " + t
	switch {
	case len(d.Pinned()) > 0:
		out += ", in " + PlaceTitles(d.Where, "or")
	case len(d.Where) > 0:
		out += ", in " + strings.Join(d.Where, " or ")
	default:
		out += ", anywhere"
	}
	return out
}

func (p *Place) label() string {
	if len(p.Chain) == 0 {
		return "no district"
	}
	return strings.Join(p.Chain, " / ")
}

func (d *RouteDef) matches(p *Place) bool {
	if (d.Mode == "car" && !p.Car) || (d.Mode == "foot" && p.Car) || d.Fight != p.Fight {
		return false
	}
	if len(d.Where) == 0 {
		return true
	}
	for _, w := range d.Where {
		if p.in(w) {
			return true
		}
	}
	return false
}

const (
	placeEveryMs = 5000.0  // the game writes a position line every 5 seconds of play
	placeGapMs   = 12000.0 // a longer gap is a pause, a menu or a loading screen: the line before it stops counting after 5 seconds
)

// RouteStat: one route in one recording.
type RouteStat struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	NeedS      float64      `json:"need_s"`
	Seconds    float64      `json:"seconds"`               // time of this recording that counts for the route
	Done       bool         `json:"done"`                  // enough of it, and every place it has to pass through
	Missing    string       `json:"missing,omitempty"`     // what is still missing when there was some time but it isn't done
	Frames     int          `json:"frames,omitempty"`      // frames inside that time
	AvgFps     float64      `json:"avg_fps,omitempty"`     // frames the game rendered (no generated ones)
	Low1Fps    float64      `json:"low1_fps,omitempty"`    // 1% low
	MedianMs   float64      `json:"median_ms,omitempty"`   //
	WorstMs    float64      `json:"worst_ms,omitempty"`    // slowest frame
	Stutters   int          `json:"stutters"`              // frames over the session's stutter limit
	Stutters10 float64      `json:"stutters_10"`           // per 10 minutes of the route
	ScriptS    float64      `json:"script_s,omitempty"`    // seconds of the route with script time measured
	ScriptMsS  float64      `json:"script_ms_s,omitempty"` // script time of all mods on the main thread, ms per second
	Scripts    []OwnerCost  `json:"scripts,omitempty"`     // the mods behind most of it
	Where      []RoutePlace `json:"where,omitempty"`       // the districts the counted time was in, longest first
	Spans      [][2]float64 `json:"-"`                     // the counted time (Unix ms), for costs per route (not saved)
}

// RoutePlace: part of a route's counted time, by district (as the game names it; "-" is open country).
type RoutePlace struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
}

// PlaceTime: how long V was somewhere in a recording, by what V was doing.
type PlaceTime struct {
	Place     string  `json:"place"`
	WalkS     float64 `json:"walk_s"`     // on foot, out of a fight
	DriveS    float64 `json:"drive_s"`    // in a vehicle, out of a fight
	FightS    float64 `json:"fight_s"`    // on foot, in a fight
	CarFightS float64 `json:"carfight_s"` // in a vehicle, in a fight
}

func (t *PlaceTime) total() float64 { return t.WalkS + t.DriveS + t.FightS + t.CarFightS }

// RouteReport: the test routes of one recording.
type RouteReport struct {
	Lines  int         `json:"lines"`          // position lines the game wrote
	Seen   []PlaceTime `json:"seen,omitempty"` // where the recording was, longest first
	Routes []RouteStat `json:"routes"`
	Note   string      `json:"note,omitempty"` // why there is nothing to go on, when there isn't
}

// routeInput: what the route numbers are made from (all times Unix ms, sorted).
type routeInput struct {
	FrameU  []float64 // start of each frame while playing
	FrameMs []float64
	SpikeU  []float64 // start of each stutter
	Script  func(ua, ub float64) (perMod map[string]float64, seconds float64)
	// Script: script time per mod on the main thread inside a span (ms) and how many seconds of the
	// span were timed. nil when the recording has no script time.
}

const FileRoutes = "routes.json"

// ReadRoutes: the routes of an analysed session (nil when it has none).
func ReadRoutes(dir string) *RouteReport {
	b, err := os.ReadFile(filepath.Join(dir, FileRoutes))
	if err != nil {
		return nil
	}
	r := &RouteReport{}
	if json.Unmarshal(b, r) != nil {
		return nil
	}
	return r
}

func writeRoutes(dir string, r *RouteReport) {
	if r == nil {
		os.Remove(filepath.Join(dir, FileRoutes))
		return
	}
	if b, err := json.Marshal(r); err == nil {
		os.WriteFile(filepath.Join(dir, FileRoutes), b, 0o644)
	}
}

// RouteProgress: how far the routes are in a recording that is still running, from the position lines
// alone (no frames yet). Done here means long enough and in the right places.
func RouteProgress(defs []RouteDef, marks []GameMark) []RouteStat {
	return buildRoutes(defs, marks, nil).Routes
}

// buildRoutes works out which routes a recording covers and each route's numbers.
func buildRoutes(defs []RouteDef, marks []GameMark, in *routeInput) *RouteReport {
	rep := &RouteReport{}
	var places []Place
	for _, m := range marks {
		if p, ok := ParsePlace(m); ok {
			places = append(places, p)
		}
	}
	sort.SliceStable(places, func(i, j int) bool { return places[i].UnixMs < places[j].UnixMs })
	rep.Lines = len(places)
	switch {
	case len(marks) == 0:
		rep.Note = "The game sent no markers in this recording, so Frame Lab can't tell where you played. They need Cyber Engine Tweaks in the build."
	case len(places) == 0:
		rep.Note = "This recording has no position lines (it was made by an older Frame Lab, on a game older than 2.5, or V never got into the game world), so no test route can be read from it."
	}
	// each line stands for the time until the next one
	end := func(i int) float64 {
		if i+1 < len(places) && places[i+1].UnixMs-places[i].UnixMs <= placeGapMs {
			return places[i+1].UnixMs
		}
		return places[i].UnixMs + placeEveryMs
	}
	seen := map[string]*PlaceTime{}
	for i := range places {
		p := &places[i]
		s := (end(i) - p.UnixMs) / 1000
		pt := seen[p.label()]
		if pt == nil {
			pt = &PlaceTime{Place: p.label()}
			seen[p.label()] = pt
		}
		switch {
		case p.Car && p.Fight:
			pt.CarFightS += s
		case p.Car:
			pt.DriveS += s
		case p.Fight:
			pt.FightS += s
		default:
			pt.WalkS += s
		}
	}
	for _, pt := range seen {
		rep.Seen = append(rep.Seen, *pt)
	}
	sort.Slice(rep.Seen, func(i, j int) bool {
		a, b := rep.Seen[i].total(), rep.Seen[j].total()
		if a != b {
			return a > b
		}
		return rep.Seen[i].Place < rep.Seen[j].Place
	})
	if len(rep.Seen) > 12 {
		rep.Seen = rep.Seen[:12]
	}
	for _, d := range defs {
		var spans [][2]float64
		passed := map[string]bool{}
		where := map[string]float64{}
		for i := range places {
			p := &places[i]
			if !d.matches(p) {
				continue
			}
			a, b := p.UnixMs, end(i)
			if n := len(spans); n > 0 && a <= spans[n-1][1] {
				spans[n-1][1] = b
			} else {
				spans = append(spans, [2]float64{a, b})
			}
			for _, k := range d.Both {
				if p.in(k) {
					passed[k] = true
				}
			}
			where[p.pinName()] += (b - a) / 1000
		}
		rep.Routes = append(rep.Routes, routeStat(d, spans, passed, where, in))
	}
	return rep
}

func routeStat(d RouteDef, spans [][2]float64, passed map[string]bool, where map[string]float64, in *routeInput) RouteStat {
	st := RouteStat{ID: d.ID, Name: d.Name, NeedS: d.Seconds, Spans: spans}
	for _, s := range spans {
		st.Seconds += (s[1] - s[0]) / 1000
	}
	st.Seconds = round1(st.Seconds)
	for p, sec := range where {
		st.Where = append(st.Where, RoutePlace{Name: p, Seconds: round1(sec)})
	}
	sort.Slice(st.Where, func(i, j int) bool {
		if st.Where[i].Seconds != st.Where[j].Seconds {
			return st.Where[i].Seconds > st.Where[j].Seconds
		}
		return st.Where[i].Name < st.Where[j].Name
	})
	var missing []string
	for _, k := range d.Both {
		if !passed[k] {
			missing = append(missing, k)
		}
	}
	if in != nil {
		var ms []float64
		playMs := 0.0
		scriptMs, scriptS := map[string]float64{}, 0.0
		for _, s := range spans {
			lo := sort.SearchFloat64s(in.FrameU, s[0])
			for i := lo; i < len(in.FrameU) && in.FrameU[i] < s[1]; i++ {
				v := in.FrameMs[i]
				if v > st.WorstMs {
					st.WorstMs = round2(v)
				}
				if v >= 5000 { // the game hung or stopped: not play (as in the session's own numbers)
					continue
				}
				ms = append(ms, v)
				playMs += v
			}
			lo = sort.SearchFloat64s(in.SpikeU, s[0])
			for i := lo; i < len(in.SpikeU) && in.SpikeU[i] < s[1]; i++ {
				st.Stutters++
			}
			if in.Script != nil {
				per, secs := in.Script(s[0], s[1])
				scriptS += secs
				for o, v := range per {
					scriptMs[o] += v
				}
			}
		}
		if n := len(ms); n > 0 && playMs > 0 {
			sort.Float64s(ms)
			st.Frames = n
			st.AvgFps = round1(float64(n) / (playMs / 1000))
			st.MedianMs = round2(pctl(ms, 50))
			st.Low1Fps = round1(1000 / pctl(ms, 99))
			st.Stutters10 = round1(float64(st.Stutters) / (playMs / 600000))
		}
		if scriptS >= 30 {
			st.ScriptS = round1(scriptS)
			sum := 0.0
			for o, v := range scriptMs {
				if scriptOwnerIsMod(o) {
					sum += v
					st.Scripts = append(st.Scripts, OwnerCost{Owner: o, MsPerSec: round3(v / scriptS)})
				}
			}
			st.ScriptMsS = round2(sum / scriptS)
			sort.Slice(st.Scripts, func(i, j int) bool {
				if st.Scripts[i].MsPerSec != st.Scripts[j].MsPerSec {
					return st.Scripts[i].MsPerSec > st.Scripts[j].MsPerSec
				}
				return st.Scripts[i].Owner < st.Scripts[j].Owner
			})
			if len(st.Scripts) > 8 {
				st.Scripts = st.Scripts[:8]
			}
		}
	}
	switch {
	case st.Seconds <= 0:
	case st.Frames == 0 && in != nil:
		st.Missing = "no frames were recorded during it"
	case len(missing) > 0:
		st.Missing = "never got to " + strings.Join(missing, " or ")
	case st.Seconds < d.Seconds:
		st.Missing = fmt.Sprintf("%.0f of %.0f seconds", st.Seconds, d.Seconds)
	default:
		st.Done = true
	}
	return st
}

// scriptOwnerIsMod: a mod, not the game's own scripts, CET itself or Frame Lab's markers
func scriptOwnerIsMod(o string) bool {
	switch o {
	case "game scripts", "scripts without a source file", "CET itself", "NightCityFrameLab":
		return false
	}
	return true
}

// ---------------------------------------------------------------- one route before and after

// RouteCompare: the same route in two recordings.
type RouteCompare struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Rows    []Follow `json:"rows"`
	Verdict string   `json:"verdict"` // better / same / worse / mixed
}

var routeMetrics = []struct {
	def  metricDef
	get  func(s *RouteStat) (float64, bool)
	main bool // counts for the route's verdict
}{
	{metricDef{"Average fps", "fps", false, 2}, func(s *RouteStat) (float64, bool) { return s.AvgFps, s.AvgFps > 0 }, true},
	{metricDef{"1% low", "fps", false, 2}, func(s *RouteStat) (float64, bool) { return s.Low1Fps, s.Low1Fps > 0 }, true},
	{metricDef{"Stutters per 10 minutes", "", true, 2}, func(s *RouteStat) (float64, bool) { return s.Stutters10, s.Frames > 0 }, true},
	{metricDef{"Script time of mods", "ms per second", true, 0.5}, func(s *RouteStat) (float64, bool) { return s.ScriptMsS, s.ScriptS > 0 }, false},
}

// CompareRoute puts a route's numbers after a change next to the ones before it.
func CompareRoute(was, now RouteStat) RouteCompare {
	c := RouteCompare{ID: now.ID, Name: now.Name}
	better, worse := 0, 0
	for _, m := range routeMetrics {
		a, ok1 := m.get(&was)
		b, ok2 := m.get(&now)
		if !ok1 || !ok2 {
			continue
		}
		v := judge(m.def, a, b)
		c.Rows = append(c.Rows, Follow{Label: m.def.Label, Unit: m.def.Unit, Was: a, Now: b, Verdict: v})
		if m.main {
			switch v {
			case "better":
				better++
			case "worse":
				worse++
			}
		}
	}
	switch {
	case better > 0 && worse > 0:
		c.Verdict = "mixed"
	case better > 0:
		c.Verdict = "better"
	case worse > 0:
		c.Verdict = "worse"
	default:
		c.Verdict = "same"
	}
	return c
}
