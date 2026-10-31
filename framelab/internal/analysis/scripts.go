package analysis

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"framelab/internal/hrec"
)

// Bucket is script time of one mod in one 50 ms window (Unix ms).
type Bucket struct {
	StartUnix, EndUnix float64
	Owner              string
	ExclusiveMs        float64
}

// OwnerCost is one mod's total.
type OwnerCost struct {
	Owner    string  `json:"owner"`
	MsPerSec float64 `json:"ms_per_sec"`
}

// ScriptLayer is one script layer (REDscript or CET Lua) of a session.
type ScriptLayer struct {
	Domain    string // "redscript" or "cet"
	Dir       string
	StartUnix float64
	StopUnix  float64
	DurationS float64
	MsPerSec  float64
	Owners    []OwnerCost
	Buckets   []Bucket
	Segments  [][2]float64 // [unix start, unix end] spans actually timed (the F11 parts)
}

// FileHitchOwners is HitchRecorder's owner list for its script records ("<index>\t<owner>").
const FileHitchOwners = "hitch.owners.txt"

func readOwners(dir string) (red, cet map[uint64]string) {
	red = map[uint64]string{0: "game scripts", 1: "scripts without a source file"}
	cet = map[uint64]string{0: "CET itself"}
	b, err := os.ReadFile(filepath.Join(dir, FileHitchOwners))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		i := strings.IndexByte(line, '\t')
		if i <= 0 || line[i+1:] == "" {
			continue
		}
		m, key := red, line[:i]
		if key[0] == 'c' { // CET owners: "c<n>"
			m, key = cet, key[1:]
		}
		if n, err := strconv.ParseUint(key, 10, 32); err == nil {
			m[n] = line[i+1:]
		}
	}
	return
}

// ourScripts and ourCet turn HitchRecorder's own REDscript and CET Lua records into the same shape
// the report already uses for a script layer: per-mod totals (all threads) and 50 ms main-thread
// buckets for the spike breakdown. nil when the recording has none.
func (d *data) ourScripts(dir string) *ScriptLayer {
	red, _ := readOwners(dir)
	return d.ourLayer(red, hrec.KScript, hrec.KScriptMain, "redscript")
}

func (d *data) ourCet(dir string) *ScriptLayer {
	_, cet := readOwners(dir)
	return d.ourLayer(cet, hrec.KCet, hrec.KCetMain, "cet")
}

func (d *data) ourLayer(owners map[uint64]string, kAll, kMain uint8, domain string) *ScriptLayer {
	if d.hr == nil {
		return nil
	}
	name := func(h uint64) string {
		if n, ok := owners[h]; ok {
			return n
		}
		return "owner " + strconv.FormatUint(h, 10)
	}
	total := map[string]float64{}
	var buckets []Bucket
	var first, last float64 = -1, -1
	for i := range d.hr.Recs {
		r := &d.hr.Recs[i]
		if r.Kind != kAll && r.Kind != kMain {
			continue
		}
		u := d.qpcToUnix(d.hr.Header.Qpc(r.TUs))
		ms := float64(r.DurUs) / 1000
		if r.Kind == kAll {
			total[name(r.Hash)] += ms
			if first < 0 || u < first {
				first = u
			}
			if u+50 > last {
				last = u + 50
			}
		} else {
			buckets = append(buckets, Bucket{StartUnix: u, EndUnix: u + 50, Owner: name(r.Hash), ExclusiveMs: ms})
		}
	}
	if len(total) == 0 {
		return nil
	}
	// time the scripts were timed: the F11 parts (session) or capture windows, else first..last bucket
	var segs [][2]float64
	var open int64 = -1
	for i := range d.hr.Recs {
		r := &d.hr.Recs[i]
		if r.Kind != hrec.KMark {
			continue
		}
		q := d.hr.Header.Qpc(r.TUs)
		switch r.Extra {
		case hrec.MarkFocusStart, hrec.MarkCaptureStart:
			open = q
		case hrec.MarkFocusStop, hrec.MarkCaptureStop:
			if open >= 0 {
				segs = append(segs, [2]float64{d.qpcToUnix(open), d.qpcToUnix(q)})
				open = -1
			}
		}
	}
	if open >= 0 && last > 0 {
		segs = append(segs, [2]float64{d.qpcToUnix(open), last})
	}
	if len(segs) == 0 {
		segs = [][2]float64{{first, last}}
	}
	secs := 0.0
	for _, s := range segs {
		secs += (s[1] - s[0]) / 1000
	}
	if secs <= 0 {
		secs = 1
	}
	res := &ScriptLayer{Domain: domain, Dir: "HitchRecorder", StartUnix: segs[0][0], StopUnix: segs[len(segs)-1][1],
		DurationS: secs, Buckets: buckets, Segments: segs}
	sum := 0.0
	for o, ms := range total {
		res.Owners = append(res.Owners, OwnerCost{Owner: o, MsPerSec: ms / secs})
		sum += ms
	}
	sort.Slice(res.Owners, func(i, j int) bool { return res.Owners[i].MsPerSec > res.Owners[j].MsPerSec })
	sort.Slice(res.Buckets, func(i, j int) bool { return res.Buckets[i].StartUnix < res.Buckets[j].StartUnix })
	res.MsPerSec = sum / secs
	return res
}
