package optimise

// Workbench (2.5E): checks while you edit a mod file. Errors stop the file from working; warnings are
// changes that usually break a mod or the game; tips are ways to make it cost less. Every item says why.
// The checks compare with the original when there is one, so they can say what an edit changed.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// LintItem: one finding in a file.
type LintItem struct {
	Line  int    `json:"line"`  // 1-based; 0 = the whole file
	Level string `json:"level"` // error, warn, tip
	Rule  string `json:"rule"`
	Text  string `json:"text"`
	Why   string `json:"why"`
}

// FileKind: what the workbench treats a file as.
func FileKind(path string) string {
	low := strings.ToLower(path)
	switch {
	case strings.HasSuffix(low, ".reds"):
		return "redscript"
	case strings.HasSuffix(low, ".lua"):
		return "lua"
	case strings.HasSuffix(low, ".archive.xl"), strings.HasSuffix(low, ".xl"):
		return "archivexl"
	case strings.HasSuffix(low, ".yaml"), strings.HasSuffix(low, ".yml"):
		if strings.Contains(low, "r6/tweaks/") {
			return "tweakxl"
		}
		return "yaml"
	case strings.HasSuffix(low, ".tweak"):
		return "tweak"
	case strings.HasSuffix(low, ".json"):
		return "json"
	case strings.HasSuffix(low, ".ini"), strings.HasSuffix(low, ".toml"):
		return "ini"
	}
	return "text"
}

// Lint checks one file. orig is the original text ("" when there is none).
func Lint(path, text, orig string) []LintItem {
	var out []LintItem
	switch FileKind(path) {
	case "json":
		out = lintJSON(text, orig)
	case "yaml", "tweakxl", "archivexl":
		out = lintYAML(path, text, orig)
	case "redscript":
		out = lintReds(text, orig)
	case "lua":
		out = lintLua(text)
	}
	if strings.Contains(text, "\t") && FileKind(path) != "lua" && FileKind(path) != "redscript" && FileKind(path) != "text" && FileKind(path) != "ini" {
		for i, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimLeft(l, " "), "\t") {
				out = append(out, LintItem{Line: i + 1, Level: "error", Rule: "tab", Text: "a tab at the start of the line",
					Why: "YAML and the files made from it only allow spaces for indenting; a tab makes the whole file unreadable for the game."})
				break
			}
		}
	}
	return out
}

func lineOf(text string, off int) int {
	if off > len(text) {
		off = len(text)
	}
	return strings.Count(text[:off], "\n") + 1
}

// ---- JSON (WolvenKit's readable form of game files)

var reJSONKey = regexp.MustCompile(`^\s*"([^"]+)"\s*:\s*(.*?),?\s*$`)

func lintJSON(text, orig string) []LintItem {
	var out []LintItem
	var v any
	dec := json.NewDecoder(strings.NewReader(text))
	if err := dec.Decode(&v); err != nil {
		line := 0
		if se, ok := err.(*json.SyntaxError); ok {
			line = lineOf(text, int(se.Offset))
		} else if te, ok := err.(*json.UnmarshalTypeError); ok {
			line = lineOf(text, int(te.Offset))
		}
		out = append(out, LintItem{Line: line, Level: "error", Rule: "json", Text: "not valid JSON: " + err.Error(),
			Why: "WolvenKit can't turn the file back into a game file; usually a missing comma, quote or bracket on this line or the one before."})
		return out
	}
	if orig == "" {
		return out
	}
	// what an edit changed: keys that are structure, not values
	guard := map[string]string{
		"$type":            "the class of this part of the file; the game expects exactly this class here, another one crashes or is ignored",
		"HandleId":         "how parts of the file point at each other; a changed id breaks those links",
		"HandleRefId":      "how parts of the file point at each other; a changed id breaks those links",
		"BufferId":         "which data buffer a part uses; the buffers themselves aren't in the readable form",
		"Flags":            "internal flags of the file format; leave them as they are",
		"WolvenKitVersion": "written by WolvenKit; leave it",
		"ArchiveFileName":  "written by WolvenKit; leave it",
	}
	ol := strings.Split(orig, "\n")
	nl := strings.Split(text, "\n")
	for _, h := range LineDiff(ol, nl) {
		for k := h.B; k < h.B+h.BN; k++ {
			m := reJSONKey.FindStringSubmatch(nl[k])
			if m == nil {
				continue
			}
			if why, ok := guard[m[1]]; ok {
				out = append(out, LintItem{Line: k + 1, Level: "warn", Rule: "json_structure", Text: `"` + m[1] + `" changed`, Why: why + "."})
			}
			if m[1] == "DepotPath" || m[1] == "$value" && strings.Contains(m[2], `\\`) {
				out = append(out, LintItem{Line: k + 1, Level: "tip", Rule: "json_path", Text: "a game path changed",
					Why: "The path has to exist: in the game or in an archive of your mod. A wrong path shows as an invisible or missing thing in game, without an error."})
			}
		}
	}
	return out
}

