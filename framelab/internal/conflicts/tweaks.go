package conflicts

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TweakXL: .yaml files in r6/tweaks set TweakDB records and values. When two mods set the same value
// to different things only one of them sticks. This reads plain "key: value" lines up to two levels
// deep (Record: / property: value, or Record.property: value) and skips lists, appends and anything it
// can't read simply, so it finds the common case, not every possible clash.

var reInlineComment = regexp.MustCompile(`\s#`)

func tweakOwner(rel string) string {
	rel = filepath.ToSlash(rel)
	if i := strings.Index(rel, "/"); i > 0 {
		return rel[:i]
	}
	return strings.TrimSuffix(rel, filepath.Ext(rel))
}

func scalar(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	if m := reInlineComment.FindStringIndex(v); m != nil {
		v = strings.TrimSpace(v[:m[0]])
	}
	if v == "" {
		return "", false
	}
	switch v[0] {
	case '[', '{', '|', '>', '&', '*', '!', '-':
		if v[0] == '-' && len(v) > 1 && (v[1] >= '0' && v[1] <= '9' || v[1] == '.') {
			break // negative number
		}
		return "", false
	}
	return strings.Trim(v, `"'`), true
}

func indentOf(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' {
			n++
		} else if c == '\t' {
			n += 2
		} else {
			break
		}
	}
	return n
}

type tweakSet struct{ owner, value, file string }

func checkTweaks(r *Report, o Options) {
	root := filepath.Join(o.Game, "r6", "tweaks")
	sets := map[string][]tweakSet{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		r.TweaksN++
		rel, _ := filepath.Rel(root, p)
		owner := tweakOwner(rel)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		top := ""       // current top-level record
		topInd := -1    // indent of the record's first child
		skipBelow := -1 // inside a list or block: ignore lines indented deeper than this
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), " \r\t")
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") || trim == "---" {
				continue
			}
			ind := indentOf(line)
			if skipBelow >= 0 {
				if ind > skipBelow {
					continue
				}
				skipBelow = -1
			}
			if strings.HasPrefix(trim, "- ") || trim == "-" {
				skipBelow = ind - 1
				if skipBelow < 0 {
					skipBelow = 0
				}
				continue
			}
			i := strings.Index(trim, ":")
			if i <= 0 {
				continue
			}
			key := strings.Trim(strings.TrimSpace(trim[:i]), `"'`)
			val := trim[i+1:]
			if ind == 0 {
				top, topInd = key, -1
				if v, ok := scalar(val); ok && strings.Contains(key, ".") {
					sets[key] = append(sets[key], tweakSet{owner, v, filepath.ToSlash(rel)})
				} else if strings.TrimSpace(val) != "" {
					top = "" // a value we can't read simply
				}
				continue
			}
			if top == "" {
				continue
			}
			if topInd < 0 {
				topInd = ind
			}
			if ind != topInd {
				if ind > topInd {
					continue // deeper levels: nested structures
				}
				continue
			}
			if strings.HasPrefix(key, "$") {
				continue // $type / $base
			}
			v, ok := scalar(val)
			if !ok {
				if strings.TrimSpace(val) == "" {
					skipBelow = ind // a nested block under this property
				}
				continue
			}
			k := top + "." + key
			sets[k] = append(sets[k], tweakSet{owner, v, filepath.ToSlash(rel)})
		}
		return nil
	})
	for k, ss := range sets {
		byOwner := map[string]tweakSet{}
		vals := map[string]bool{}
		for _, s := range ss {
			byOwner[s.owner] = s
		}
		for _, s := range byOwner {
			vals[strings.ToLower(s.value)] = true
		}
		if len(byOwner) < 2 || len(vals) < 2 {
			continue
		}
		t := Tweak{Key: k}
		for _, s := range byOwner {
			mod := label(o, "tweak:", s.owner)
			t.Values = append(t.Values, TweakVal{Mod: mod, Value: s.value, File: s.file})
			if LooksLikePatch(mod) || LooksLikePatch(s.file) {
				t.Patch = true
			}
		}
		sort.Slice(t.Values, func(i, j int) bool { return t.Values[i].File < t.Values[j].File })
		r.Tweaks = append(r.Tweaks, t)
	}
	sort.Slice(r.Tweaks, func(i, j int) bool {
		if r.Tweaks[i].Patch != r.Tweaks[j].Patch {
			return !r.Tweaks[i].Patch
		}
		return r.Tweaks[i].Key < r.Tweaks[j].Key
	})
}
