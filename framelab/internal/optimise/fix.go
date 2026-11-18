package optimise

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Optimise step 2 (2.5): fixes Frame Lab makes on this PC only. Each one is small, saved with the
// original file next to it, and can be undone; nothing is ever uploaded or shared.

// ThrottleHz: how often a slowed-down onUpdate handler still runs.
var ThrottleHz = []int{5, 10, 20}

// ThrottleLua slows the CET onUpdate handler that starts on `line` down to `hz` times a second.
// Only handlers the scan calls a candidate are touched (game-state checks, nothing that moves the
// camera, vehicles, sound or the UI). The change is one run of code put right after the handler's
// "function(dt)" on the same line, so every line number in the file stays the same:
//
//	function(dt) --[[ Frame Lab: 10 times a second ]] __fl_acc = __fl_acc or {} local __fl_t = (__fl_acc[57] or 0) + (dt or 0) if __fl_t < 0.0995 then __fl_acc[57] = __fl_t return end __fl_acc[57] = 0 dt = __fl_t
//
// The handler then gets the time since it last ran as its dt, so mods that count time keep counting
// right. __fl_acc is a table in the mod's own Lua environment (CET gives each mod its own).
func ThrottleLua(text string, line, hz int) (string, error) {
	if hz < 1 || hz > 60 {
		return "", errors.New("pick 5, 10 or 20 times a second")
	}
	for _, h := range reHandler.FindAllStringSubmatchIndex(text, -1) {
		if strings.Count(text[:h[0]], "\n")+1 != line {
			continue
		}
		var kind string
		for _, x := range ScanLua(text) {
			if x.Line == line {
				kind = x.Kind
			}
		}
		switch kind {
		case "candidate":
		case "done":
			return "", errors.New("this onUpdate is already slowed down")
		case "":
			return "", fmt.Errorf("no onUpdate handler found on line %d", line)
		default:
			return "", fmt.Errorf("this onUpdate is not one to slow down (%s)", kind)
		}
		params := strings.TrimSpace(text[h[2]:h[3]])
		dt := strings.TrimSpace(strings.Split(params, ",")[0])
		newParams := params
		switch {
		case dt == "":
			dt, newParams = "__fl_dt", "__fl_dt"
		case dt == "...":
			dt, newParams = "__fl_dt", "__fl_dt, "+params
		}
		k := strconv.Itoa(line)
		iv := strconv.FormatFloat(1/float64(hz)-0.0005, 'f', -1, 64) // a hair under: 12 frames of 1/120 s add up to 0.0999...
		inject := fmt.Sprintf(" --[[ Frame Lab: %d times a second ]] __fl_acc = __fl_acc or {} local __fl_t = (__fl_acc[%s] or 0) + (%s or 0) if __fl_t < %s then __fl_acc[%s] = __fl_t return end __fl_acc[%s] = 0 %s = __fl_t",
			hz, k, dt, iv, k, k, dt)
		closeAt := h[1] // just after "function(params)"
		head := text[:h[2]] + newParams + text[h[3]:closeAt]
		return head + inject + text[closeAt:], nil
	}
	return "", fmt.Errorf("no onUpdate handler found on line %d", line)
}
