package optimise

// Optimise stage 2 (2.5E): general fix rules, each one a small, local change taken from the hand edits
// that worked (see recipes.json) and checked on the downloaded-mods corpus. Every rule keeps the
// file's line count, so line numbers in logs and in other fixes stay right.
//
//	lua_throttle_call   one call inside a CET onUpdate handler runs N times a second instead of every frame
//	                    (the frame time it skipped is handed on when the call takes the frame time)
//	lua_hubguard        interactionUI.lua's ui.update() (a helper many CET mods copy) stops right away
//	                    while its dialog hub isn't on screen; only for versions of the function checked by hand
//	reds_arraycontains  a REDscript loop that looks for a value with Equals and sets a flag becomes one
//	                    ArrayContains call

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// RuleFix: one place a rule can change.
type RuleFix struct {
	Rule  string `json:"rule"`
	Line  int    `json:"line"`
	Title string `json:"title"` // the change in a few words
	What  string `json:"what"`  // what it does and why it is safe, in words
	Code  string `json:"code"`  // the line(s) as they are now (trimmed), so the player sees what changes
	State string `json:"state"` // "ready" or "done"
}

// ---------------------------------------------------------------- lua_throttle_call

var (
	reStmtCall = regexp.MustCompile(`^(\s*)((?:[A-Za-z_][\w]*)(?:[.:][A-Za-z_][\w]*)*)\s*\(([^()]*)\)\s*;?\s*$`)
	reWorthy   = regexp.MustCompile(`(?i)(update|tick|check|scan|refresh|process|poll|watch|run)\w*$`)
	// calls that look like they draw, move, sound or count something in step with frames, or that come in
	// begin/end pairs: slowing those down would show, so they are never offered
	reNotCall = regexp.MustCompile(`(?i)(begin|end|menu|^ui|\bui\.|hub|choice|photo|hud|camo|head|look|cam|light|acoust|sound|audio|height|ride|lean|aim|position|cadence|log|anim|pose|drift|vr|input|key|visual|effect|fx|particle|interpol|lerp|smooth|vehicle|physic|move|motion|fov|gaze|wind|trail|fade|overlay|esp\b|draw|render|kerenzikov|dilat|force|accel|lateral|blood|text|presentation|taunt|speech|pause)`)
)

// luaHandlerBodies: each onUpdate handler of a file: the line of "function(dt)" and its first param,
// and the lines of its body (1-based, inclusive).
func luaHandlerBodies(text string) (out []struct {
	line, from, to int
	dt             string
}) {
	lines := strings.Split(text, "\n")
	for _, h := range reHandler.FindAllStringSubmatchIndex(text, -1) {
		start := strings.Count(text[:h[0]], "\n") + 1
		params := strings.TrimSpace(text[h[2]:h[3]])
		dt := strings.TrimSpace(strings.Split(params, ",")[0])
		if dt == "..." {
			dt = ""
		}
		// the end of the handler: the matching "end" of its function, found with the block keywords
		depth, end := 1, 0 // inside the handler's function already
		luaTokens(text, h[1]-1, func(word string, at int) bool {
			switch word {
			case "function", "if", "do", "repeat":
				depth++
			case "end", "until":
				depth--
				if depth == 0 {
					end = strings.Count(text[:at], "\n") + 1
					return false
				}
			}
			return true
		})
		if end == 0 || end > len(lines) {
			continue
		}
		out = append(out, struct {
			line, from, to int
			dt             string
		}{start, start + 1, end - 1, dt})
	}
	return out
}

