package app

// Merge (2.5E, Conflicts): two mods that replace the same text files of the game (.json: on-screen
// texts, subtitles and other lists the game reads) can both be kept. Frame Lab unpacks both archives
// with the WolvenKit console, puts the entries of both into one file (where both change the same
// entry, the mod that already won keeps its version), packs those files into a new archive that loads
// before both, and installs it through the fix ledger: nothing of the two mods is changed, and Undo
// takes the merged archive out again.
//
// Only lists whose entries can be told apart (a key, an id, a string id) are merged. Anything else
// (scenes, quest files, models, textures) can't be merged safely by a program: those clashes can be
// opened side by side in the Workbench.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"framelab/internal/conflicts"
	"framelab/internal/gamefs"
)

// MergeJob: one merge of two archives, as the Conflicts tab shows it.
type MergeJob struct {
	ID          string      `json:"id"`
	Build       string      `json:"build"`
	Winner      string      `json:"winner"` // archive rel paths, as in the conflict check
	Loser       string      `json:"loser"`
	WinnerLabel string      `json:"winner_label"`
	LoserLabel  string      `json:"loser_label"`
	State       string      `json:"state"` // unpacking | merging | packing | done | error: ...
	Started     time.Time   `json:"started"`
	Files       []MergeFile `json:"files,omitempty"`
	Archive     string      `json:"archive,omitempty"` // the merged archive (rel path in the build)
	Fix         string      `json:"fix,omitempty"`     // its fix ledger id
	Log         string      `json:"log,omitempty"`
}

