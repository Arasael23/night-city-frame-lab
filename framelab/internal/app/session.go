package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"framelab/internal/analysis"
	"framelab/internal/gamefs"
	"framelab/internal/platform"
)

// One recording session = HitchRecorder + the marker script are put into the build when the
// session starts and taken out again when it ends (or the next time Frame Lab starts after a crash).
// Normal play never has any of it in the game.

func hitchDir(game string) string  { return filepath.Join(game, "red4ext", "plugins", "HitchRecorder") }
func markerDir(game string) string { return filepath.Join(game, "r6", "scripts", "NightCityFrameLab") }

// markerLuaDir: the session markers as a CET mod (game 2.5 and later). It writes markers.log in there.
func markerLuaDir(game string) string {
	return filepath.Join(game, "bin", "x64", "plugins", "cyber_engine_tweaks", "mods", "NightCityFrameLab")
}

const (
	markerLuaLog    = "markers.log"
	markerLuaRoutes = "routes.txt" // a test recording: the routes still to record, for the line on screen
)

// routesFile: the routes of a test recording as the markers mod reads them, one a line:
// name|seconds|foot, car or any|fight 0/1|places,..|pass through,..|places as text
func routesFile(routes []analysis.RouteDef) string {
	clean := func(s string) string { return strings.NewReplacer("|", " ", ",", " ", "\r", " ", "\n", " ").Replace(s) }
	list := func(names []string) string {
		var out []string
		for _, n := range names {
			if c := strings.TrimSpace(clean(n)); c != "" {
				out = append(out, c)
			}
		}
		return strings.Join(out, ",")
	}
	var b strings.Builder
	for _, d := range routes {
		name := strings.ToLower(strings.TrimSpace(clean(d.Name)))
		name = strings.TrimPrefix(strings.TrimPrefix(name, "a "), "an ")
		mode, fight, text := d.Mode, "0", ""
		if mode == "" {
			mode = "any"
		}
		if d.Fight {
			fight = "1"
		}
		switch {
		case len(d.Pinned()) > 0:
			text = analysis.PlaceTitles(d.Where, "or")
		case len(d.Where) > 0:
			text = strings.Join(d.Where, " or ")
		}
		fmt.Fprintf(&b, "%s|%.0f|%s|%s|%s|%s|%s\n", name, d.Seconds, mode, fight, list(d.Where), list(d.Both), strings.TrimSpace(strings.ReplaceAll(clean(text), "  ", " ")))
	}
	return b.String()
}

// sessionDirs: every folder Frame Lab puts into a build for a session
func sessionDirs(game string) []string {
	return []string{hitchDir(game), markerDir(game), markerLuaDir(game)}
}

// overwriteParts: in an MO2 build, the copies of the session parts in MO2's overwrite folder: the
// recordings HitchRecorder wrote there, and its ini once the game rewrote it (SessionOnly), which
// would otherwise sit on top of the next session's ini. Frame Lab's own folders only (2.5).
func overwriteParts(game string) []string {
	ow := gamefs.Overwrite(game)
	if ow == "" {
		return nil
	}
	return []string{filepath.Join(ow, "red4ext", "plugins", "HitchRecorder"), filepath.Join(ow, "r6", "scripts", "NightCityFrameLab"),
		filepath.Join(ow, "bin", "x64", "plugins", "cyber_engine_tweaks", "mods", "NightCityFrameLab")}
}

// sessionIni turns the bundled HitchRecorder.ini into the session one:
// on, whole run recorded with the mark key marking parts (CaptureMode=2), one game run only (SessionOnly=1),
// REDscript and CET time per mod in the marked parts or not (Settings), the mark key from Settings (2.5).
// HitchRecorder's own marker key (Scroll Lock) is switched off when Settings uses that key for something else.
// whole: a test recording (Optimise schedule), where script time is measured for the whole run.
func sessionIni(src string, scripts, whole bool, markKey, feltKey int) string {
	set := func(s, key, val string) string {
		re := regexp.MustCompile(`(?m)^` + key + `=.*$`)
		if re.MatchString(s) {
			return re.ReplaceAllString(s, key+"="+val)
		}
		return strings.Replace(s, "[Recorder]", "[Recorder]\r\n"+key+"="+val, 1)
	}
	s := set(src, "Enabled", "1")
	s = set(s, "CaptureMode", "2")
	s = set(s, "SessionOnly", "1")
	on := "0"
	if scripts {
		on = "1"
	}
	s = set(s, "RecordScripts", on)
	s = set(s, "RecordCET", on)
	all := "0"
	if whole && scripts {
		all = "1"
	}
	s = set(s, "ScriptsWholeRun", all)
	if markKey == 0 {
		markKey = DefaultMarkKey
	}
	s = set(s, "CaptureKey", strconv.Itoa(markKey))
	if own := regexp.MustCompile(`(?m)^MarkKey=\s*(\d+)`).FindStringSubmatch(s); own != nil {
		if v, _ := strconv.Atoi(own[1]); v != 0 && (v == markKey || v == feltKey) {
			s = set(s, "MarkKey", "0")
		}
	}
	return s
}

