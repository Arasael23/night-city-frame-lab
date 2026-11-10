package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"framelab/internal/gamefs"
	"framelab/internal/platform"
)

// One recording session = HitchRecorder + the marker script are put into the build when the
// session starts and taken out again when it ends (or the next time Frame Lab starts after a crash).
// Normal play never has any of it in the game.

func hitchDir(game string) string  { return filepath.Join(game, "red4ext", "plugins", "HitchRecorder") }
func markerDir(game string) string { return filepath.Join(game, "r6", "scripts", "NightCityFrameLab") }

// overwriteParts: in an MO2 build, the copies of the session parts in MO2's overwrite folder: the
// recordings HitchRecorder wrote there, and its ini once the game rewrote it (SessionOnly), which
// would otherwise sit on top of the next session's ini. Frame Lab's own folders only (2.5).
func overwriteParts(game string) []string {
	ow := gamefs.Overwrite(game)
	if ow == "" {
		return nil
	}
	return []string{filepath.Join(ow, "red4ext", "plugins", "HitchRecorder"), filepath.Join(ow, "r6", "scripts", "NightCityFrameLab")}
}

// sessionIni turns the bundled HitchRecorder.ini into the session one:
// on, whole run recorded with the mark key marking parts (CaptureMode=2), one game run only (SessionOnly=1),
// REDscript and CET time per mod in the marked parts or not (Settings), the mark key from Settings (2.5).
// HitchRecorder's own marker key (Scroll Lock) is switched off when Settings uses that key for something else.
func sessionIni(src string, scripts bool, markKey, feltKey int) string {
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

// InstallSessionParts copies HitchRecorder (session settings) and the marker script into the build.
// Anything already there is removed first. The game must be closed.
func InstallSessionParts(comp Components, game string, scripts bool, markKey, feltKey int) (hitch, markers bool, err error) {
	if len(platform.FindGame()) > 0 {
		return false, false, errors.New("close the game first")
	}
	for _, d := range overwriteParts(game) {
		os.RemoveAll(d)
	}
	if comp.HitchPkg != "" {
		src := filepath.Join(comp.HitchPkg, "red4ext", "plugins", "HitchRecorder")
		files, _ := os.ReadDir(src)
		if len(files) == 0 {
			return false, false, errors.New("the bundled HitchRecorder is missing from components\\HitchRecorder")
		}
		dst := hitchDir(game)
		if err := os.RemoveAll(dst); err != nil {
			return false, false, fmt.Errorf("could not clear the old HitchRecorder folder: %w", err)
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return false, false, err
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			from, to := filepath.Join(src, f.Name()), filepath.Join(dst, f.Name())
			if strings.EqualFold(f.Name(), "HitchRecorder.ini") {
				b, err := os.ReadFile(from)
				if err != nil {
					return true, false, err
				}
				if err := os.WriteFile(to, []byte(sessionIni(string(b), scripts, markKey, feltKey)), 0o644); err != nil {
					return true, false, err
				}
				continue
			}
			if err := copyFile(from, to); err != nil {
				return true, false, err
			}
		}
		hitch = true
	}
	if comp.Markers != "" {
		dst := markerDir(game)
		os.RemoveAll(dst)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return hitch, false, err
		}
		src := comp.Markers
		if comp.Markers25 != "" && gameAtLeast25(filepath.Join(game, "bin", "x64", "Cyberpunk2077.exe")) {
			src = comp.Markers25 // 2.31's script would add a method 2.5 already has (redscript warns it may break at runtime)
		}
		if err := copyFile(src, filepath.Join(dst, "FrameLab.Markers.reds")); err != nil {
			return hitch, false, err
		}
		markers = true
	}
	return hitch, markers, nil
}

// RemoveSessionParts takes HitchRecorder and the marker script out of the build again.
func RemoveSessionParts(game string) error {
	if len(platform.FindGame()) > 0 {
		return errors.New("close the game first")
	}
	var errs []string
	for _, d := range append([]string{hitchDir(game), markerDir(game)}, overwriteParts(game)...) {
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
	for _, d := range []string{hitchDir(game), markerDir(game)} {
		if _, err := os.Stat(d); err == nil {
			return true
		}
	}
	return false
}
