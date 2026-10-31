package conflicts

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// REDscript: @replaceMethod(Class) / @replaceGlobal() swap a game function for the mod's own. When two
// mods replace the same function only one replacement runs. @wrapMethod / @wrapGlobal chain and are
// fine; they are listed with a replaced function for context.

var (
	reLineComment  = regexp.MustCompile(`(?m)//.*$`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reAnnot        = regexp.MustCompile(`@(replaceMethod|wrapMethod|replaceGlobal|wrapGlobal)\(\s*([A-Za-z_][\w.]*)?\s*\)`)
	reFunc         = regexp.MustCompile(`(?s)^[^{;@]*?\bfunc\s+([A-Za-z_]\w*)\s*\(([^)]*)\)`)
	reParamType    = regexp.MustCompile(`:\s*([^,]+)`)
)

// owner of a .reds file: the folder under r6/scripts, or the file name for a file directly in it
func scriptOwner(rel string) string {
	rel = filepath.ToSlash(rel)
	if i := strings.Index(rel, "/"); i > 0 {
		return rel[:i]
	}
	return strings.TrimSuffix(rel, filepath.Ext(rel))
}

func paramTypes(params string) string {
	var ts []string
	for _, p := range strings.Split(params, ",") {
		m := reParamType.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		t := strings.Join(strings.Fields(m[1]), "")
		ts = append(ts, t)
	}
	return strings.Join(ts, ",")
}

type scriptHit struct {
	kind, owner, file string
}

func checkScripts(r *Report, o Options) {
	root := filepath.Join(o.Game, "r6", "scripts")
	hits := map[string][]scriptHit{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".reds") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		r.ScriptsN++
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		src := reBlockComment.ReplaceAllString(string(b), "")
		src = reLineComment.ReplaceAllString(src, "")
		for _, m := range reAnnot.FindAllStringSubmatchIndex(src, -1) {
			kind := src[m[2]:m[3]]
			class := ""
			if m[4] >= 0 {
				class = src[m[4]:m[5]]
			}
			rest := src[m[1]:]
			if len(rest) > 600 {
				rest = rest[:600]
			}
			f := reFunc.FindStringSubmatch(rest)
			if f == nil {
				continue
			}
			target := f[1] + "(" + paramTypes(f[2]) + ")"
			if strings.HasSuffix(kind, "Method") {
				if class == "" {
					continue
				}
				target = class + "." + target
			}
			hits[target] = append(hits[target], scriptHit{kind, scriptOwner(rel), rel})
		}
		return nil
	})
	for target, hs := range hits {
		repl := map[string]bool{}
		var mods, files, wraps []string
		kind := ""
		for _, h := range hs {
			if strings.HasPrefix(h.kind, "replace") {
				kind = h.kind
				if !repl[h.owner] {
					repl[h.owner] = true
					mods = append(mods, label(o, "reds:", h.owner))
				}
				files = append(files, h.file)
			}
		}
		if len(repl) < 2 {
			continue
		}
		seenW := map[string]bool{}
		for _, h := range hs {
			if strings.HasPrefix(h.kind, "wrap") && !seenW[h.owner] {
				seenW[h.owner] = true
				wraps = append(wraps, label(o, "reds:", h.owner))
			}
		}
		sort.Strings(mods)
		sort.Strings(files)
		sort.Strings(wraps)
		r.Scripts = append(r.Scripts, Script{Target: target, Kind: kind, Mods: mods, Files: files, Wraps: wraps})
	}
	sort.Slice(r.Scripts, func(i, j int) bool { return r.Scripts[i].Target < r.Scripts[j].Target })
}

// label: the library zip name for an owner key, else the owner itself
func label(o Options, prefix, owner string) string {
	if l, _ := o.Library.Find(prefix + strings.ToLower(owner)); l != "" {
		if !strings.EqualFold(l, owner) {
			return l + " (" + owner + ")"
		}
		return l
	}
	return owner
}