// Markers kinds: how the game's markers reach Frame Lab in a session.
const (
	MarkersNone = ""
	MarkersLua  = "lua"  // Frame Lab's CET mod and its markers.log (game 2.5 and later)
	MarkersReds = "reds" // the session script and CET's gamelog.log
)

// InstallSessionParts copies HitchRecorder (session settings) and the markers into the build.
// Anything already there is removed first. The game must be closed.
// routes: the test routes still to record. With any, this is a test recording: script time is measured
// for the whole run, and the markers mod shows how far each route is.
// opts: the game options the markers mod reads once, and the one on trial in this recording (CET mod only).
func InstallSessionParts(comp Components, game string, scripts bool, markKey, feltKey int, routes []analysis.RouteDef, opts SessionOpts) (hitch bool, markers string, err error) {
	test := len(routes) > 0
	if len(platform.FindGame()) > 0 {
		return false, MarkersNone, errors.New("close the game first")
	}
	for _, d := range overwriteParts(game) {
		os.RemoveAll(d)
	}
	if comp.HitchPkg != "" {
		src := filepath.Join(comp.HitchPkg, "red4ext", "plugins", "HitchRecorder")
		files, _ := os.ReadDir(src)
		if len(files) == 0 {
			return false, MarkersNone, errors.New("the bundled HitchRecorder is missing from components\\HitchRecorder")
		}
		dst := hitchDir(game)
		if err := os.RemoveAll(dst); err != nil {
			return false, MarkersNone, fmt.Errorf("could not clear the old HitchRecorder folder: %w", err)
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return false, MarkersNone, err
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			from, to := filepath.Join(src, f.Name()), filepath.Join(dst, f.Name())
			if strings.EqualFold(f.Name(), "HitchRecorder.ini") {
				b, err := os.ReadFile(from)
				if err != nil {
					return true, MarkersNone, err
				}
				if err := os.WriteFile(to, []byte(sessionIni(string(b), scripts, test, markKey, feltKey)), 0o644); err != nil {
					return true, MarkersNone, err
				}
				continue
			}
			if err := copyFile(from, to); err != nil {
				return true, MarkersNone, err
			}
		}
		hitch = true
	}
	os.RemoveAll(markerDir(game))
	os.RemoveAll(markerLuaDir(game))
	is25 := gameAtLeast25(filepath.Join(game, "bin", "x64", "Cyberpunk2077.exe"))
	// game 2.5 and later with CET: the markers are a CET mod that writes its own log line by line
	// (nothing is added to the game's script compile, and nothing is lost when the game closes badly)
	if comp.MarkersLua != "" && is25 && HasCET(game) {
		dst := markerLuaDir(game)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return hitch, MarkersNone, err
		}
		if err := copyFile(filepath.Join(comp.MarkersLua, "init.lua"), filepath.Join(dst, "init.lua")); err != nil {
			os.RemoveAll(dst)
			return hitch, MarkersNone, err
		}
		if test {
			os.WriteFile(filepath.Join(dst, markerLuaRoutes), []byte(routesFile(routes)), 0o644)
		}
		if of := optionsFile(opts); of != "" {
			if err := os.WriteFile(filepath.Join(dst, markerLuaOptions), []byte(of), 0o644); err != nil && len(opts.Set) > 0 {
				os.RemoveAll(dst)
				return hitch, MarkersNone, fmt.Errorf("could not write the option on trial into the markers mod: %w", err)
			}
		}
		return hitch, MarkersLua, nil
	}
	if comp.Markers != "" {
		dst := markerDir(game)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return hitch, MarkersNone, err
		}
		src := comp.Markers
		if comp.Markers25 != "" && is25 {
			src = comp.Markers25 // 2.31's script would add a method 2.5 already has (redscript warns it may break at runtime)
		}
		if err := copyFile(src, filepath.Join(dst, "FrameLab.Markers.reds")); err != nil {
			return hitch, MarkersNone, err
		}
		markers = MarkersReds
	}
	return hitch, markers, nil
}

// RemoveSessionParts takes HitchRecorder and the marker script out of the build again.
func RemoveSessionParts(game string) error {
	if len(platform.FindGame()) > 0 {
		return errors.New("close the game first")
	}
	var errs []string
	for _, d := range append(sessionDirs(game), overwriteParts(game)...) {
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, err.Error())
		}
		if _, err := os.Stat(d); err == nil {
			errs = append(errs, d+" is still there")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// SessionPartsLeft reports whether HitchRecorder or the marker script is in a build.
func SessionPartsLeft(game string) bool {
	for _, d := range sessionDirs(game) {
		if _, err := os.Stat(d); err == nil {
			return true
		}
	}
	return false
}