// ---- YAML (TweakXL tweaks, ArchiveXL .xl)

var (
	reYKey      = regexp.MustCompile(`^(\s*)(-\s+)?("[^"]*"|'[^']*'|[^:#\s][^:#]*?)\s*:(\s*|\s+.*)$`)
	reYNoSpace  = regexp.MustCompile(`^\s*(-\s+)?[\w.$]+:[^\s/\\]`)
	reNodeType  = regexp.MustCompile(`^\s*-?\s*type:\s*(\w+)`)
	reIndexLine = regexp.MustCompile(`^\s*-?\s*index:\s*(.*?)\s*(#.*)?$`)
)

func lintYAML(path, text, orig string) []LintItem {
	var out []LintItem
	lines := strings.Split(text, "\n")
	topKeys := map[string]int{}
	inBlock := false // inside a | or > block: anything goes
	blockInd := 0
	for i, raw := range lines {
		l := strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		ind := len(l) - len(strings.TrimLeft(l, " "))
		if inBlock {
			if ind > blockInd {
				continue
			}
			inBlock = false
		}
		if strings.HasSuffix(t, "|") || strings.HasSuffix(t, ">") || strings.HasSuffix(t, "|-") || strings.HasSuffix(t, ">-") {
			inBlock, blockInd = true, ind
		}
		if reYNoSpace.MatchString(l) && !strings.Contains(t, "://") && !strings.Contains(t, `"`) {
			out = append(out, LintItem{Line: i + 1, Level: "error", Rule: "yaml_space", Text: "no space after the colon",
				Why: "YAML reads \"key:value\" as one word; it has to be \"key: value\"."})
		}
		if m := reYKey.FindStringSubmatch(l); m != nil && ind == 0 && m[2] == "" {
			k := strings.Trim(m[3], `"'`)
			if p, ok := topKeys[k]; ok {
				out = append(out, LintItem{Line: i + 1, Level: "warn", Rule: "yaml_dup", Text: "\"" + k + "\" is already set on line " + fmt.Sprint(p),
					Why: "Only one of the two counts; usually the last one. Put the values together under one key."})
			} else {
				topKeys[k] = i + 1
			}
		}
		if FileKind(path) == "archivexl" {
			if m := reNodeType.FindStringSubmatch(l); m != nil && !strings.HasPrefix(m[1], "world") {
				out = append(out, LintItem{Line: i + 1, Level: "error", Rule: "xl_type", Text: "\"" + m[1] + "\" isn't a node type",
					Why: "Node types are the game's class names, like worldStaticMeshNode or worldCookedStaticMeshNode. ArchiveXL compares it with the node in the game and refuses the edit if it differs."})
			}
			if m := reIndexLine.FindStringSubmatch(l); m != nil && !regexp.MustCompile(`^\d+$`).MatchString(m[1]) {
				out = append(out, LintItem{Line: i + 1, Level: "error", Rule: "xl_index", Text: "an index has to be a number",
					Why: "It is the node's position in the sector's list (0 is the first)."})
			}
		}
	}
	if FileKind(path) == "archivexl" && orig != "" {
		ol, nl := strings.Split(orig, "\n"), strings.Split(text, "\n")
		for _, h := range LineDiff(ol, nl) {
			for k := h.B; k < h.B+h.BN; k++ {
				if strings.Contains(nl[k], "expectedNodes") {
					out = append(out, LintItem{Line: k + 1, Level: "warn", Rule: "xl_expected", Text: "expectedNodes changed",
						Why: "It must be the number of nodes the sector has in this game version. ArchiveXL uses it to notice that a game update moved things; a wrong number makes it refuse the edit, a forced one can delete the wrong objects."})
				}
				if reIndexLine.MatchString(nl[k]) {
					out = append(out, LintItem{Line: k + 1, Level: "tip", Rule: "xl_index_changed", Text: "a node index changed",
						Why: "Check that the new index is the same object as before (position, mesh). After a game update the list can shift, and the same number then points at something else."})
				}
			}
		}
	}
	if FileKind(path) == "tweakxl" && orig != "" {
		ol, nl := strings.Split(orig, "\n"), strings.Split(text, "\n")
		for _, h := range LineDiff(ol, nl) {
			for k := h.B; k < h.B+h.BN; k++ {
				if strings.Contains(nl[k], "$type") {
					out = append(out, LintItem{Line: k + 1, Level: "warn", Rule: "tweak_type", Text: "$type changed",
						Why: "The record's class: other records and the game's code expect this class; another one makes the record useless or crashes."})
				}
				if strings.Contains(nl[k], "$base") {
					out = append(out, LintItem{Line: k + 1, Level: "tip", Rule: "tweak_base", Text: "$base changed",
						Why: "The new record copies everything from its base first; make sure the base exists in this game version."})
				}
			}
		}
	}
	return out
}