// MergeFile: one file of the merge.
type MergeFile struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`   // entries only the losing mod had: they are in now
	Same    int    `json:"same"`    // entries both have, the same
	Changed int    `json:"changed"` // entries both have, different: the winner's are kept
}

var (
	mergeMu   sync.Mutex
	mergeJobs = map[string]*MergeJob{} // build + "|" + winner + "|" + loser
)

func mergeKey(build, winner, loser string) string { return build + "|" + winner + "|" + loser }

// mergeable: every clashing file of the pair is a JSON resource ("19 json")
func mergeable(kinds string) bool {
	n := 0
	for _, part := range strings.Split(kinds, ",") {
		f := strings.Fields(strings.TrimSpace(part))
		if len(f) != 2 {
			return false
		}
		if f[1] != "json" {
			return false
		}
		n++
	}
	return n > 0
}

// sceneLike: clashes a person can merge by hand in the Workbench (scenes, quest files)
func sceneLike(kinds string) bool {
	for _, k := range []string{"scene", "questphase", "quest", "journal"} {
		if strings.Contains(kinds, k) {
			return true
		}
	}
	return false
}

func (s *Server) mergePair(b BuildCfg, winner, loser string) (*conflicts.Report, *conflicts.Pair, error) {
	r, err := s.savedConflicts(b)
	if err != nil {
		return nil, nil, err
	}
	for k := range r.Pairs {
		p := &r.Pairs[k]
		if p.Winner < len(r.Archives) && p.Loser < len(r.Archives) && r.Archives[p.Winner].Rel == winner && r.Archives[p.Loser].Rel == loser {
			return r, p, nil
		}
	}
	return nil, nil, errors.New("that clash isn't in the last conflict check: run it again")
}

// mergeStatus: the merge of this pair, if one was started since Frame Lab started
func (s *Server) mergeStatus(i int, winner, loser string) *MergeJob {
	b, err := s.buildAt(i)
	if err != nil {
		return nil
	}
	mergeMu.Lock()
	defer mergeMu.Unlock()
	if j := mergeJobs[mergeKey(b.Name, winner, loser)]; j != nil {
		c := *j
		c.Files = append([]MergeFile(nil), j.Files...)
		return &c
	}
	return nil
}

// mergeStart checks the pair and starts the merge in the background.
func (s *Server) mergeStart(i int, winner, loser string) (*MergeJob, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first: the merged archive is read when the game starts")
	}
	r, p, err := s.mergePair(b, winner, loser)
	if err != nil {
		return nil, err
	}
	if !mergeable(p.Kinds) {
		return nil, fmt.Errorf("only text files (.json) can be merged by Frame Lab; these are %s. Open both in the Workbench to merge them by hand", p.Kinds)
	}
	wk := wolvenKit(s.Paths, s.Settings.Get())
	if runWKHook != nil {
		wk.Found, wk.DotNet = true, "test"
	}
	if !wk.Found {
		return nil, errors.New("the WolvenKit console isn't there (components\\WolvenKit): it is what unpacks and packs the archives")
	}
	if wk.DotNet == "" {
		return nil, errors.New("the WolvenKit console needs Microsoft's .NET 10 runtime, which isn't on this PC. Get '.NET Runtime 10' (x64) from Microsoft's .NET download page, then try again")
	}
	k := mergeKey(b.Name, winner, loser)
	mergeMu.Lock()
	if j := mergeJobs[k]; j != nil && (j.State == "unpacking" || j.State == "merging" || j.State == "packing") {
		mergeMu.Unlock()
		return nil, errors.New("this merge is running already")
	}
	j := &MergeJob{ID: time.Now().Format("20060102-150405"), Build: b.Name, Winner: winner, Loser: loser,
		WinnerLabel: r.Archives[p.Winner].Label, LoserLabel: r.Archives[p.Loser].Label, State: "unpacking", Started: time.Now()}
	mergeJobs[k] = j
	mergeMu.Unlock()
	go s.mergeRun(i, b, k)
	c := *j
	return &c, nil
}

func (s *Server) mergeSet(k string, f func(j *MergeJob)) {
	mergeMu.Lock()
	defer mergeMu.Unlock()
	if j := mergeJobs[k]; j != nil {
		f(j)
	}
}

// mergeRun: unpack both, merge the files both have, pack, install
func (s *Server) mergeRun(i int, b BuildCfg, k string) {
	mergeMu.Lock()
	j := *mergeJobs[k]
	mergeMu.Unlock()
	dir := filepath.Join(s.Paths.Data, "merge", j.ID)
	os.RemoveAll(dir)
	log := ""
	fail := func(err error) {
		s.mergeSet(k, func(x *MergeJob) { x.State = "error: " + err.Error(); x.Log = tail(log, 4000) })
		s.Ctl.diag("merge " + j.WinnerLabel + " + " + j.LoserLabel + " failed: " + err.Error())
	}
	aRaw, bRaw := filepath.Join(dir, "a", "raw"), filepath.Join(dir, "b", "raw")
	for _, x := range [][2]string{{j.Winner, aRaw}, {j.Loser, bRaw}} {
		l, err := s.runWK("unbundle", gamefs.Real(filepath.Join(b.Path, filepath.FromSlash(x[0]))), x[1])
		log += l + "\n"
		if err != nil {
			fail(err)
			return
		}
	}
	// the files both archives have, with different contents
	var common []string
	filepath.WalkDir(aRaw, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(aRaw, p)
		other := filepath.Join(bRaw, rel)
		if !fileExists(other) {
			return nil
		}
		x, _ := os.ReadFile(p)
		y, _ := os.ReadFile(other)
		if !bytes.Equal(x, y) {
			common = append(common, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(common)
	if len(common) == 0 {
		fail(errors.New("the two archives have no different versions of the same file: there is nothing to merge"))
		return
	}
	s.mergeSet(k, func(x *MergeJob) { x.State = "merging" })
	// readable JSON of just those files
	sel := func(raw, side string) (string, error) {
		in := filepath.Join(dir, side, "sel")
		for _, rel := range common {
			if err := copyFileMk(filepath.Join(raw, filepath.FromSlash(rel)), filepath.Join(in, filepath.FromSlash(rel))); err != nil {
				return "", err
			}
		}
		out := filepath.Join(dir, side, "json")
		l, err := s.runWK("serialize", in, out)
		log += l + "\n"
		return out, err
	}
	aJSON, err := sel(aRaw, "a")
	if err != nil {
		fail(err)
		return
	}
	bJSON, err := sel(bRaw, "b")
	if err != nil {
		fail(err)
		return
	}
	stem := "merge"
	build := filepath.Join(dir, "build", stem)
	var files []MergeFile
	for _, rel := range common {
		// a CR2W file comes out as <name>.json; a file that already is plain JSON text stays as it is
		aj, bj, plain := filepath.Join(aJSON, filepath.FromSlash(rel)+".json"), filepath.Join(bJSON, filepath.FromSlash(rel)+".json"), false
		if !fileExists(aj) || !fileExists(bj) {
			aj, bj, plain = filepath.Join(aRaw, filepath.FromSlash(rel)), filepath.Join(bRaw, filepath.FromSlash(rel)), true
		}
		x, err1 := os.ReadFile(aj)
		y, err2 := os.ReadFile(bj)
		if err1 != nil || err2 != nil {
			fail(fmt.Errorf("%s: WolvenKit couldn't make it readable", rel))
			return
		}
		merged, st, err := MergeJSON(x, y)
		if err != nil {
			fail(fmt.Errorf("%s: %v. Open both in the Workbench to merge it by hand", rel, err))
			return
		}
		st.Path = rel
		files = append(files, st)
		target := filepath.Join(build, filepath.FromSlash(rel))
		if plain {
			if err := writeFileMk(target, merged); err != nil {
				fail(err)
				return
			}
			continue
		}
		w := filepath.Join(dir, "work", filepath.FromSlash(rel)+".json")
		if err := writeFileMk(w, merged); err != nil {
			fail(err)
			return
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		l, err := s.runWK("deserialize", w, filepath.Dir(target))
		log += l + "\n"
		if err == nil && !fileExists(target) {
			err = fmt.Errorf("WolvenKit didn't make %s again", rel)
		}
		if err != nil {
			fail(err)
			return
		}
	}
	s.mergeSet(k, func(x *MergeJob) { x.State, x.Files = "packing", files })
	l, err := s.runWK("pack", build, filepath.Join(dir, "out"))
	log += l
	packed := filepath.Join(dir, "out", stem+".archive")
	if err == nil && !fileExists(packed) {
		err = errors.New("WolvenKit didn't make the merged archive")
	}
	if err != nil {
		fail(err)
		return
	}
	if gameRunning(b.Path) {
		fail(errors.New("the game was started meanwhile: close it and merge again"))
		return
	}
	data, err := os.ReadFile(packed)
	if err != nil {
		fail(err)
		return
	}
	rec, rel, err := s.installMerged(b, j, files, data)
	if err != nil {
		fail(err)
		return
	}
	s.schedChange(b, fixText(rec), rec.ID, false)
	s.mergeSet(k, func(x *MergeJob) { x.State, x.Fix, x.Archive, x.Log = "done", rec.ID, rel, tail(log, 4000) })
	os.RemoveAll(filepath.Join(dir, "a"))
	os.RemoveAll(filepath.Join(dir, "b"))
}

var reMergeName = regexp.MustCompile(`[^A-Za-z0-9 ._+-]+`)

// mergeName: the merged archive's name, so it loads before both (first loaded wins). With a
// modlist.txt it goes at the top of the list instead.
func mergeName(winner, loser, wl, ll string) (string, error) {
	short := func(s string) string {
		s = strings.TrimSpace(reMergeName.ReplaceAllString(strings.TrimSuffix(s, ".archive"), " "))
		if len(s) > 40 {
			s = strings.TrimSpace(s[:40])
		}
		return s
	}
	base := "FrameLab merge - " + short(wl) + " + " + short(ll) + ".archive"
	wn, ln := filepath.Base(winner), filepath.Base(loser)
	for _, pre := range []string{"!", "! ", "!  ", "!   "} { // not "#": a modlist.txt line starting with it may be read as a comment
		if n := pre + base; n < wn && n < ln {
			return n, nil
		}
	}
	return "", errors.New("the two archives' names sort before anything Frame Lab can name the merged one: give one of them a modlist.txt place first")
}

func (s *Server) installMerged(b BuildCfg, j MergeJob, files []MergeFile, data []byte) (*FixRecord, string, error) {
	const dir = "archive/pc/mod/"
	name, err := mergeName(j.Winner, j.Loser, j.WinnerLabel, j.LoserLabel)
	ml := filepath.Join(b.Path, "archive", "pc", "mod", "modlist.txt")
	out := map[string][]byte{}
	if cur, e := gamefs.ReadFile(ml); e == nil {
		// modlist.txt: the merged archive goes on top, everything else keeps its place
		if name == "" {
			name = "FrameLab merge - " + strings.TrimSuffix(filepath.Base(j.Winner), ".archive") + ".archive"
		}
		var lines []string
		for _, l := range strings.Split(strings.ReplaceAll(string(cur), "\r\n", "\n"), "\n") {
			if !strings.EqualFold(strings.TrimSpace(l), name) {
				lines = append(lines, l)
			}
		}
		head := []string{}
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "#") {
			head, lines = lines[:1], lines[1:]
		}
		text := strings.Join(append(append(head, name), lines...), "\r\n")
		out[dir+"modlist.txt"] = []byte(strings.TrimRight(text, "\r\n") + "\r\n")
	} else if err != nil {
		return nil, "", err
	}
	out[dir+name] = data
	added := 0
	for _, f := range files {
		added += f.Added
	}
	rec := FixRecord{Kind: "merge", Mod: j.WinnerLabel + " + " + j.LoserLabel,
		What: fmt.Sprintf("%d text file%s of %s and %s merged into %s (%d entr%s only %s had are in now)", len(files), map[bool]string{true: "", false: "s"}[len(files) == 1],
			j.WinnerLabel, j.LoserLabel, name, added, map[bool]string{true: "y", false: "ies"}[added == 1], j.LoserLabel)}
	r, err := s.writeFixFiles(b, rec, out)
	return r, dir + name, err
}

// mergeWorkbench opens both archives of a clash in the Workbench, to merge them by hand.
func (s *Server) mergeWorkbench(i int, winner, loser string) ([]string, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	if _, _, err := s.mergePair(b, winner, loser); err != nil {
		return nil, err
	}
	var ids []string
	for _, a := range []string{winner, loser} {
		p, err := s.wbNew(i, a)
		if err != nil {
			return ids, err
		}
		ids = append(ids, p.ID)
		time.Sleep(1100 * time.Millisecond) // project ids are made from the time
	}
	return ids, nil
}

func copyFileMk(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst)
}

func writeFileMk(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// ---------------------------------------------------------------- merging two JSON files

// ordered JSON: objects keep their keys in the order they came (WolvenKit reads "$type" first)
type jObj struct {
	keys []string
	vals map[string]any
}

func parseJSON(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\ufeff"))))
	d.UseNumber()
	v, err := parseVal(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err == nil {
		return nil, errors.New("more than one JSON value")
	}
	return v, nil
}

func parseVal(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			o := &jObj{vals: map[string]any{}}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := parseVal(d)
				if err != nil {
					return nil, err
				}
				if _, dup := o.vals[k]; !dup {
					o.keys = append(o.keys, k)
				}
				o.vals[k] = v
			}
			_, err := d.Token()
			return o, err
		case '[':
			a := []any{}
			for d.More() {
				v, err := parseVal(d)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			_, err := d.Token()
			return a, err
		}
		return nil, errors.New("bad JSON")
	default:
		return x, nil
	}
}

func writeJSONVal(buf *bytes.Buffer, v any, ind string) {
	switch x := v.(type) {
	case *jObj:
		if len(x.keys) == 0 {
			buf.WriteString("{}")
			return
		}
		buf.WriteString("{\n")
		for i, k := range x.keys {
			kb, _ := json.Marshal(k)
			buf.WriteString(ind + "  ")
			buf.Write(kb)
			buf.WriteString(": ")
			writeJSONVal(buf, x.vals[k], ind+"  ")
			if i < len(x.keys)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(ind + "}")
	case []any:
		if len(x) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteString("[\n")
		for i, e := range x {
			buf.WriteString(ind + "  ")
			writeJSONVal(buf, e, ind+"  ")
			if i < len(x)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(ind + "]")
	case json.Number:
		buf.WriteString(x.String())
	default:
		b, _ := json.Marshal(x)
		buf.Write(b)
	}
}

func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case *jObj:
		y, ok := b.(*jObj)
		if !ok || len(x.keys) != len(y.keys) {
			return false
		}
		for _, k := range x.keys {
			yv, ok := y.vals[k]
			if !ok || !jsonEqual(x.vals[k], yv) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case json.Number:
		y, ok := b.(json.Number)
		return ok && x.String() == y.String()
	default:
		return a == b
	}
}

// the fields entries of a list are told apart by, in the order they are tried
var mergeIDs = [][]string{{"primaryKey", "secondaryKey"}, {"stringId"}, {"secondaryKey"}, {"primaryKey"}, {"id"}, {"Id"}, {"ID"}, {"key"}, {"name"}}

func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

// entryID: the identity of a list entry by these fields ("" when it has none)
func entryID(v any, fields []string) string {
	o, ok := v.(*jObj)
	if !ok {
		return ""
	}
	var parts []string
	nonEmpty := false
	for _, f := range fields {
		fv, ok := o.vals[f]
		if !ok {
			return ""
		}
		t, ok := scalarText(fv)
		if !ok {
			return ""
		}
		if t != "" && t != "0" {
			nonEmpty = true
		}
		parts = append(parts, t)
	}
	if !nonEmpty {
		return ""
	}
	return strings.Join(parts, "\x00")
}

// listID: fields that tell every entry of both lists apart, nil when there are none
func listID(a, b []any) []string {
	for _, f := range mergeIDs {
		ok := true
		for _, l := range [][]any{a, b} {
			seen := map[string]bool{}
			for _, e := range l {
				id := entryID(e, f)
				if id == "" || seen[id] {
					ok = false
					break
				}
				seen[id] = true
			}
			if !ok {
				break
			}
		}
		if ok {
			return f
		}
	}
	return nil
}

type mergeStats struct{ added, same, changed int }

func mergeVal(w, l any, path string, st *mergeStats) (any, error) {
	switch x := w.(type) {
	case *jObj:
		y, ok := l.(*jObj)
		if !ok {
			return w, nil
		}
		out := &jObj{keys: append([]string(nil), x.keys...), vals: map[string]any{}}
		for k, v := range x.vals {
			out.vals[k] = v
		}
		for _, k := range y.keys {
			if path == "" && k == "Header" { // WolvenKit's own header: the winner's
				continue
			}
			yv := y.vals[k]
			xv, has := x.vals[k]
			if !has {
				out.keys = append(out.keys, k)
				out.vals[k] = yv
				continue
			}
			m, err := mergeVal(xv, yv, path+"/"+k, st)
			if err != nil {
				return nil, err
			}
			out.vals[k] = m
		}
		return out, nil
	case []any:
		y, ok := l.([]any)
		if !ok || jsonEqual(x, y) {
			return w, nil
		}
		if len(x) == 0 {
			st.added += len(y)
			return y, nil
		}
		if len(y) == 0 {
			return w, nil
		}
		scalars := true
		for _, e := range append(append([]any{}, x...), y...) {
			if _, ok := scalarText(e); !ok {
				scalars = false
			}
		}
		if scalars { // a plain list of values: both mods' values, the winner's first
			out := append([]any{}, x...)
			for _, e := range y {
				found := false
				for _, f := range x {
					if jsonEqual(e, f) {
						found = true
					}
				}
				if !found {
					out = append(out, e)
					st.added++
				}
			}
			return out, nil
		}
		f := listID(x, y)
		if f == nil {
			return nil, fmt.Errorf("its list %s has entries without a key or id to match them by", strings.TrimPrefix(path, "/"))
		}
		byID := map[string]any{}
		for _, e := range y {
			byID[entryID(e, f)] = e
		}
		out := make([]any, 0, len(x)+len(y))
		have := map[string]bool{}
		for _, e := range x {
			id := entryID(e, f)
			have[id] = true
			if o, ok := byID[id]; ok {
				if jsonEqual(e, o) {
					st.same++
				} else {
					st.changed++ // both changed this entry: the winner's version stays
				}
			}
			out = append(out, e)
		}
		for _, e := range y {
			if !have[entryID(e, f)] {
				out = append(out, e)
				st.added++
			}
		}
		return out, nil
	default:
		return w, nil // a single value both set: the winner's
	}
}

// MergeJSON merges the loser's JSON into the winner's.
func MergeJSON(winner, loser []byte) ([]byte, MergeFile, error) {
	w, err := parseJSON(winner)
	if err != nil {
		return nil, MergeFile{}, fmt.Errorf("the first file isn't JSON: %v", err)
	}
	l, err := parseJSON(loser)
	if err != nil {
		return nil, MergeFile{}, fmt.Errorf("the second file isn't JSON: %v", err)
	}
	var st mergeStats
	m, err := mergeVal(w, l, "", &st)
	if err != nil {
		return nil, MergeFile{}, err
	}
	var buf bytes.Buffer
	writeJSONVal(&buf, m, "")
	buf.WriteByte('\n')
	return buf.Bytes(), MergeFile{Added: st.added, Same: st.same, Changed: st.changed}, nil
}
