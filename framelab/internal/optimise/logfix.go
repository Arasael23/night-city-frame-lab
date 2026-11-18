package optimise

// Optimise stage 4 (2.5E): errors in the frameworks' logs turned into named fixes.
//
//	xl_cooked_type  ArchiveXL: "The target node #N has type worldCookedStaticMeshNode, but the mod expects
//	                worldStaticMeshNode". Game 2.5 cooked many static meshes; the mod's world edit names the
//	                old type, so ArchiveXL refuses the whole edit. Fix: that one "type:" line gets the cooked
//	                type (what we did by hand for Apartment Toys, where the node positions were checked).
//	file_off        any mod file a log names in an error (a world edit .xl, a TweakXL .yaml, a REDscript
//	                .reds that doesn't compile) can be switched off: Frame Lab moves it into its own data
//	                folder and Undo puts it back. A REDscript file that doesn't compile stops every script
//	                mod, so switching that one file off brings the others back.

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"framelab/internal/conflicts"
	"framelab/internal/gamefs"
)

// LogFix: one fix for one log line.
type LogFix struct {
	Log   string `json:"log"`             // the log it is from (RED4ext, ArchiveXL, REDscript ...)
	Text  string `json:"text"`            // the line
	Kind  string `json:"kind"`            // xl_cooked_type or file_off
	File  string `json:"file"`            // the mod file, relative to the game folder
	Index int    `json:"index,omitempty"` // xl_cooked_type: the node
	Title string `json:"title"`
	What  string `json:"what"`
}

var (
	reXlLine     = regexp.MustCompile(`^([^:\\/]+\.(?:archive\.xl|xl)):\s*(.*)$`)
	reCookedNode = regexp.MustCompile(`The target node #(\d+) has type worldCookedStaticMeshNode, but the mod expects worldStaticMeshNode`)
	reRedsAt     = regexp.MustCompile(`(?i)([A-Za-z]:)?[^\s"']*?[\\/]r6[\\/]scripts[\\/][^\s"':]+\.reds`)
	reYamlName   = regexp.MustCompile(`(?i)([\w .()\[\]-]+\.ya?ml)`)
)

