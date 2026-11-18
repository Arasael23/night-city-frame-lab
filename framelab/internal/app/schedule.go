package app

// Optimise schedule (2.5). Optimise changes nothing in a build until the build has been measured, and
// every change is measured again on the same test:
//
//	1. start the test round, then record the test routes (a walk, a drive, a fight, wherever the
//	   player likes): Frame Lab ticks each one off from the game's position lines
//	2. run the conflict check
//	3. analyse the build: from here the fixes are open
//	4. after any change (a Frame Lab fix, an undo, or mod files changed by hand) the same routes have
//	   to be recorded again, in the districts of the first recordings and with the same game settings;
//	   as soon as one recording is made after the change, nothing more can be changed until all are in
//	5. the routes before and after are put next to each other, and the next round starts from there
//	6. one of the game's own options can be put on trial instead of a fix (options.go): the round then
//	   has that one change, the option is set in the retest recordings only, and when the retest is in
//	   the player keeps it or drops it before anything else can be changed
//
// The state of a build is data\optimise\schedule\<build>.json. Which recordings count is worked out
// from the sessions themselves each time, so deleting or re-analysing a session can't leave it wrong.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"framelab/internal/analysis"
)

// RouteRun: one route in the recording that counts for it.
type RouteRun struct {
	Session string             `json:"session"`
	Title   string             `json:"title"`
	When    time.Time          `json:"when"`
	Stat    analysis.RouteStat `json:"stat"`
}

// SchedChange: something that was changed in the build during a round.
type SchedChange struct {
	When time.Time `json:"when"`
	Text string    `json:"text"`
	Fix  string    `json:"fix,omitempty"`  // the fix in Frame Lab's ledger (it has an Undo)
	Undo bool      `json:"undo,omitempty"` // this entry is the undo of that fix
}

// SchedRound: one turn of change and retest.
type SchedRound struct {
	N        int                     `json:"n"`
	Opened   time.Time               `json:"opened"`
	Base     map[string]RouteRun     `json:"base"`               // every route before this round's changes
	Settings map[string]string       `json:"settings,omitempty"` // the game settings the test is played with
	Scripts  bool                    `json:"scripts"`            // script time was measured in the base recordings
	Mods     []analysis.ModFile      `json:"mods,omitempty"`     // the build's mod files when the round opened
	Changes  []SchedChange           `json:"changes,omitempty"`  // Frame Lab's fixes and undos in this round
	Changed  time.Time               `json:"changed,omitempty"`  // the last of them: only recordings after it count for the retest
	ByHand   []string                `json:"by_hand,omitempty"`  // mod files changed outside Frame Lab (filled in when the round closes)
	After    map[string]RouteRun     `json:"after,omitempty"`
	Closed   time.Time               `json:"closed,omitempty"`
	Result   []analysis.RouteCompare `json:"result,omitempty"`
	Verdict  string                  `json:"verdict,omitempty"`
	Kind     string                  `json:"kind,omitempty"`  // better / same / worse / mixed
	Trial    *OptTrial               `json:"trial,omitempty"` // a game option on trial: the only change of this round
}

// OptTrial: a game option on trial in a round. It is set in the retest recordings only; once the
// retest is in, the player keeps it or drops it.
type OptTrial struct {
	ID      string    `json:"id"`
	Cat     string    `json:"cat"`
	Item    string    `json:"item"`
	Name    string    `json:"name"`
	Was     string    `json:"was"`   // the game's value before the trial
	Value   string    `json:"value"` // the value on trial
	When    time.Time `json:"when"`
	Outcome string    `json:"outcome,omitempty"` // "" while it runs or waits for the decision, then kept / dropped
	Decided time.Time `json:"decided,omitempty"`
}

// Schedule: the saved state of one build.
type Schedule struct {
	Build     string              `json:"build"`
	Started   time.Time           `json:"started"`          // recordings from before this don't count
	Routes    []analysis.RouteDef `json:"routes"`           // fixed when the round starts, so every retest is the same test
	Pinned    []analysis.RouteDef `json:"pinned,omitempty"` // the same routes with the districts of the first recordings: what every retest is asked for
	Conflicts time.Time           `json:"conflicts,omitempty"`
	Analysed  time.Time           `json:"analysed,omitempty"`
	Rounds    []SchedRound        `json:"rounds,omitempty"`
	Kept      []KeptOption        `json:"kept,omitempty"` // game options kept after their trial (Frame Lab's kept-options mod sets them)
}

// SchedRoute: one route as the Optimise tab shows it.
type SchedRoute struct {
	Def      analysis.RouteDef `json:"def"`
	Done     bool              `json:"done"`
	Run      *RouteRun         `json:"run,omitempty"`      // the recording that counts in this step
	Best     *RouteRun         `json:"best,omitempty"`     // the furthest attempt so far when it isn't done
	Base     *RouteRun         `json:"base,omitempty"`     // in a retest and while fixing: the numbers before
	Rejected string            `json:"rejected,omitempty"` // a recording covered it but doesn't count, and why
	Where    string            `json:"where,omitempty"`    // the districts its recording was in ("Little China, Kabuki")
	Again    string            `json:"again,omitempty"`    // the districts it is asked for again ("Little China or Kabuki")
}

