package optimise

import (
	"fmt"
	"regexp"
	"strings"
)

// LuaHandler: one CET onUpdate handler (runs every frame) and what it does.
//
//	cheap        nothing but timers, or returns at once when idle: nothing to gain
//	own timer    counts time itself and only works every so often
//	every frame  moves the camera, vehicles, objects or audio in step with frames: must stay as it is
//	candidate    checks or updates game state every frame although a few times a second would do
type LuaHandler struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Kind  string `json:"kind"`
	Why   string `json:"why"`
	Lines int    `json:"lines"`
}

var (
	reHandler  = regexp.MustCompile(`registerForEvent\(\s*["']onUpdate["']\s*,\s*function\s*\(([^)]*)\)`)
	reUnsafe   = regexp.MustCompile(`Teleport|SetWorldPosition|SetLocalPosition|SetLocalOrientation|SetWorldTransform|SetVelocity|ApplyImpulse|AddLinearImpulse|GetCameraSystem|[Cc]amera|fpp|SetTimeDilation|[Ll]erp|[Ii]nterpolat|[Ss]mooth|[Aa]nimat|ImGui|\brpm\b|RPM|[Gg]ear|[Ss]teer|[Pp]hysics|SetPosition|Transition|[Ss]ound|[Aa]udio|[Ii]nput|[Kk]ey[Pp]ress|IsKeyDown|Drift|Speed`)
	reGameWork = regexp.MustCompile(`Game\.|GetPlayer\(|GetSingleton\(|NewObject\(|\w+[.:](?:update|Update|tick|Tick|check|Check|scan|Scan|refresh|Refresh|process|Process|run|Run)\w*\s*\(|processTaskQueue|\w+\([^)]*\)`)
	reCronOnly = regexp.MustCompile(`^\s*(?:\w+\.)*Cron\.Update\(\s*\w+\s*\)\s*;?\s*$`)
	reAcc      = regexp.MustCompile(`([\w.]+)\s*=\s*\(?\s*[\w.]+\s*(?:or\s*[\d.]+\s*)?\)?\s*\+\s*\(?\s*(\w+)\b`)
	reEarlyRet = regexp.MustCompile(`^\s*if\s+.*then\s+return\s+end\s*$`)
	reEndElse  = regexp.MustCompile(`^\s*(end|else)\b`)
	reQueue    = regexp.MustCompile(`\bpcall\(\s*flush\s*\)|\bflush\(\s*\)`)
	reIfThen   = regexp.MustCompile(`^\s*if\b.*\bthen\s*$`)
)

// luaTokens walks Lua source and calls fn for every block keyword outside strings and comments
// (function, if, do, repeat, end, until), with its byte offset.
func luaTokens(s string, from int, fn func(word string, at int) bool) {
	longEnd := func(i int) (level int, ok bool) { // s[i] == '[': [[ or [==[
		j := i + 1
		for j < len(s) && s[j] == '=' {
			j++
		}
		if j < len(s) && s[j] == '[' {
			return j - i - 1, true
		}
		return 0, false
	}
	skipLong := func(i, level int) int { // i just after the opening bracket
		closer := "]" + strings.Repeat("=", level) + "]"
		if k := strings.Index(s[i:], closer); k >= 0 {
			return i + k + len(closer)
		}
		return len(s)
	}
	isWord := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	for i := from; i < len(s); {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			if i+2 < len(s) && s[i+2] == '[' {
				if lv, ok := longEnd(i + 2); ok {
					i = skipLong(i+2+lv+2, lv)
					continue
				}
			}
			if k := strings.IndexByte(s[i:], '\n'); k >= 0 {
				i += k + 1
			} else {
				i = len(s)
			}
		case c == '[':
			if lv, ok := longEnd(i); ok {
				i = skipLong(i+lv+2, lv)
				continue
			}
			i++
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c && s[j] != '\n' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
		case isWord(c):
			j := i
			for j < len(s) && isWord(s[j]) {
				j++
			}
			if i == 0 || !isWord(s[i-1]) && s[i-1] != '.' && s[i-1] != ':' {
				switch w := s[i:j]; w {
				case "function", "if", "do", "repeat", "end", "until":
					if !fn(w, i) {
						return
					}
				}
			}
			i = j
		default:
			i++
		}
	}
}

func stripLuaComments(s string) string {
	re := regexp.MustCompile(`(?s)--\[=*\[.*?\]=*\]|--[^\n]*`)
	return re.ReplaceAllString(s, "")
}

func classifyLua(body, params string) (kind, why string) {
	b := stripLuaComments(body)
	var lines []string
	for _, l := range strings.Split(b, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if strings.Contains(b, "__fl_acc") {
		return "done", "already slowed down by Frame Lab"
	}
	dt := strings.TrimSpace(strings.Split(params, ",")[0])
	if dt != "" {
		for _, m := range reAcc.FindAllStringSubmatchIndex(b, -1) {
			acc, v := b[m[2]:m[3]], b[m[4]:m[5]]
			if v != dt {
				continue
			}
			q := regexp.QuoteMeta(acc)
			if regexp.MustCompile(q + `\s*[<>]=?|[<>]=?\s*` + q).MatchString(b[m[1]:]) {
				return "own timer", "counts time itself (" + acc + ")"
			}
		}
	}
	all := true
	for _, l := range lines {
		if !(reCronOnly.MatchString(l) || reEndElse.MatchString(l) || reIfThen.MatchString(l)) {
			all = false
			break
		}
	}
	if all {
		return "cheap", "only runs its timers"
	}
	if len(lines) > 0 && reEarlyRet.MatchString(lines[0]) && len(lines) <= 6 {
		return "cheap", "returns at once when it has nothing to do"
	}
	if reQueue.MatchString(b) {
		return "every frame", "works through its own queue every frame by design"
	}
	if u := reUnsafe.FindString(b); u != "" {
		return "every frame", "works with '" + u + "', needs every frame"
	}
	if !reGameWork.MatchString(b) {
		return "cheap", "no game calls"
	}
	return "candidate", fmt.Sprintf("%d line%s run%s every frame", len(lines), sIf(len(lines) != 1), sIf(len(lines) == 1))
}

// ScanLua: every onUpdate handler in a Lua file, classified.
func ScanLua(text string) []LuaHandler {
	var out []LuaHandler
	for _, h := range reHandler.FindAllStringSubmatchIndex(text, -1) {
		depth, end := 1, -1
		luaTokens(text, h[1], func(w string, at int) bool {
			switch w {
			case "function", "if", "do", "repeat":
				depth++
			default:
				depth--
				if depth == 0 {
					end = at
					return false
				}
			}
			return true
		})
		if end < 0 {
			continue
		}
		params := text[h[2]:h[3]]
		kind, why := classifyLua(text[h[1]:end], params)
		out = append(out, LuaHandler{Line: strings.Count(text[:h[0]], "\n") + 1, Kind: kind, Why: why, Lines: strings.Count(text[h[1]:end], "\n")})
	}
	return out
}
