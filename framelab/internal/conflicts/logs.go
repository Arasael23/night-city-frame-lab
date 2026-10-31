package conflicts

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The last log of each framework and plugin: RED4ext and the plugins that log next to it or in their
// own folder (ArchiveXL, TweakXL, Codeware, ...), REDscript's compile log and CET with each CET mod.
// Only lines marked as errors or warnings are kept, identical lines counted once.

var (
	reErr      = regexp.MustCompile(`(?i)\[\s*(error|err|critical|fatal)\s*\]|^\[ERROR\b|\[ERROR -`)
	reWarn     = regexp.MustCompile(`(?i)\[\s*(warn|warning)\s*\]|^\[WARN\b|\[WARN -`)
	reLead     = regexp.MustCompile(`^(\s*\[[^\]]*\]\s*)+`)
	reDatedLog = regexp.MustCompile(`^(.+?)[-_]r?\d{4}-\d{2}-\d{2}[-_]\d{2}-\d{2}-\d{2}\.log$`)
)

func newestLog(dir string, match func(name string) bool) (string, os.FileInfo) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	var best string
	var bi os.FileInfo
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".log") || (match != nil && !match(e.Name())) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if bi == nil || fi.ModTime().After(bi.ModTime()) {
			best, bi = filepath.Join(dir, e.Name()), fi
		}
	}
	return best, bi
}

func readLog(name, path string, fi os.FileInfo, game string) *LogSource {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	rel, _ := filepath.Rel(game, path)
	src := &LogSource{Name: name, File: filepath.ToSlash(rel), When: fi.ModTime()}
	count := map[string]*LogLine{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		lvl := ""
		switch {
		case reErr.MatchString(line):
			lvl = "error"
		case reWarn.MatchString(line):
			lvl = "warning"
		default:
			continue
		}
		text := strings.TrimSpace(reLead.ReplaceAllString(line, ""))
		if text == "" {
			text = strings.TrimSpace(line)
		}
		if len(text) > 400 {
			text = text[:400] + " ..."
		}
		k := lvl + "\x00" + text
		if l := count[k]; l != nil {
			l.Count++
		} else {
			count[k] = &LogLine{Level: lvl, Text: text, Count: 1}
		}
		if lvl == "error" {
			src.Errors++
		} else {
			src.Warnings++
		}
	}
	for _, l := range count {
		src.Lines = append(src.Lines, *l)
	}
	sort.Slice(src.Lines, func(i, j int) bool {
		a, b := src.Lines[i], src.Lines[j]
		if a.Level != b.Level {
			return a.Level == "error"
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Text < b.Text
	})
	if len(src.Lines) > 40 {
		src.Lines = src.Lines[:40]
	}
	return src
}

func checkLogs(r *Report, game string) {
	add := func(name, path string, fi os.FileInfo) {
		if path == "" || fi == nil {
			return
		}
		if s := readLog(name, path, fi, game); s != nil {
			r.Logs = append(r.Logs, *s)
		}
	}
	// RED4ext and plugins that log into red4ext/logs as <name>-<date>.log
	logDir := filepath.Join(game, "red4ext", "logs")
	groups := map[string]bool{}
	if ents, err := os.ReadDir(logDir); err == nil {
		for _, e := range ents {
			if m := reDatedLog.FindStringSubmatch(e.Name()); m != nil {
				groups[m[1]] = true
			}
		}
	}
	for g := range groups {
		pre := g
		p, fi := newestLog(logDir, func(n string) bool {
			m := reDatedLog.FindStringSubmatch(n)
			return m != nil && m[1] == pre
		})
		name := g
		if strings.EqualFold(g, "red4ext") {
			name = "RED4ext"
		}
		add(name, p, fi)
	}
	// plugins with logs in their own folder
	plugDir := filepath.Join(game, "red4ext", "plugins")
	if ents, err := os.ReadDir(plugDir); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				p, fi := newestLog(filepath.Join(plugDir, e.Name()), nil)
				add(e.Name(), p, fi)
			}
		}
	}
	// REDscript
	if p := filepath.Join(game, "r6", "logs", "redscript_rCURRENT.log"); fileExists(p) {
		fi, _ := os.Stat(p)
		add("REDscript", p, fi)
	}
	// CET and its mods
	cet := filepath.Join(game, "bin", "x64", "plugins", "cyber_engine_tweaks")
	for _, n := range []string{"cyber_engine_tweaks.log", "scripting.log"} {
		if p := filepath.Join(cet, n); fileExists(p) {
			fi, _ := os.Stat(p)
			add("CET ("+strings.TrimSuffix(n, ".log")+")", p, fi)
		}
	}
	if ents, err := os.ReadDir(filepath.Join(cet, "mods")); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				p, fi := newestLog(filepath.Join(cet, "mods", e.Name()), nil)
				add("CET mod "+e.Name(), p, fi)
			}
		}
	}
	sort.Slice(r.Logs, func(i, j int) bool {
		a, b := r.Logs[i], r.Logs[j]
		if a.Errors != b.Errors {
			return a.Errors > b.Errors
		}
		if a.Warnings != b.Warnings {
			return a.Warnings > b.Warnings
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