// SchedRec: one test recording in plain words.
type SchedRec struct {
	Session string    `json:"session"`
	Title   string    `json:"title"`
	When    time.Time `json:"when"`
	Did     string    `json:"did"`            // "3 min 5 s on foot (Little China, Kabuki), 2 min driving (Downtown), no fight"
	Counts  []string  `json:"counts"`         // route by route: recorded, how far, or why nothing counted
	Note    string    `json:"note,omitempty"` // why the whole recording doesn't count (other settings, other mod files)
}

// SchedStatus: where a build stands.
type SchedStatus struct {
	Build   string        `json:"build"`
	Step    string        `json:"step"`    // start, routes, conflicts, analyse, fix, retest, decide (keep or drop the option that was on trial)
	Locked  bool          `json:"locked"`  // no fix can be made now
	Why     string        `json:"why"`     // why not, or what comes next
	Routes  []SchedRoute  `json:"routes"`  //
	Done    int           `json:"done"`    // routes recorded in this step
	Round   int           `json:"round"`   // the open round (0 before the first)
	Changes []SchedChange `json:"changes"` // Frame Lab's changes in the open round
	ByHand  []string      `json:"by_hand"` // mod files changed outside Frame Lab in the open round
	Frozen  bool          `json:"frozen"`  // a recording was made since the last change: the round's changes are fixed
	Last    *SchedRound   `json:"last"`    // the round finished last, with its comparison
	Rounds  int           `json:"rounds"`  // finished rounds
	Notes   []string      `json:"notes"`   //
	Recs    []SchedRec    `json:"recs"`    // the test recordings of this step, newest first, in plain words
	Tip     string        `json:"tip"`     // where the routes are best played (until the places are fixed by the first recordings)
	Started string        `json:"started"` // when the test round was started
	Trial   *OptTrial     `json:"trial"`   // the game option on trial in the open round, or waiting for the decision
}

var (
	schedMu sync.Mutex // one schedule operation at a time

	tsMu    sync.Mutex
	tsCache = map[string]*testSession{} // session folder -> what the schedule needs of it

	fpMu    sync.Mutex
	fpCache = map[string]fpEntry{} // game folder -> its mod files now
)

type fpEntry struct {
	at   time.Time
	mods []analysis.ModFile
}

func (s *Server) schedulePath(build string) string {
	return filepath.Join(s.Paths.Data, "optimise", "schedule", strings.TrimSpace(reUnsafe.ReplaceAllString(build, "_"))+".json")
}

func (s *Server) routesPath() string { return filepath.Join(s.Paths.Data, "optimise", "routes.json") }

func (s *Server) loadSchedule(build string) *Schedule {
	b, err := os.ReadFile(s.schedulePath(build))
	if err != nil {
		return nil
	}
	sc := &Schedule{}
	if json.Unmarshal(b, sc) != nil || sc.Started.IsZero() || len(sc.Routes) == 0 {
		return nil
	}
	return sc
}