// findModFile: a file the build has under one of the folders, by its file name (first match).
func findModFile(game, name string, dirs ...string) string {
	found := ""
	for _, d := range dirs {
		root := filepath.Join(game, filepath.FromSlash(d))
		gamefs.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil || found != "" {
				return filepath.SkipDir
			}
			if !e.IsDir() && strings.EqualFold(e.Name(), name) {
				if rel, err := filepath.Rel(game, p); err == nil {
					found = filepath.ToSlash(rel)
				}
				return filepath.SkipDir
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// xlTypeLines: the "type: worldStaticMeshNode" lines that belong to the list entry with "index: n" in a
// .xl (1-based line numbers). An entry runs from its "- " line to the next "- " line at the same indent.
func xlTypeLines(text string, n int) []int {
	lines := strings.Split(text, "\n")
	reIdx := regexp.MustCompile(`^\s*-?\s*index:\s*` + strconv.Itoa(n) + `\s*(#.*)?$`) // YAML comments are fine
	reType := regexp.MustCompile(`^-?\s*type:\s*worldStaticMeshNode\s*(#.*)?$`)
	dashAt := func(l string) int { // indent of a "- " list line, or -1
		t := strings.TrimRight(l, "\r")
		k := len(t) - len(strings.TrimLeft(t, " "))
		if strings.HasPrefix(t[k:], "- ") || t[k:] == "-" {
			return k
		}
		return -1
	}
	var out []int
	for i, l := range lines {
		if !reIdx.MatchString(strings.TrimRight(l, "\r")) {
			continue
		}
		start := i // the entry's "- " line
		for start >= 0 && dashAt(lines[start]) < 0 {
			start--
		}
		if start < 0 {
			continue
		}
		dash := dashAt(lines[start])
		for j := start; j < len(lines); j++ {
			if j > start {
				if d := dashAt(lines[j]); d >= 0 && d <= dash {
					break // the next entry
				}
				t := strings.TrimRight(lines[j], "\r")
				if strings.TrimSpace(t) != "" && len(t)-len(strings.TrimLeft(t, " ")) <= dash {
					break // out of the list
				}
			}
			if reType.MatchString(strings.TrimSpace(strings.TrimRight(lines[j], "\r"))) {
				out = append(out, j+1)
			}
		}
	}
	return out
}

// LogFixes: the fixes for the errors in a build's logs (from the conflict check).
func LogFixes(game string, logs []conflicts.LogSource) []LogFix {
	var out []LogFix
	seen := map[string]bool{}
	add := func(f LogFix) {
		k := f.Kind + "|" + f.File + "|" + strconv.Itoa(f.Index)
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	for _, src := range logs {
		for _, l := range src.Lines {
			if l.Level != "error" {
				continue
			}
			// ArchiveXL world edits: "<file>.xl: ..."
			if m := reXlLine.FindStringSubmatch(l.Text); m != nil {
				rel := findModFile(game, m[1], "archive/pc/mod", "mods")
				if rel == "" {
					continue
				}
				if c := reCookedNode.FindStringSubmatch(m[2]); c != nil {
					n, _ := strconv.Atoi(c[1])
					if text, err := gamefs.ReadFile(filepath.Join(game, filepath.FromSlash(rel))); err == nil && len(xlTypeLines(string(text), n)) == 1 {
						add(LogFix{Log: src.Name, Text: l.Text, Kind: "xl_cooked_type", File: rel, Index: n,
							Title: "node #" + c[1] + ": worldCookedStaticMeshNode",
							What: "Game 2.5 cooked this mesh, so ArchiveXL refuses the mod's whole edit of that part of the city. The fix changes the one type line in " + m[1] +
								". It is most likely the same object (as with Apartment Toys); check in game that the right thing is gone."})
					}
				}
				add(LogFix{Log: src.Name, Text: l.Text, Kind: "file_off", File: rel, Title: "switch " + m[1] + " off",
					What: "Only this world edit stops; the rest of the mod (its archive) stays. Useful when the edit hits the wrong objects after a game update."})
				continue
			}
			// REDscript: "At C:\...\r6\scripts\Mod\file.reds:12:3"
			if src.Name == "REDscript" {
				if p := reRedsAt.FindString(l.Text); p != "" {
					low := strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
					if i := strings.Index(low, "r6/scripts/"); i >= 0 {
						rel := strings.ReplaceAll(p, "\\", "/")[i:]
						if gamefs.Exists(filepath.Join(game, filepath.FromSlash(rel))) {
							add(LogFix{Log: src.Name, Text: l.Text, Kind: "file_off", File: rel, Title: "switch " + filepath.Base(rel) + " off",
								What: "This script doesn't compile, and while one script fails the game runs without any mod scripts. Switched off, the others work again; this mod (or the part of it in this file) doesn't. An update of the mod is the real fix."})
						}
					}
				}
				continue
			}
			// TweakXL: a .yaml named in the error
			if strings.EqualFold(src.Name, "TweakXL") {
				if m := reYamlName.FindStringSubmatch(l.Text); m != nil {
					if rel := findModFile(game, strings.TrimSpace(m[1]), "r6/tweaks"); rel != "" {
						add(LogFix{Log: src.Name, Text: l.Text, Kind: "file_off", File: rel, Title: "switch " + filepath.Base(rel) + " off",
							What: "TweakXL couldn't use this file. Switched off, the error goes; what the file changed is gone too."})
					}
				}
			}
		}
	}
	return out
}

// CookedType changes the one "type: worldStaticMeshNode" line of entry n (line count stays the same).
func CookedType(text string, n int) (string, bool) {
	ls := xlTypeLines(text, n)
	if len(ls) != 1 {
		return "", false
	}
	lines := strings.Split(text, "\n")
	lines[ls[0]-1] = strings.Replace(lines[ls[0]-1], "worldStaticMeshNode", "worldCookedStaticMeshNode", 1)
	return strings.Join(lines, "\n"), true
}
