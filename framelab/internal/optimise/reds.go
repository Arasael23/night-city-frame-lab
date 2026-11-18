package optimise

// A small REDscript reader (2.5E Optimise stage 2): finds every function in a .reds file with the class
// it belongs to (or the game class it wraps), its line and its body. Comments and strings are skipped,
// so braces inside them don't count. It is not a compiler: anything it can't follow, it leaves alone.

import (
	"strings"
)

// RedsFunc: one function in a .reds file.
type RedsFunc struct {
	Class     string `json:"class,omitempty"` // owning class/struct, or the game class of a @wrapMethod/@replaceMethod/@addMethod
	Name      string `json:"name"`
	Line      int    `json:"line"` // line of the "func" keyword
	Kind      string `json:"kind"` // method, wrap, replace, add, global
	Callback  bool   `json:"callback,omitempty"`
	BodyStart int    `json:"-"` // byte offset of the body's "{" (-1 when it has none: native/import)
	BodyEnd   int    `json:"-"` // byte offset of the matching "}"
}

// redsMask returns the text with comments and string/name literals blanked out (same length, newlines
// kept), so offsets and line numbers stay the same.
func redsMask(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); {
		switch {
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			depth := 0
			for i < len(b) {
				if b[i] == '/' && i+1 < len(b) && b[i+1] == '*' {
					depth++
					b[i], b[i+1] = ' ', ' '
					i += 2
					continue
				}
				if b[i] == '*' && i+1 < len(b) && b[i+1] == '/' {
					depth--
					b[i], b[i+1] = ' ', ' '
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				if b[i] != '\n' {
					b[i] = ' '
				}
				i++
			}
		case b[i] == '"':
			i++
			for i < len(b) && b[i] != '"' && b[i] != '\n' {
				if b[i] == '\\' && i+1 < len(b) {
					b[i] = ' '
					i++
				}
				b[i] = ' '
				i++
			}
			i++
		default:
			i++
		}
	}
	return string(b)
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// matchBrace: offset of the "}" closing the "{" at open (in masked text), or -1
func matchBrace(m string, open int) int {
	d := 0
	for i := open; i < len(m); i++ {
		switch m[i] {
		case '{':
			d++
		case '}':
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}

// ParseReds lists the functions of one .reds file.
func ParseReds(src string) []RedsFunc {
	m := redsMask(src)
	var out []RedsFunc
	type scope struct {
		class string
		end   int
	}
	var classes []scope
	ann, annClass := "", ""
	lineAt := func(off int) int { return strings.Count(src[:off], "\n") + 1 }
	word := func(i int) (string, int) {
		j := i
		for j < len(m) && isIdent(m[j]) {
			j++
		}
		return m[i:j], j
	}
	skipSp := func(i int) int {
		for i < len(m) && (m[i] == ' ' || m[i] == '\t' || m[i] == '\r' || m[i] == '\n') {
			i++
		}
		return i
	}
	for i := 0; i < len(m); i++ {
		for len(classes) > 0 && i > classes[len(classes)-1].end {
			classes = classes[:len(classes)-1]
		}
		c := m[i]
		if c == '@' {
			w, j := word(i + 1)
			switch w {
			case "wrapMethod", "replaceMethod", "addMethod", "replaceGlobal", "wrapGlobal":
				ann = w
				annClass = ""
				if k := skipSp(j); k < len(m) && m[k] == '(' {
					if e := strings.IndexByte(m[k:], ')'); e > 0 {
						annClass = strings.TrimSpace(m[k+1 : k+e])
					}
				}
			}
			i = j - 1
			continue
		}
		if !isIdent(c) || (i > 0 && isIdent(m[i-1])) {
			continue
		}
		w, j := word(i)
		switch w {
		case "class", "struct":
			k := skipSp(j)
			name, k2 := word(k)
			if name == "" {
				break
			}
			if ob := strings.IndexByte(m[k2:], '{'); ob >= 0 {
				semi := strings.IndexByte(m[k2:], ';')
				if semi >= 0 && semi < ob { // a forward/native declaration
					break
				}
				if end := matchBrace(m, k2+ob); end > 0 {
					classes = append(classes, scope{class: name, end: end})
				}
			}
			ann = ""
		case "func":
			k := skipSp(j)
			name, k2 := word(k)
			if name == "" {
				break
			}
			f := RedsFunc{Name: name, Line: lineAt(i), BodyStart: -1, BodyEnd: -1}
			// callback functions are written "cb func" or "callback func"
			pre := strings.TrimRight(m[maxInt(0, i-40):i], " \t\r\n")
			f.Callback = strings.HasSuffix(pre, "cb") || strings.HasSuffix(pre, "callback")
			switch {
			case ann == "wrapMethod":
				f.Kind, f.Class = "wrap", annClass
			case ann == "replaceMethod":
				f.Kind, f.Class = "replace", annClass
			case ann == "addMethod":
				f.Kind, f.Class = "add", annClass
			case len(classes) > 0:
				f.Kind, f.Class = "method", classes[len(classes)-1].class
			default:
				f.Kind = "global"
			}
			ann = ""
			// the body: the first "{" after the closing ")" of the parameters, unless a ";" comes first
			if p := strings.IndexByte(m[k2:], '('); p >= 0 {
				d, q := 0, k2+p
				for ; q < len(m); q++ {
					if m[q] == '(' {
						d++
					} else if m[q] == ')' {
						d--
						if d == 0 {
							break
						}
					}
				}
				rest := m[q:]
				ob, semi := strings.IndexByte(rest, '{'), strings.IndexByte(rest, ';')
				if ob >= 0 && (semi < 0 || ob < semi) {
					f.BodyStart = q + ob
					f.BodyEnd = matchBrace(m, q+ob)
					if f.BodyEnd > 0 {
						i = f.BodyEnd // nothing inside a body declares a function
					}
				}
			}
			out = append(out, f)
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// FindRedsFunc: the function HitchRecorder names "Class::Func" (or "Func") in one file's functions.
// A wrap/replace of a game class counts for that class.
func FindRedsFunc(fns []RedsFunc, label string) (RedsFunc, bool) {
	cls, name := "", label
	if i := strings.LastIndex(label, "::"); i >= 0 {
		cls, name = label[:i], label[i+2:]
	}
	// HitchRecorder may give the full name "Module.Class"; .reds files use the short one
	if i := strings.LastIndexByte(cls, '.'); i >= 0 {
		cls = cls[i+1:]
	}
	for _, f := range fns {
		if f.Name == name && (cls == "" && f.Class == "" || strings.EqualFold(f.Class, cls)) {
			return f, true
		}
	}
	return RedsFunc{}, false
}