// ---- REDscript

var (
	reHotFunc = regexp.MustCompile(`^(OnUpdate|OnAction|OnTick|Update|Tick|OnFrame|OnEvaluate)$`)
	reLookup  = regexp.MustCompile(`GameInstance\.Get\w+\(|GetScriptableSystemsContainer\(|\.GetBlackboardSystem\(|GameInstance\.FindEntityByID\(`)
	reLogCall = regexp.MustCompile(`\b(FTLog|LogChannel|Log|ModLog|LogError|LogWarning)\(`)
)

func lintReds(text, orig string) []LintItem {
	var out []LintItem
	m := redsMask(text)
	// brackets: the first one without a partner
	type br struct {
		c   byte
		off int
	}
	var st []br
	pair := map[byte]byte{')': '(', ']': '[', '}': '{'}
	bad := -1
	for i := 0; i < len(m) && bad < 0; i++ {
		switch c := m[i]; c {
		case '(', '[', '{':
			st = append(st, br{c, i})
		case ')', ']', '}':
			if len(st) == 0 || st[len(st)-1].c != pair[c] {
				bad = i
			} else {
				st = st[:len(st)-1]
			}
		}
	}
	if bad >= 0 {
		out = append(out, LintItem{Line: lineOf(text, bad), Level: "error", Rule: "brackets", Text: "a closing bracket without its opening one",
			Why: "The script doesn't compile, and while one script fails the game runs without ANY mod scripts."})
	} else if len(st) > 0 {
		out = append(out, LintItem{Line: lineOf(text, st[len(st)-1].off), Level: "error", Rule: "brackets", Text: "this bracket is never closed",
			Why: "The script doesn't compile, and while one script fails the game runs without ANY mod scripts."})
	}
	if bad >= 0 || len(st) > 0 {
		return out
	}
	for _, f := range ParseReds(text) {
		if f.BodyStart < 0 || f.BodyEnd < 0 {
			continue
		}
		body := m[f.BodyStart:f.BodyEnd]
		switch f.Kind {
		case "wrap":
			if !strings.Contains(body, "wrappedMethod(") {
				out = append(out, LintItem{Line: f.Line, Level: "warn", Rule: "wrap_no_call", Text: f.Class + "." + f.Name + " never calls wrappedMethod()",
					Why: "The game's own function (and every other mod that wraps it) then never runs. Call wrappedMethod(...) unless skipping the game's code is really the point."})
			}
		case "replace":
			out = append(out, LintItem{Line: f.Line, Level: "tip", Rule: "replace", Text: "@replaceMethod replaces the game's function for everyone",
				Why: "Only one mod's replacement can run, and wraps by other mods are lost. @wrapMethod with wrappedMethod() works together with other mods."})
		}
		if reHotFunc.MatchString(f.Name) || f.Callback && strings.HasPrefix(f.Name, "OnAction") {
			if loc := reLookup.FindStringIndex(body); loc != nil {
				out = append(out, LintItem{Line: lineOf(text, f.BodyStart+loc[0]), Level: "tip", Rule: "hot_lookup", Text: "a system lookup in " + f.Name,
					Why: f.Name + " runs very often (every frame or every key press). Look the system up once and keep it in a field (a wref)."})
			}
			if loc := reLogCall.FindStringIndex(body); loc != nil {
				out = append(out, LintItem{Line: lineOf(text, f.BodyStart+loc[0]), Level: "tip", Rule: "hot_log", Text: "logging in " + f.Name,
					Why: "Writing a log line every frame or key press costs time and fills the log. Log only when something changes."})
			}
		}
	}
	for _, x := range arrayContainsFixes(text) {
		out = append(out, LintItem{Line: x.Line, Level: "tip", Rule: "array_contains", Text: x.Title, Why: x.What})
	}
	if orig != "" {
		a, b := ParseReds(orig), ParseReds(text)
		names := map[string]bool{}
		for _, f := range b {
			names[f.Kind+"|"+f.Class+"|"+f.Name] = true
		}
		for _, f := range a {
			if (f.Kind == "wrap" || f.Kind == "replace" || f.Kind == "add") && !names[f.Kind+"|"+f.Class+"|"+f.Name] {
				out = append(out, LintItem{Line: 0, Level: "warn", Rule: "func_removed", Text: "@" + map[string]string{"wrap": "wrapMethod", "replace": "replaceMethod", "add": "addMethod"}[f.Kind] + " " + f.Class + "." + f.Name + " is gone",
					Why: "Other parts of the mod (or other mods) may call or expect it; a missing @addMethod makes every caller fail to compile."})
			}
		}
	}
	return out
}