// throttleCalls lists the calls inside onUpdate handlers that the rule can slow down.
func throttleCalls(text string) []RuleFix {
	lines := strings.Split(text, "\n")
	var out []RuleFix
	for _, h := range luaHandlerBodies(text) {
		if h.dt == "" {
			continue // a handler without a frame-time parameter: the whole-handler fix is the one to use
		}
		depth := 0
		for ln := h.from; ln <= h.to && ln <= len(lines); ln++ {
			l := strings.TrimRight(lines[ln-1], "\r")
			t := strings.TrimSpace(l)
			if strings.Contains(l, "Frame Lab:") {
				out = append(out, RuleFix{Rule: "lua_throttle_call", Line: ln, Title: "already slowed down", Code: t, State: "done"})
				continue
			}
			// only statements at the handler's own level: a call inside an if/for may already be gated
			m := reStmtCall.FindStringSubmatch(l)
			if depth == 0 && m != nil {
				callee, args := m[2], m[3]
				short := callee[strings.LastIndexAny(callee, ".:")+1:]
				if reWorthy.MatchString(short) && !reNotCall.MatchString(callee) && !reUnsafe.MatchString(l) && !reCronOnly.MatchString(l) {
					out = append(out, RuleFix{Rule: "lua_throttle_call", Line: ln, State: "ready", Code: t,
						Title: callee + " 10 times a second",
						What: "This call runs every frame inside the mod's onUpdate. Slowed down it runs 10 times a second (or 5 or 20); " +
							"the rest of the handler still runs every frame" + map[bool]string{true: ", and the call still gets all the frame time that passed", false: ""}[strings.Contains(args, h.dt)] +
							". If something of that mod looks jerky afterwards, undo it."})
				}
			}
			// block depth inside the handler, for the next line
			for _, w := range regexp.MustCompile(`\b(function|if|do|repeat|end|until)\b`).FindAllString(stripLuaStrings(t), -1) {
				switch w {
				case "function", "if", "do", "repeat":
					depth++
				case "end", "until":
					depth--
				}
			}
			if depth < 0 {
				depth = 0
			}
		}
	}
	return out
}

// stripLuaStrings blanks "..." and '...' and a trailing -- comment on one line
func stripLuaStrings(l string) string {
	b := []byte(l)
	for i := 0; i < len(b); i++ {
		if b[i] == '-' && i+1 < len(b) && b[i+1] == '-' {
			return string(b[:i])
		}
		if b[i] == '"' || b[i] == '\'' {
			q := b[i]
			for j := i + 1; j < len(b); j++ {
				if b[j] == '\\' {
					j++
					continue
				}
				if b[j] == q {
					for k := i; k <= j; k++ {
						b[k] = ' '
					}
					i = j
					break
				}
			}
		}
	}
	return string(b)
}

// ThrottleCall slows the call on `line` (inside an onUpdate handler) down to hz times a second.
// The line stays one line:
//
//	__fl_c57 = (__fl_c57 or 0) + (dt or 0) if __fl_c57 >= 0.0995 then local __fl_d = __fl_c57 __fl_c57 = 0 world.update(__fl_d) end --[[ Frame Lab: 10 times a second ]]
//
// __fl_c57 is a global of the mod's own Lua environment (CET gives each mod its own), so the file gets
// no new local or upvalue (some large files are at Lua's limit; see the NCPD Hotline hand edit).
func ThrottleCall(text string, line, hz int) (string, error) {
	if hz < 1 || hz > 60 {
		return "", errors.New("pick 5, 10 or 20 times a second")
	}
	ok := false
	for _, f := range throttleCalls(text) {
		if f.Line == line && f.State == "ready" {
			ok = true
		}
	}
	if !ok {
		return "", fmt.Errorf("line %d is not a call this fix can slow down", line)
	}
	var dt string
	for _, h := range luaHandlerBodies(text) {
		if line >= h.from && line <= h.to {
			dt = h.dt
		}
	}
	lines := strings.Split(text, "\n")
	l := lines[line-1]
	cr := ""
	if strings.HasSuffix(l, "\r") {
		cr, l = "\r", strings.TrimSuffix(l, "\r")
	}
	m := reStmtCall.FindStringSubmatch(l)
	indent, callee, args := m[1], m[2], m[3]
	v := "__fl_c" + strconv.Itoa(line)
	newArgs := regexp.MustCompile(`(^|[^.:\w])`+regexp.QuoteMeta(dt)+`\b`).ReplaceAllString(args, "${1}__fl_d")
	iv := strconv.FormatFloat(1/float64(hz)-0.0005, 'f', -1, 64)
	lines[line-1] = fmt.Sprintf("%s%s = (%s or 0) + (%s or 0) if %s >= %s then local __fl_d = %s %s = 0 %s(%s) end --[[ Frame Lab: %d times a second ]]%s",
		indent, v, v, dt, v, iv, v, v, callee, newArgs, hz, cr)
	return strings.Join(lines, "\n"), nil
}

