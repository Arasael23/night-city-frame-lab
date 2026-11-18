package analysis

// Optimise stage 2 (2.5E): what each REDscript function of a mod cost, from HitchRecorder 1.3's
// <recording>.fn.txt (copied into the session as hitch.fn.txt). The file has 5-second blocks:
//
//	N  id  mod  function       (once, before the function's first F line; function = Class::Func or Func)
//	T  block end (us since the game started)  block length (us)
//	F  id  own us  own us on the main thread  us with the functions it calls  calls
//
// Only mods' functions are in it (not the game's own scripts). A function is only written once it has
// 50 us of own time (or 500 us with its callees) to report: smaller amounts wait for a later block.

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FnCost: one mod function over the whole recording and on each test route.
type FnCost struct {
	Mod     string             `json:"mod"`
	Fn      string             `json:"fn"`               // Class::Func, or Func for a global function
	MainMsS float64            `json:"main_ms_s"`        // own time on the game's main thread, ms per second
	OwnMsS  float64            `json:"own_ms_s"`         // own time on every thread
	InclMsS float64            `json:"incl_ms_s"`        // with the mod functions it calls
	CallsS  float64            `json:"calls_s"`          // calls per second
	Routes  map[string]float64 `json:"routes,omitempty"` // route id -> own main-thread ms per second while on that route
	File    string             `json:"file,omitempty"`   // where it is (filled in by Frame Lab, which knows the build)
	Line    int                `json:"line,omitempty"`
	Wraps   bool               `json:"wraps,omitempty"` // a @wrapMethod / @replaceMethod of a game function
}

// FnReport: the costliest mod functions of a recording.
type FnReport struct {
	Seconds float64  `json:"seconds"` // length of the blocks the file covers
	Fns     []FnCost `json:"fns"`
}

const fnTop = 40 // functions kept per recording

type fnBlock struct {
	a, b float64 // Unix ms
	rows []fnRow
}
type fnRow struct {
	id                     int
	own, main, incl, calls float64
}

// readFns reads hitch.fn.txt. toUnix turns "us since the game started" into Unix ms.
func readFns(dir string, toUnix func(us uint64) float64) (names map[int][2]string, blocks []fnBlock) {
	f, err := os.Open(filepath.Join(dir, FileHitchFn))
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	names = map[int][2]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var cur *fnBlock
	for sc.Scan() {
		p := strings.Split(strings.TrimRight(sc.Text(), "\r"), "\t")
		switch {
		case len(p) == 4 && p[0] == "N":
			if id, err := strconv.Atoi(p[1]); err == nil {
				names[id] = [2]string{p[2], p[3]}
			}
		case len(p) == 3 && p[0] == "T":
			end, e1 := strconv.ParseUint(p[1], 10, 64)
			n, e2 := strconv.ParseUint(p[2], 10, 64)
			if e1 != nil || e2 != nil || n > end {
				cur = nil
				continue
			}
			blocks = append(blocks, fnBlock{a: toUnix(end - n), b: toUnix(end)})
			cur = &blocks[len(blocks)-1]
		case len(p) == 6 && p[0] == "F" && cur != nil:
			var v [5]float64
			ok := true
			for i := 1; i < 6; i++ {
				x, err := strconv.ParseFloat(p[i], 64)
				if err != nil {
					ok = false
					break
				}
				v[i-1] = x
			}
			if ok {
				cur.rows = append(cur.rows, fnRow{id: int(v[0]), own: v[1], main: v[2], incl: v[3], calls: v[4]})
			}
		}
	}
	return names, blocks
}

// fnCosts adds up hitch.fn.txt; spans gives each route's counted time (Unix ms) so every function also
// gets its cost on each route (a block that is partly on a route counts by the part that is).
func fnCosts(dir string, toUnix func(us uint64) float64, spans map[string][][2]float64) *FnReport {
	names, blocks := readFns(dir, toUnix)
	if len(blocks) == 0 {
		return nil
	}
	type acc struct {
		own, main, incl, calls float64
		route                  map[string]float64
	}
	sum := map[int]*acc{}
	total := 0.0
	routeS := map[string]float64{}
	for _, bl := range blocks {
		lenS := (bl.b - bl.a) / 1000
		if lenS <= 0 {
			continue
		}
		total += lenS
		share := map[string]float64{} // route -> part of this block on it (0..1)
		for id, ss := range spans {
			ov := 0.0
			for _, s := range ss {
				if o := math.Min(s[1], bl.b) - math.Max(s[0], bl.a); o > 0 {
					ov += o
				}
			}
			if ov > 0 {
				share[id] = ov / (bl.b - bl.a)
				routeS[id] += ov / 1000
			}
		}
		for _, r := range bl.rows {
			a := sum[r.id]
			if a == nil {
				a = &acc{route: map[string]float64{}}
				sum[r.id] = a
			}
			a.own += r.own
			a.main += r.main
			a.incl += r.incl
			a.calls += r.calls
			for id, sh := range share {
				a.route[id] += r.main * sh
			}
		}
	}
	if total <= 0 {
		return nil
	}
	rep := &FnReport{Seconds: round1(total)}
	for id, a := range sum {
		nm, ok := names[id]
		if !ok || !scriptOwnerIsMod(nm[0]) {
			continue
		}
		c := FnCost{Mod: nm[0], Fn: nm[1],
			MainMsS: round3(a.main / 1000 / total), OwnMsS: round3(a.own / 1000 / total),
			InclMsS: round3(a.incl / 1000 / total), CallsS: round1(a.calls / total)}
		for rid, us := range a.route {
			if s := routeS[rid]; s >= 5 && us > 0 {
				if c.Routes == nil {
					c.Routes = map[string]float64{}
				}
				c.Routes[rid] = round3(us / 1000 / s)
			}
		}
		rep.Fns = append(rep.Fns, c)
	}
	sort.Slice(rep.Fns, func(i, j int) bool {
		a, b := rep.Fns[i], rep.Fns[j]
		if a.MainMsS != b.MainMsS {
			return a.MainMsS > b.MainMsS
		}
		if a.InclMsS != b.InclMsS {
			return a.InclMsS > b.InclMsS
		}
		return a.Mod+a.Fn < b.Mod+b.Fn
	})
	if len(rep.Fns) > fnTop {
		rep.Fns = rep.Fns[:fnTop]
	}
	return rep
}