// ---- Lua (CET mods)

var (
	reEvent     = regexp.MustCompile(`registerForEvent\(\s*["']([^"']+)["']`)
	knownEvents = map[string]bool{"onInit": true, "onUpdate": true, "onDraw": true, "onOverlayOpen": true, "onOverlayClose": true, "onShutdown": true, "onTweak": true, "onHook": true}
)

func lintLua(text string) []LintItem {
	var out []LintItem
	depth := 0
	var opens []int
	firstBad := -1
	luaTokens(text, 0, func(word string, at int) bool {
		switch word {
		case "function", "if", "do", "repeat":
			depth++
			opens = append(opens, at)
		case "end", "until":
			depth--
			if depth < 0 && firstBad < 0 {
				firstBad = at
				return false
			}
			if len(opens) > 0 {
				opens = opens[:len(opens)-1]
			}
		}
		return true
	})
	switch {
	case firstBad >= 0:
		out = append(out, LintItem{Line: lineOf(text, firstBad), Level: "error", Rule: "lua_blocks", Text: "an \"end\" too many",
			Why: "CET can't load the mod at all; the CET log names the line. Every function / if / for / while / do needs exactly one end."})
	case depth > 0 && len(opens) > 0:
		out = append(out, LintItem{Line: lineOf(text, opens[len(opens)-1]), Level: "error", Rule: "lua_blocks", Text: "this block has no \"end\"",
			Why: "CET can't load the mod at all. Every function / if / for / while / do needs exactly one end."})
	}
	for _, m := range reEvent.FindAllStringSubmatchIndex(text, -1) {
		if name := text[m[2]:m[3]]; !knownEvents[name] {
			out = append(out, LintItem{Line: lineOf(text, m[0]), Level: "warn", Rule: "lua_event", Text: "CET has no event \"" + name + "\"",
				Why: "The function is never called. CET's events are onInit, onUpdate, onDraw, onOverlayOpen, onOverlayClose, onShutdown and onTweak."})
		}
	}
	for _, h := range ScanLua(text) {
		if h.Kind == "candidate" {
			out = append(out, LintItem{Line: h.Line, Level: "tip", Rule: "lua_every_frame", Text: "this onUpdate works every frame",
				Why: h.Why + ". A few times a second usually does: count dt and only work when 0.1 s or more has passed."})
		}
	}
	return out
}