// ---------------------------------------------------------------- lua_hubguard

// hubBodies: interactionUI.lua's ui.update() bodies (comments and blank lines left out, lines trimmed)
// that were read one by one: while the hub isn't shown they only fetch the game's choice hubs and set
// ui.input = false, so stopping right away changes nothing but the cost. Made from the 45 copies in
// the corpus (29 of them are one of these).
var hubBodies = map[string]bool{
	"c46865ab047e351a": true, // the common one (24 copies: Improved Aldecaldos Camp, the gift mods, IVDE, Romance Voicemail ...)
	"79e2623e7ae6cea5": true, // Rent a Motel / Rent a Hotel
	"2ad596fa8480197d": true, // CyberTrials (checks the player first)
	"d02b790f98715e0c": true, // Night City Salvage
	"e7152b7a1dcc11a8": true, // pachinko (has its own timer first)
	// left out: QuestRunner / CookSomething, LetsGoBowling / NightCityArmored and Cyberscript only work inside
	// "if ui.hubShown" already (nothing to gain); Hotscenes and NightCityJobFramework already stop early
}

// HubBodyKey: the key of a ui.update() body in hubBodies.
func HubBodyKey(body string) string {
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		l = strings.TrimSpace(stripLuaStrings(l))
		if l != "" {
			keep = append(keep, l)
		}
	}
	h := sha256.Sum256([]byte(strings.Join(keep, "\n")))
	return hex.EncodeToString(h[:8])
}

var reUIUpdate = regexp.MustCompile(`(?m)^function ui\.update\(\)[^\n]*\n`)

const hubGuard = "if not ui.hubShown then ui.input = false return end --[[ Frame Lab: nothing below works unless its hub is on screen ]]"

// hubGuardFix: the fix for one interactionUI.lua, if it is a known version
func hubGuardFix(text string) []RuleFix {
	loc := reUIUpdate.FindStringIndex(text)
	if loc == nil || !strings.Contains(text, "ui.hubShown") {
		return nil
	}
	end := strings.Index(text[loc[1]:], "\nend")
	if end < 0 {
		return nil
	}
	body := text[loc[1] : loc[1]+end]
	line := strings.Count(text[:loc[0]], "\n") + 1
	first := strings.TrimSpace(strings.SplitN(strings.TrimLeft(body, " \t\r\n"), "\n", 2)[0])
	if strings.HasPrefix(first, "if not ui.hubShown") || strings.Contains(text[loc[0]:loc[1]], "Frame Lab") {
		return []RuleFix{{Rule: "lua_hubguard", Line: line, Title: "already stops while its hub is hidden", State: "done", Code: "function ui.update()"}}
	}
	if !hubBodies[HubBodyKey(body)] {
		return nil
	}
	return []RuleFix{{Rule: "lua_hubguard", Line: line, State: "ready", Code: "function ui.update()",
		Title: "dialog helper only while its hub is on screen",
		What: "This copy of the interactionUI helper asks the game for its dialog choice hubs every frame, even when none of the mod's choices is on screen. " +
			"With the fix it stops right away until one is; what it does while one is on screen stays the same."}}
}

// HubGuard puts the guard in, on the function's own line (before any comment there), so the line count
// stays the same.
func HubGuard(text string) (string, error) {
	fx := hubGuardFix(text)
	if len(fx) == 0 || fx[0].State != "ready" {
		return "", errors.New("this file isn't a known version of the interactionUI helper")
	}
	loc := reUIUpdate.FindStringIndex(text)
	i := loc[0] + len("function ui.update()") // before any comment on that line
	return text[:i] + " " + hubGuard + text[i:], nil
}

