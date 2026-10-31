package analysis

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"framelab/internal/hrec"
	"framelab/internal/modmap"
)

// ExportTrace writes what HitchRecorder recorded from t-span/2 to t+span/2 (seconds on the
// report's time axis) as a Chrome trace (JSON) that ui.perfetto.dev and chrome://tracing open:
// one row per game thread with every recorded piece of work, the frames on the main thread, and
// the REDscript / CET totals per mod in 50 ms steps on two extra rows. Returns the file's path.
func ExportTrace(dir string, t, span float64, names modmap.NamesFile) (string, error) {
	var res struct {
		Timeline struct {
			T0Qpc int64 `json:"t0_qpc"`
		} `json:"timeline"`
	}
	b, err := os.ReadFile(filepath.Join(dir, FileAnalysis))
	if err != nil {
		return "", err
	}
	json.Unmarshal(b, &res)
	if res.Timeline.T0Qpc == 0 {
		return "", errors.New("re-analyse this session first (it was analysed by an older Frame Lab)")
	}
	f, err := hrec.Read(filepath.Join(dir, FileHitch))
	if err != nil {
		return "", errors.New("no engine recording in this session")
	}
	freq := float64(f.Header.QpcFreq)
	if span <= 0 {
		span = 6
	}
	q0 := res.Timeline.T0Qpc + int64((t-span/2)*freq)
	q1 := res.Timeline.T0Qpc + int64((t+span/2)*freq)
	us := func(q int64) float64 { return float64(q-q0) / freq * 1e6 }
	red, cet := readOwners(dir)

	type ev struct {
		Name string         `json:"name"`
		Cat  string         `json:"cat,omitempty"`
		Ph   string         `json:"ph"`
		Ts   float64        `json:"ts"`
		Dur  float64        `json:"dur,omitempty"`
		Pid  int            `json:"pid"`
		Tid  uint32         `json:"tid"`
		S    string         `json:"s,omitempty"`
		Args map[string]any `json:"args,omitempty"`
	}
	const tidRed, tidCet = 4000000001, 4000000002
	var evs []ev
	var mainTid uint32
	want := map[uint64]bool{}
	var picked []*hrec.Rec
	for i := range f.Recs {
		r := &f.Recs[i]
		if r.Kind == hrec.KVram {
			continue
		}
		q := f.Header.Qpc(r.TUs)
		end := q + int64(float64(r.DurUs)*freq/1e6)
		if end < q0 || q > q1 {
			continue
		}
		if r.Main() && mainTid == 0 && r.Tid != 0 {
			mainTid = r.Tid
		}
		if hrec.IsPath(r.Kind) && r.Hash != 0 {
			want[r.Hash] = true
		}
		picked = append(picked, r)
	}
	if len(picked) == 0 {
		return "", errors.New("nothing was recorded at that time")
	}
	paths := map[uint64]string{}
	if names.Exists() && len(want) > 0 {
		paths, _ = names.Resolve(want)
	}
	for _, r := range picked {
		q := f.Header.Qpc(r.TUs)
		e := ev{Ph: "X", Ts: us(q), Dur: float64(r.DurUs), Pid: 1, Tid: r.Tid}
		k := hrec.KindName[r.Kind]
		switch {
		case r.Kind == hrec.KFrame:
			e.Name, e.Cat = fmt.Sprintf("frame %d", r.Frame), "frame"
		case r.Kind == hrec.KMark:
			e.Name, e.Ph, e.Dur, e.S, e.Cat = "marker", "i", 0, "g", "marker"
		case r.Kind == hrec.KScript || r.Kind == hrec.KCet:
			own := red[r.Hash]
			e.Tid = tidRed
			if r.Kind == hrec.KCet {
				own, e.Tid = cet[r.Hash], tidCet
			}
			if own == "" {
				own = fmt.Sprintf("owner %d", r.Hash)
			}
			e.Name, e.Cat = own, "script (total in this 50 ms)"
		case r.Kind == hrec.KScriptMain || r.Kind == hrec.KCetMain:
			continue // the all-threads totals above already cover these
		default:
			e.Name, e.Cat = k, k
			if p := paths[r.Hash]; p != "" {
				e.Name = k + ": " + filepath.Base(p)
				e.Args = map[string]any{"file": p}
			} else if r.Hash != 0 && hrec.IsPath(r.Kind) {
				e.Args = map[string]any{"hash": fmt.Sprintf("%016x", r.Hash)}
			}
			if r.Failed() {
				e.Name += " (failed)"
			}
		}
		evs = append(evs, e)
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].Ts < evs[j].Ts })
	meta := func(tid uint32, name string, order int) {
		evs = append(evs, ev{Name: "thread_name", Ph: "M", Pid: 1, Tid: tid, Args: map[string]any{"name": name}},
			ev{Name: "thread_sort_index", Ph: "M", Pid: 1, Tid: tid, Args: map[string]any{"sort_index": order}})
	}
	evs = append(evs, ev{Name: "process_name", Ph: "M", Pid: 1, Args: map[string]any{"name": fmt.Sprintf("Cyberpunk 2077, %.1f s around %s", span, fmtT(t))}})
	if mainTid != 0 {
		meta(mainTid, "main thread", 0)
	}
	meta(tidRed, "REDscript per mod (50 ms totals)", 1)
	meta(tidCet, "CET Lua per mod (50 ms totals)", 2)

	out := filepath.Join(dir, fmt.Sprintf("timeline_%s.json", fmtTFile(t)))
	fh, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	w := bufio.NewWriterSize(fh, 1<<20)
	enc := json.NewEncoder(w)
	w.WriteString(`{"displayTimeUnit":"ms","traceEvents":[`)
	for i, e := range evs {
		if i > 0 {
			w.WriteByte(',')
		}
		enc.Encode(e)
	}
	w.WriteString("]}")
	return out, w.Flush()
}

func fmtT(t float64) string {
	m := int(t) / 60
	return fmt.Sprintf("%d:%04.1f", m, t-float64(m*60))
}

func fmtTFile(t float64) string {
	m := int(t) / 60
	return fmt.Sprintf("%dm%02.0fs", m, t-float64(m*60))
}