// ---- line diff (Myers), shared by the checks and the workbench's "show changes"

// Hunk: lines A..A+AN of the old text became lines B..B+BN of the new one (0-based).
type Hunk struct {
	A  int `json:"a"`
	AN int `json:"an"`
	B  int `json:"b"`
	BN int `json:"bn"`
}

// LineDiff: the changed runs of lines between a and b.
func LineDiff(a, b []string) []Hunk {
	// trim the common start and end: most edits are small
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	x, y := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(x) == 0 && len(y) == 0 {
		return nil
	}
	n, m := len(x), len(y)
	max := n + m
	const maxD = 2000 // more changes than this: shown as one block
	v := make([]int32, 2*max+3)
	off := max + 1
	type snap struct {
		lo   int
		vals []int32
	}
	get := func(sn snap, k int) int {
		if i := k - sn.lo; i >= 0 && i < len(sn.vals) {
			return int(sn.vals[i])
		}
		return 0
	}
	var trace []snap
	found := false
	for d := 0; d <= max && !found; d++ {
		if d > maxD {
			return []Hunk{{pre, n, pre, m}}
		}
		lo, hi := off-d-1, off+d+2
		if lo < 0 {
			lo = 0
		}
		if hi > len(v) {
			hi = len(v)
		}
		trace = append(trace, snap{lo - off, append([]int32(nil), v[lo:hi]...)})
		for k := -d; k <= d; k += 2 {
			var i int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				i = int(v[off+k+1])
			} else {
				i = int(v[off+k-1]) + 1
			}
			j := i - k
			for i < n && j < m && x[i] == y[j] {
				i++
				j++
			}
			v[off+k] = int32(i)
			if i >= n && j >= m {
				found = true
				break
			}
		}
	}
	// walk back: which lines are equal
	eqA := make([]bool, n)
	eqB := make([]bool, m)
	i, j := n, m
	for d := len(trace) - 1; d > 0 && (i > 0 || j > 0); d-- {
		sn := trace[d]
		k := i - j
		var pk int
		if k == -d || k != d && get(sn, k-1) < get(sn, k+1) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		pi := get(sn, pk)
		pj := pi - pk
		for i > pi && j > pj {
			i--
			j--
			eqA[i], eqB[j] = true, true
		}
		i, j = pi, pj
	}
	for i > 0 && j > 0 && x[i-1] == y[j-1] {
		i--
		j--
		eqA[i], eqB[j] = true, true
	}
	var out []Hunk
	ia, ib := 0, 0
	for ia < n || ib < m {
		if ia < n && ib < m && eqA[ia] && eqB[ib] {
			ia++
			ib++
			continue
		}
		h := Hunk{A: ia + pre, B: ib + pre}
		for ia < n && !eqA[ia] {
			ia++
			h.AN++
		}
		for ib < m && !eqB[ib] {
			ib++
			h.BN++
		}
		if h.AN == 0 && h.BN == 0 { // shouldn't happen; step on
			ia++
			ib++
			continue
		}
		out = append(out, h)
	}
	return out
}

// sameText: equal apart from line ends
func sameText(a, b []byte) bool {
	return bytes.Equal(bytes.ReplaceAll(a, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
}

var _ = sameText