func (s *Server) saveSchedule(sc *Schedule) error {
	b, err := json.MarshalIndent(sc, "", " ")
	if err != nil {
		return err
	}
	p := s.schedulePath(sc.Build)
	os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ---------------------------------------------------------------- what a recording was made with

// testSession: a finished test recording, as the schedule sees it.
type testSession struct {
	ID       string
	Title    string
	Game     string // the game folder it was recorded in
	Created  time.Time
	Routes   *analysis.RouteReport
	Settings map[string]string // the game settings that decide how it runs
	Scripts  bool
	Mods     []analysis.ModFile
	Options  *analysis.OptionsReport // the game's options in it, and the one that was on trial
	stamp    time.Time               // of its routes.json, to notice a new analysis
}

// testSettings: the game settings that change how the game runs (graphics, resolution, frame cap).
// Sound, controls, colours and brightness are left out: two runs that differ only there are the same test.
func testSettings(all map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range all {
		l := strings.ToLower(k)
		switch {
		case strings.HasPrefix(l, "/graphics/"):
			out[k] = v
		case strings.HasPrefix(l, "/video/display/"):
			switch l[len("/video/display/"):] {
			case "resolution", "windowmode", "vsync", "maximumfps_onoff", "maximumfps_value", "reflexmode", "hdrmodes", "xellmode", "xellframecap", "fsr_antilag2mode":
				out[k] = v
			}
		}
	}
	return out
}

// settingsDiff: the settings of a recording that differ from the ones the test is played with, as
// "DLSS: Performance instead of Quality" (at most three, then a count).
func settingsDiff(want, got map[string]string) string {
	var d []string
	for k, a := range want {
		if b, ok := got[k]; ok && a != b {
			d = append(d, fmt.Sprintf("%s: %s instead of %s", k[strings.LastIndexByte(k, '/')+1:], b, a))
		}
	}
	sort.Strings(d)
	if len(d) > 3 {
		return strings.Join(d[:3], ", ") + fmt.Sprintf(" and %d more", len(d)-3)
	}
	return strings.Join(d, ", ")
}

// slashed: a mod path of a snapshot (Windows style) with forward slashes, lower case
func slashed(path string) string { return strings.ToLower(strings.ReplaceAll(path, `\`, "/")) }

// modName: the last part of a mod path
func modName(path string) string {
	p := strings.ReplaceAll(path, `\`, "/")
	return p[strings.LastIndexByte(p, '/')+1:]
}

// Frame Lab's own parts: the session parts are in a snapshot or not depending on timing and never a
// change to the build; the kept-options mod is a change the schedule notes itself (keep / take out)
func ownPart(path string) bool {
	l := slashed(path)
	return l == "red4ext/plugins/hitchrecorder" || l == "r6/scripts/nightcityframelab" ||
		l == "bin/x64/plugins/cyber_engine_tweaks/mods/nightcityframelab" ||
		l == "bin/x64/plugins/cyber_engine_tweaks/mods/nightcityframelaboptions" // the kept game options: the schedule tracks those itself
}

// modsDiff: what differs between two lists of a build's mod files. Left out: Frame Lab's own session
// parts, and a different size where the game or the mod itself writes into the folder while playing
// (CET mods and plugin folders keep their settings and logs there), which would look like a change
// after every run.
func modsDiff(was, now []analysis.ModFile) []string {
	a := map[string]analysis.ModFile{}
	for _, m := range was {
		if !ownPart(m.Path) {
			a[m.Path] = m
		}
	}
	var added, removed, replaced []string
	seen := map[string]bool{}
	for _, m := range now {
		if ownPart(m.Path) {
			continue
		}
		seen[m.Path] = true
		old, ok := a[m.Path]
		if !ok {
			added = append(added, modName(m.Path))
			continue
		}
		if old.Size != m.Size && modStatic(m.Path) {
			replaced = append(replaced, modName(m.Path))
		}
	}
	for p := range a {
		if !seen[p] {
			removed = append(removed, modName(p))
		}
	}
	var out []string
	add := func(what string, names []string) {
		if len(names) == 0 {
			return
		}
		sort.Strings(names)
		n := len(names)
		if n > 4 {
			names = append(names[:4:4], fmt.Sprintf("%d more", n-4))
		}
		out = append(out, fmt.Sprintf("%d %s: %s", n, what, strings.Join(names, ", ")))
	}
	add("added", added)
	add("removed", removed)
	add("replaced", replaced)
	return out
}

// modStatic: nothing writes into this mod place while the game runs, so another size means another file
func modStatic(path string) bool {
	l := slashed(path)
	for _, p := range []string{"archive/pc/mod/", "mods/", "r6/scripts/", "r6/tweaks/"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// testSessions: the finished test recordings of a game folder made after 'since', newest first.
func (s *Server) testSessions(game string, since time.Time) []*testSession {
	ents, _ := os.ReadDir(s.Paths.Sessions)
	var out []*testSession
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.Paths.Sessions, e.Name())
		st, err := os.Stat(filepath.Join(dir, analysis.FileRoutes))
		if err != nil {
			continue
		}
		tsMu.Lock()
		ts := tsCache[dir]
		tsMu.Unlock()
		if ts == nil || !ts.stamp.Equal(st.ModTime()) {
			m, err := analysis.ReadMeta(dir)
			if err != nil || !m.Finished || !m.Test {
				continue
			}
			ts = &testSession{ID: e.Name(), Title: m.Title, Game: m.Game, Created: m.Created, Scripts: m.Layers.Scripts, stamp: st.ModTime()}
			ts.Routes = analysis.ReadRoutes(dir)
			ts.Options = analysis.ReadOptions(dir)
			if sn := analysis.ReadSnapshot(dir); sn != nil {
				set := sn.SettingsEnd
				if len(set) == 0 {
					set = sn.Settings
				}
				ts.Settings, ts.Mods = testSettings(set), sn.Mods
			}
			tsMu.Lock()
			tsCache[dir] = ts
			tsMu.Unlock()
		}
		if ts.Routes == nil || !strings.EqualFold(filepath.Clean(ts.Game), filepath.Clean(game)) || !ts.Created.After(since) {
			continue
		}
		out = append(out, ts)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// buildMods: the build's mod files as they are now (kept for a minute: walking them takes a moment)
func buildMods(game string) []analysis.ModFile {
	fpMu.Lock()
	defer fpMu.Unlock()
	if e, ok := fpCache[game]; ok && time.Since(e.at) < 60*time.Second {
		return e.mods
	}
	m := modFilesNow(game)
	fpCache[game] = fpEntry{time.Now(), m}
	return m
}

// modFilesNow walks the build's mod places (a variable so the tests can stand in for a real game folder)
var modFilesNow = modFiles

func buildModsForget(game string) {
	fpMu.Lock()
	delete(fpCache, game)
	fpMu.Unlock()
}

// ---------------------------------------------------------------- picking the recordings that count

type pickOpts struct {
	settings map[string]string  // nil: taken from the newest recording that covers a route
	scripts  *bool              // nil: likewise
	mods     []analysis.ModFile // nil: taken from that recording too
	trial    *OptTrial          // the game option that has to be on trial in the recording (nil: none may be)
}

// pickRoutes: for every route the newest recording that covers it under the same conditions. A recording
// with other game settings, other mod files or without script time doesn't count, and the route says why.
func pickRoutes(defs []analysis.RouteDef, sessions []*testSession, o pickOpts) (rows []SchedRoute, ref *testSession, used pickOpts) {
	stat := func(ts *testSession, id string) *analysis.RouteStat {
		for i := range ts.Routes.Routes {
			if ts.Routes.Routes[i].ID == id {
				return &ts.Routes.Routes[i]
			}
		}
		return nil
	}
	// the conditions every recording has to share: given, or those of the newest one that covers a route
	if o.settings == nil || o.scripts == nil || o.mods == nil {
		for _, ts := range sessions {
			covers := false
			for _, d := range defs {
				if st := stat(ts, d.ID); st != nil && st.Done {
					covers = true
				}
			}
			if covers {
				ref = ts
				break
			}
		}
		if ref != nil {
			if o.settings == nil {
				o.settings = ref.Settings
			}
			if o.scripts == nil {
				o.scripts = &ref.Scripts
			}
			if o.mods == nil {
				o.mods = ref.Mods
			}
		}
	}
	why := func(ts *testSession) string { return condWhy(o, ts) }
	for _, d := range defs {
		row := SchedRoute{Def: d}
		for _, ts := range sessions { // newest first
			st := stat(ts, d.ID)
			if st == nil || st.Seconds <= 0 {
				continue
			}
			run := &RouteRun{Session: ts.ID, Title: ts.Title, When: ts.Created, Stat: *st}
			if !st.Done {
				if row.Best == nil || st.Seconds > row.Best.Stat.Seconds {
					row.Best = run
				}
				continue
			}
			if w := why(ts); w != "" {
				if row.Rejected == "" {
					row.Rejected = fmt.Sprintf("The recording \"%s\" covers it but doesn't count: %s.", ts.Title, w)
				}
				continue
			}
			row.Done, row.Run = true, run
			break
		}
		if row.Done {
			row.Best, row.Rejected = nil, ""
		}
		rows = append(rows, row)
	}
	return rows, ref, o
}

// condWhy: why a recording doesn't count under these conditions ("" when it does)
func condWhy(o pickOpts, ts *testSession) string {
	if o.settings != nil && ts.Settings != nil {
		if d := settingsDiff(o.settings, ts.Settings); d != "" {
			return "it was played with other game settings (" + d + ")"
		}
	}
	if o.scripts != nil && *o.scripts != ts.Scripts {
		if ts.Scripts {
			return "script time was measured in it and not in the others"
		}
		return "script time wasn't measured in it (it is switched off in Settings)"
	}
	if o.mods != nil && ts.Mods != nil {
		if d := modsDiff(ts.Mods, o.mods); len(d) > 0 {
			return "the build's mod files have changed since (" + strings.Join(d, "; ") + ")"
		}
	}
	if o.trial == nil {
		if ts.Options != nil {
			for _, t := range ts.Options.Trials {
				if t.Set {
					return fmt.Sprintf("a game option was on trial in it (%s / %s)", t.Cat, t.Item)
				}
			}
		}
		return ""
	}
	t := ts.Options.Trial(o.trial.Cat, o.trial.Item)
	switch {
	case t == nil:
		return "the option on trial wasn't set in it (that needs Frame Lab's markers mod: Cyber Engine Tweaks, game 2.5 or later)"
	case t.Off != "":
		return "the option on trial didn't hold: " + t.Off
	case !analysis.SameOption(t.Want, o.trial.Value):
		return "another value was on trial in it"
	}
	return ""
}

// ---------------------------------------------------------------- in plain words

func durText(sec float64) string {
	s := int(sec + 0.5)
	switch {
	case s >= 60 && s%60 == 0:
		return fmt.Sprintf("%d min", s/60)
	case s >= 60:
		return fmt.Sprintf("%d min %d s", s/60, s%60)
	}
	return fmt.Sprintf("%d s", s)
}

// placeOf: the district of a "where the recording was" line, as the player reads it
func placeOf(label string) string {
	if label == "no district" {
		return "open country"
	}
	leaf, _, _ := strings.Cut(label, " / ")
	if up, rest, ok := strings.Cut(label, " / "); ok && strings.Contains(up, "_") {
		leaf, _, _ = strings.Cut(rest, " / ") // a small named area counts as the district it is in
	}
	return analysis.PlaceTitle(leaf)
}

// kindTime: how long a recording spent doing what a route asks for (on foot or driving, in a fight or
// not), wherever that was, and the districts most of it was in.
func kindTime(seen []analysis.PlaceTime, d analysis.RouteDef) (total float64, places []string) {
	type pt struct {
		name string
		s    float64
	}
	var by []pt
	for _, p := range seen {
		v := 0.0
		if d.Mode != "car" {
			if d.Fight {
				v += p.FightS
			} else {
				v += p.WalkS
			}
		}
		if d.Mode != "foot" {
			if d.Fight {
				v += p.CarFightS
			} else {
				v += p.DriveS
			}
		}
		if v > 0 {
			total += v
			by = append(by, pt{placeOf(p.Place), v})
		}
	}
	sort.SliceStable(by, func(i, j int) bool { return by[i].s > by[j].s })
	seenName := map[string]bool{}
	for _, x := range by {
		if !seenName[x.name] && len(places) < 3 {
			seenName[x.name] = true
			places = append(places, x.name)
		}
	}
	return total, places
}

// recText: one recording in plain words: what was played in it, and what that did for each route.
func recText(ts *testSession, defs []analysis.RouteDef, o pickOpts) SchedRec {
	r := SchedRec{Session: ts.ID, Title: ts.Title, When: ts.Created, Counts: []string{}, Note: condWhy(o, ts)}
	var did []string
	for _, k := range []struct {
		d    analysis.RouteDef
		what string
	}{{analysis.RouteDef{Mode: "foot"}, "on foot"}, {analysis.RouteDef{Mode: "car"}, "driving"}, {analysis.RouteDef{Fight: true}, "in a fight"}} {
		if t, places := kindTime(ts.Routes.Seen, k.d); t >= 5 {
			did = append(did, fmt.Sprintf("%s %s (%s)", durText(t), k.what, strings.Join(places, ", ")))
		}
	}
	switch {
	case len(did) > 0:
		r.Did = strings.Join(did, ", ") + "."
	case ts.Routes.Note != "":
		r.Did = ts.Routes.Note
	default:
		r.Did = "No play was read from it."
	}
	for _, d := range defs {
		var st *analysis.RouteStat
		for i := range ts.Routes.Routes {
			if ts.Routes.Routes[i].ID == d.ID {
				st = &ts.Routes.Routes[i]
			}
		}
		name := d.Name
		total, places := kindTime(ts.Routes.Seen, d)
		again := analysis.PlaceTitles(d.Pinned(), "or")
		switch {
		case st == nil:
			r.Counts = append(r.Counts, name+": not read (the recording was analysed with other routes)")
		case st.Done:
			r.Counts = append(r.Counts, name+": recorded")
		case st.Seconds > 0 && again != "" && total >= st.Seconds+15:
			r.Counts = append(r.Counts, fmt.Sprintf("%s: %s of %s. Another %s was outside %s, where it is asked for again.", name, durText(st.Seconds), durText(d.Seconds), durText(total-st.Seconds), again))
		case st.Seconds > 0 && st.Seconds < d.Seconds:
			r.Counts = append(r.Counts, fmt.Sprintf("%s: %s of %s", name, durText(st.Seconds), durText(d.Seconds)))
		case st.Seconds > 0:
			r.Counts = append(r.Counts, fmt.Sprintf("%s: not complete (%s)", name, st.Missing))
		case again != "" && total >= 10:
			r.Counts = append(r.Counts, fmt.Sprintf("%s: nothing counted. It is asked for in %s again, and this one was in %s.", name, again, strings.Join(places, ", ")))
		default:
			r.Counts = append(r.Counts, name+": none in this recording")
		}
	}
	return r
}

func recTexts(sessions []*testSession, defs []analysis.RouteDef, o pickOpts) []SchedRec {
	out := []SchedRec{}
	for i, ts := range sessions {
		if i >= 6 {
			break
		}
		out = append(out, recText(ts, defs, o))
	}
	return out
}

// routeWhere: the districts a recorded route was in ("Little China, Kabuki")
func routeWhere(st analysis.RouteStat) string {
	var names []string
	for i, w := range st.Where {
		if i >= 5 {
			names = append(names, fmt.Sprintf("%d more", len(st.Where)-5))
			break
		}
		names = append(names, analysis.PlaceTitle(w.Name))
	}
	return strings.Join(names, ", ")
}

// dress fills in where each route was recorded and where it is asked for again
func dress(rows []SchedRoute) []SchedRoute {
	for i := range rows {
		r := &rows[i]
		switch {
		case r.Run != nil:
			r.Where = routeWhere(r.Run.Stat)
		case r.Base != nil:
			r.Where = routeWhere(r.Base.Stat)
		}
		r.Again = analysis.PlaceTitles(r.Def.Pinned(), "or")
	}
	return rows
}

// pinRoutes: the routes as every retest is asked for: in the districts of their first recordings
func pinRoutes(defs []analysis.RouteDef, base map[string]RouteRun) []analysis.RouteDef {
	out := make([]analysis.RouteDef, 0, len(defs))
	for _, d := range defs {
		if r, ok := base[d.ID]; ok {
			d = analysis.PinRoute(d, r.Stat)
		}
		out = append(out, d)
	}
	return out
}

func countDone(rows []SchedRoute) int {
	n := 0
	for _, r := range rows {
		if r.Done {
			n++
		}
	}
	return n
}

func runsOf(rows []SchedRoute) map[string]RouteRun {
	out := map[string]RouteRun{}
	for _, r := range rows {
		if r.Run != nil {
			out[r.Def.ID] = *r.Run
		}
	}
	return out
}

func newest(rows []SchedRoute) time.Time {
	var t time.Time
	for _, r := range rows {
		if r.Run != nil && r.Run.When.After(t) {
			t = r.Run.When
		}
	}
	return t
}

// ---------------------------------------------------------------- the state machine

func (s *Server) buildAt(i int) (BuildCfg, error) {
	cfg := s.Settings.Get()
	if i < 0 || i >= len(cfg.Builds) {
		return BuildCfg{}, errors.New("no game build picked yet: Settings > Find game installs, or Add a build")
	}
	return cfg.Builds[i], nil
}

// scheduleStatus works out where a build stands, moves it on when a step is complete, and saves that.
func (s *Server) scheduleStatus(i int) (*SchedStatus, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	schedMu.Lock()
	defer schedMu.Unlock()
	return s.schedStatusLocked(b), nil
}

func (s *Server) schedStatusLocked(b BuildCfg) *SchedStatus {
	st := &SchedStatus{Build: b.Name, Routes: []SchedRoute{}, Changes: []SchedChange{}, ByHand: []string{}, Notes: []string{}, Recs: []SchedRec{}}
	sc := s.loadSchedule(b.Name)
	if sc == nil {
		st.Step, st.Locked, st.Tip = "start", true, analysis.RouteTip
		st.Why = "Optimise is locked until the build has been measured: start the test round."
		for _, d := range analysis.LoadRoutes(s.routesPath()) {
			st.Routes = append(st.Routes, SchedRoute{Def: d})
		}
		return st
	}
	st.Started = sc.Started.Format("2 Jan 2006 15:04")
	for i := range sc.Rounds {
		if !sc.Rounds[i].Closed.IsZero() {
			st.Rounds++
			st.Last = &sc.Rounds[i]
		}
	}
	var cur *SchedRound
	if n := len(sc.Rounds); n > 0 && sc.Rounds[n-1].Closed.IsZero() {
		cur = &sc.Rounds[n-1]
		st.Round = cur.N
	} else if n > 0 && sc.Rounds[n-1].Trial != nil && sc.Rounds[n-1].Trial.Outcome == "" {
		return decideStatus(st, sc, &sc.Rounds[n-1]) // the trial's retest is in: keep or drop comes first
	}

	// ---- before the first round: the baseline
	if cur == nil {
		// the recordings have to be of the build as it is now (its mod files are only walked once there is a recording to check)
		sessions := s.testSessions(b.Path, sc.Started)
		var mods []analysis.ModFile
		if len(sessions) > 0 {
			mods = buildMods(b.Path)
		}
		rows, ref, used := pickRoutes(sc.Routes, sessions, pickOpts{mods: mods})
		st.Routes, st.Done, st.Tip = dress(rows), countDone(rows), analysis.RouteTip
		st.Recs = recTexts(sessions, sc.Routes, used)
		last := newest(rows)
		switch {
		case st.Done < len(rows):
			st.Step, st.Locked = "routes", true
			st.Why = fmt.Sprintf("Optimise is locked until every test route is recorded: %d of %d done.", st.Done, len(rows))
		case sc.Conflicts.Before(last):
			st.Step, st.Locked = "conflicts", true
			st.Why = "Every route is recorded. Next: run the conflict check."
		case sc.Analysed.Before(sc.Conflicts):
			st.Step, st.Locked = "analyse", true
			st.Why = "The conflict check is done. Next: analyse the build."
		default:
			// everything is in: the fixes open, and this is what they will be measured against
			r := SchedRound{N: 1, Opened: time.Now(), Base: runsOf(rows), Mods: buildMods(b.Path)}
			if ref != nil {
				r.Settings, r.Scripts = ref.Settings, ref.Scripts
			}
			sc.Rounds = append(sc.Rounds, r)
			sc.Pinned = pinRoutes(sc.Routes, r.Base) // from here on the walk and the drive are asked for where they were first recorded
			s.saveSchedule(sc)
			cur = &sc.Rounds[len(sc.Rounds)-1]
			st.Round, st.Tip, st.Recs = 1, "", []SchedRec{}
		}
		if cur == nil {
			return st
		}
	}

	// ---- a round is open
	defs := sc.Pinned
	if len(defs) != len(sc.Routes) {
		defs = sc.Routes
	}
	st.Changes = append(st.Changes, cur.Changes...)
	st.Trial = cur.Trial
	now := buildMods(b.Path)
	if d := modsDiff(cur.Mods, now); len(d) > 0 {
		st.ByHand = d
	}
	base := func(rows []SchedRoute) []SchedRoute {
		for i := range rows {
			if r, ok := cur.Base[rows[i].Def.ID]; ok {
				r := r
				rows[i].Base = &r
			}
		}
		return rows
	}
	if len(cur.Changes) == 0 && len(st.ByHand) == 0 {
		st.Step = "fix"
		st.Why = "The build is measured: fixes are open. After a change, the same routes are recorded again."
		st.Routes = nil // (the rows of the baseline step, when the round opened just now)
		for _, d := range defs {
			st.Routes = append(st.Routes, SchedRoute{Def: d, Done: true})
		}
		st.Routes, st.Done = dress(base(st.Routes)), len(defs)
		return st
	}
	// changed: the same test again. Only recordings after the last change count, with the same settings
	// and with the mod files as they are now.
	since := cur.Opened
	if cur.Changed.After(since) {
		since = cur.Changed
	}
	sessions := s.testSessions(b.Path, since)
	scripts := cur.Scripts
	rows, _, used := pickRoutes(defs, sessions, pickOpts{settings: orEmpty(cur.Settings), scripts: &scripts, mods: now, trial: cur.Trial})
	st.Routes, st.Done = dress(base(rows)), countDone(rows)
	st.Recs = recTexts(sessions, defs, used)
	st.Step, st.Frozen = "retest", len(sessions) > 0
	if st.Done < len(rows) {
		st.Locked = st.Frozen
		if cur.Trial != nil {
			// an option is tried on its own: nothing else can be changed until it is kept or dropped
			st.Locked = true
			st.Why = fmt.Sprintf("On trial: %s. Record the same routes again: Frame Lab sets the option in these recordings only. %d of %d done.", cur.Trial.Name, st.Done, len(rows))
		} else if st.Frozen {
			st.Why = fmt.Sprintf("You have played since the last change, so nothing more can be changed until the same routes are recorded again: %d of %d done.", st.Done, len(rows))
		} else {
			st.Why = "The build has changed. Record the same routes again to see what it did. Until your first recording you can still add to this round."
		}
		return st
	}
	// the retest is complete: the verdict, and the next round starts from these numbers
	cur.After, cur.Closed, cur.ByHand = runsOf(rows), time.Now(), st.ByHand
	better, worse, same, mixed := 0, 0, 0, 0
	var worseNames []string
	for _, d := range defs {
		c := analysis.CompareRoute(cur.Base[d.ID].Stat, cur.After[d.ID].Stat)
		cur.Result = append(cur.Result, c)
		switch c.Verdict {
		case "better":
			better++
		case "worse":
			worse++
			worseNames = append(worseNames, d.Name)
		case "mixed":
			mixed++
		default:
			same++
		}
	}
	cur.Kind, cur.Verdict = roundVerdict(len(defs), better, worse, same, mixed, worseNames)
	if cur.Trial != nil {
		// a trial doesn't move on by itself: the player keeps the option or drops it first
		s.saveSchedule(sc)
		st.Rounds++
		return decideStatus(st, sc, cur)
	}
	next := SchedRound{N: cur.N + 1, Opened: time.Now(), Base: cur.After, Settings: cur.Settings, Scripts: cur.Scripts, Mods: now}
	sc.Rounds = append(sc.Rounds, next)
	s.saveSchedule(sc)
	closed := sc.Rounds[len(sc.Rounds)-2]
	st.Last, st.Rounds, st.Round = &closed, st.Rounds+1, next.N
	st.Step, st.Locked, st.Frozen = "fix", false, false
	st.Changes, st.ByHand = []SchedChange{}, []string{}
	st.Why = "The retest is in: see the result. Fixes are open again."
	st.Routes, st.Recs = nil, []SchedRec{}
	for _, d := range defs {
		r := next.Base[d.ID]
		st.Routes = append(st.Routes, SchedRoute{Def: d, Done: true, Base: &r})
	}
	st.Routes, st.Done = dress(st.Routes), len(defs)
	return st
}

// decideStatus: the retest of a trial is in. The routes before and after are shown, and the player has
// to keep the option or drop it before the schedule moves on.
func decideStatus(st *SchedStatus, sc *Schedule, last *SchedRound) *SchedStatus {
	defs := sc.Pinned
	if len(defs) != len(sc.Routes) {
		defs = sc.Routes
	}
	st.Step, st.Locked, st.Frozen, st.Round, st.Trial, st.Last = "decide", true, false, last.N, last.Trial, last
	st.Changes = append([]SchedChange{}, last.Changes...)
	st.ByHand = append([]string{}, last.ByHand...)
	st.Why = fmt.Sprintf("The trial of \"%s\" is in. Keep the option or drop it: nothing else can be changed until then.", last.Trial.Name)
	st.Routes, st.Recs = nil, []SchedRec{}
	for _, d := range defs {
		row := SchedRoute{Def: d, Done: true}
		if r, ok := last.After[d.ID]; ok {
			r := r
			row.Run = &r
		}
		if r, ok := last.Base[d.ID]; ok {
			r := r
			row.Base = &r
		}
		st.Routes = append(st.Routes, row)
	}
	st.Routes, st.Done = dress(st.Routes), len(defs)
	return st
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// roundVerdict: a round in one sentence.
func roundVerdict(n, better, worse, same, mixed int, worseNames []string) (kind, text string) {
	of := func(k int) string {
		if k == n {
			return "every route"
		}
		return fmt.Sprintf("%d of %d routes", k, n)
	}
	switch {
	case worse > 0 && better == 0:
		kind, text = "worse", "Worse on "+of(worse)+"."
	case better > 0 && worse == 0 && mixed == 0:
		kind, text = "better", "Better on "+of(better)+"."
	case better == 0 && worse == 0 && mixed == 0:
		return "same", "No difference that counts on any route: the change made the game neither smoother nor rougher."
	default:
		kind = "mixed"
		var p []string
		if better > 0 {
			p = append(p, "better on "+of(better))
		}
		if worse > 0 {
			p = append(p, "worse on "+of(worse))
		}
		if mixed > 0 {
			p = append(p, "partly better and partly worse on "+of(mixed))
		}
		text = strings.ToUpper(p[0][:1]) + p[0][1:]
		if len(p) > 1 {
			text += ", " + strings.Join(p[1:], ", ")
		}
		text += "."
	}
	if same > 0 {
		text += " The same on " + of(same) + "."
	}
	if len(worseNames) > 0 {
		text += " Worse: " + strings.Join(worseNames, ", ") + "."
	}
	return kind, text
}

// ---------------------------------------------------------------- what the rest of Frame Lab calls

// scheduleStart begins (or begins again) the test round of a build. Fixes already made stay in place:
// the new baseline is the build as it is now.
func (s *Server) scheduleStart(i int) error {
	b, err := s.buildAt(i)
	if err != nil {
		return err
	}
	schedMu.Lock()
	defer schedMu.Unlock()
	buildModsForget(b.Path)
	sc := &Schedule{Build: b.Name, Started: time.Now(), Routes: analysis.LoadRoutes(s.routesPath())}
	if old := s.loadSchedule(b.Name); old != nil {
		sc.Kept = old.Kept // kept game options stay in the build, so they stay on the list
	}
	return s.saveSchedule(sc)
}

// schedRoutesToDo: the routes a new recording of this build should cover (pinned to their districts
// once those are known); with any, the recording is a test recording. nil when the schedule isn't
// waiting for routes.
func (s *Server) schedRoutesToDo(i int) []analysis.RouteDef {
	routes, _ := s.schedSession(i)
	return routes
}

// schedSession: what a new recording of this build is for: the routes it should cover (see
// schedRoutesToDo) and, in a test recording, the game options the markers mod reads and the one on trial.
func (s *Server) schedSession(i int) ([]analysis.RouteDef, SessionOpts) {
	st, err := s.scheduleStatus(i)
	if err != nil || (st.Step != "routes" && st.Step != "retest") {
		return nil, SessionOpts{}
	}
	var out []analysis.RouteDef
	for _, r := range st.Routes {
		if !r.Done {
			out = append(out, r.Def)
		}
	}
	var o SessionOpts
	if len(out) > 0 {
		seen := map[string]bool{}
		for _, d := range LoadOptions(s.optionsPath()) {
			if k := analysis.OptionKey(d.Cat, d.Item); !seen[k] {
				seen[k] = true
				o.Probe = append(o.Probe, [2]string{d.Cat, d.Item})
			}
		}
		if st.Trial != nil && st.Step == "retest" {
			o.Set = [][3]string{{st.Trial.Cat, st.Trial.Item, st.Trial.Value}}
		}
	}
	return out, o
}

// schedRoutes: the route set recordings of this game folder are read with (the running schedule's, so a
// retest is read the same way as its baseline even if routes.json changed in between).
func (s *Server) schedRoutes(game string) []analysis.RouteDef {
	for _, b := range s.Settings.Get().Builds {
		if strings.EqualFold(filepath.Clean(b.Path), filepath.Clean(game)) {
			schedMu.Lock()
			sc := s.loadSchedule(b.Name)
			schedMu.Unlock()
			if sc != nil && len(sc.Pinned) == len(sc.Routes) {
				return sc.Pinned
			}
			if sc != nil {
				return sc.Routes
			}
		}
	}
	return analysis.LoadRoutes(s.routesPath())
}

// schedMark notes that the conflict check ("conflicts") or the build analysis ("analysed") of a build finished.
func (s *Server) schedMark(build, what string) {
	schedMu.Lock()
	defer schedMu.Unlock()
	sc := s.loadSchedule(build)
	if sc == nil {
		return
	}
	switch what {
	case "conflicts":
		sc.Conflicts = time.Now()
	case "analysed":
		sc.Analysed = time.Now()
	}
	s.saveSchedule(sc)
}

// schedChange notes a fix (or its undo) in the open round. From here the round needs its retest.
func (s *Server) schedChange(b BuildCfg, text, fix string, undo bool) {
	schedMu.Lock()
	defer schedMu.Unlock()
	buildModsForget(b.Path)
	sc := s.loadSchedule(b.Name)
	if sc == nil || len(sc.Rounds) == 0 || !sc.Rounds[len(sc.Rounds)-1].Closed.IsZero() {
		return
	}
	cur := &sc.Rounds[len(sc.Rounds)-1]
	now := time.Now()
	if undo {
		// a fix made and undone inside the same round, with nothing recorded in between, never happened
		for k := len(cur.Changes) - 1; k >= 0; k-- {
			if c := cur.Changes[k]; c.Fix == fix && !c.Undo && len(s.testSessions(b.Path, c.When)) == 0 {
				cur.Changes = append(cur.Changes[:k], cur.Changes[k+1:]...)
				cur.Changed = time.Time{}
				for _, r := range cur.Changes {
					if r.When.After(cur.Changed) {
						cur.Changed = r.When
					}
				}
				s.saveSchedule(sc)
				return
			}
		}
	}
	cur.Changes = append(cur.Changes, SchedChange{When: now, Text: text, Fix: fix, Undo: undo})
	cur.Changed = now
	s.saveSchedule(sc)
}

// schedLock: nil when a fix may be made in this build now, else why not.
func (s *Server) schedLock(i int) error {
	st, err := s.scheduleStatus(i)
	if err != nil {
		return err
	}
	if st.Locked {
		return errors.New(st.Why)
	}
	return nil
}

// schedOpen: the build analysis may run (every route is recorded and the conflict check is done)
func (s *Server) schedOpen(i int) error {
	st, err := s.scheduleStatus(i)
	if err != nil {
		return err
	}
	switch st.Step {
	case "start", "routes", "conflicts":
		return errors.New(st.Why)
	}
	return nil
}