// ---------------------------------------------------------------- reds_arraycontains

// for x in arr { if Equals(x, y) { flag = true; break; } }   (or Equals(y, x))
var reEqLoop = regexp.MustCompile(`for\s+(\w+)\s+in\s+([\w.\[\]]+)\s*\{\s*if\s+Equals\(\s*(\w+(?:\.\w+)*)\s*,\s*(\w+(?:\.\w+)*)\s*\)\s*\{\s*(\w+(?:\.\w+)*)\s*=\s*true\s*;\s*break\s*;\s*\}\s*\}`)

// arrayContainsFixes: the Equals loops of one .reds file (in masked text, so comments don't match)
func arrayContainsFixes(src string) []RuleFix {
	m := redsMask(src)
	var out []RuleFix
	for _, x := range reEqLoop.FindAllStringSubmatchIndex(m, -1) {
		v, arr, a, b := m[x[2]:x[3]], m[x[4]:x[5]], m[x[6]:x[7]], m[x[8]:x[9]]
		if a != v && b != v {
			continue
		}
		if a == v && b == v {
			continue
		}
		out = append(out, RuleFix{Rule: "reds_arraycontains", Line: strings.Count(src[:x[0]], "\n") + 1, State: "ready",
			Code:  strings.Join(strings.Fields(src[x[0]:x[1]]), " "),
			Title: "one ArrayContains instead of a loop over " + arr,
			What:  "The script walks the whole list " + arr + " comparing each entry, in REDscript (slow). ArrayContains does the same lookup in the game's own code. The result is the same."})
	}
	return out
}

// ArrayContains rewrites the loop that starts on `line`. The new code keeps the loop's line count
// (blank lines fill up), so line numbers after it stay the same.
func ArrayContains(src string, line int) (string, error) {
	m := redsMask(src)
	for _, x := range reEqLoop.FindAllStringSubmatchIndex(m, -1) {
		if strings.Count(src[:x[0]], "\n")+1 != line {
			continue
		}
		v, arr, a, b, flag := m[x[2]:x[3]], m[x[4]:x[5]], m[x[6]:x[7]], m[x[8]:x[9]], m[x[10]:x[11]]
		want := b
		if b == v {
			want = a
		}
		if want == v {
			break
		}
		repl := fmt.Sprintf("if ArrayContains(%s, %s) { %s = true; } // Frame Lab: was a loop with Equals", arr, want, flag)
		repl += strings.Repeat("\n", strings.Count(src[x[0]:x[1]], "\n"))
		return src[:x[0]] + repl + src[x[1]:], nil
	}
	return "", fmt.Errorf("no loop this fix can change on line %d", line)
}

// ---------------------------------------------------------------- all rules of one file

// RuleFixes: what the general rules can change in one file (path decides Lua or REDscript).
func RuleFixes(path, text string) []RuleFix {
	low := strings.ToLower(path)
	switch {
	case strings.HasSuffix(low, "interactionui.lua"):
		return append(hubGuardFix(text), throttleCalls(text)...)
	case strings.HasSuffix(low, ".lua"):
		return throttleCalls(text)
	case strings.HasSuffix(low, ".reds"):
		return arrayContainsFixes(text)
	}
	return nil
}

// ApplyRule makes one rule's change. hz only counts for lua_throttle (whole handler) and lua_throttle_call.
func ApplyRule(text, rule string, line, hz int) (string, error) {
	switch rule {
	case "lua_throttle":
		return ThrottleLua(text, line, hz)
	case "lua_throttle_call":
		return ThrottleCall(text, line, hz)
	case "lua_hubguard":
		return HubGuard(text)
	case "reds_arraycontains":
		return ArrayContains(text, line)
	}
	return "", errors.New("unknown fix")
}
