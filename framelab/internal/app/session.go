package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"framelab/internal/platform"
)

// One recording session = HitchRecorder + the marker script are put into the build when the
// session starts and taken out again when it ends (or the next time Frame Lab starts after a crash).
// Normal play never has any of it in the game.

func hitchDir(game string) string  { return filepath.Join(game, "red4ext", "plugins", "HitchRecorder") }
func markerDir(game string) string { return filepath.Join(game, "r6", "scripts", "NightCityFrameLab") }

// sessionIni turns the bundled HitchRecorder.ini into the session one:
// on, whole run recorded with F11 marking parts (CaptureMode=2), one game run only (SessionOnly=1),
// REDscript and CET time per mod in the F11 parts or not (Settings).
func sessionIni(src string, scripts bool) string {
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
	return s
}

// InstallSessionParts copies HitchRecorder (session settings) and the marker script into the build.
// Anything already there is removed first. The game must be closed.
func InstallSessionParts(comp Components, game string, scripts bool) (hitch, markers bool, err error) {
	if len(platform.FindGame()) > 0 {
		return false, false, errors.New("close the game first")
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
				if err := os.WriteFile(to, []byte(sessionIni(string(b), scripts)), 0o644); err != nil {
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
		if err := copyFile(comp.Markers, filepath.Join(dst, "FrameLab.Markers.reds")); err != nil {
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
	for _, d := range []string{hitchDir(game), markerDir(game)} {
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
