package optimise

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"framelab/internal/gamefs"
)

type checker struct {
	r       *Report
	o       Options
	files   []*file
	present map[string]bool
	mods    map[string]*Mod
	have    map[int]bool // frameworks installed (nexus id)
	byOwner map[string][]*file
}

func (c *checker) add(f Finding) { c.r.Findings = append(c.r.Findings, f) }

func (c *checker) nameOf(owner string) string {
	if m := c.mods[owner]; m != nil {
		return m.Name
	}
	return owner
}

func link(name string, id int) Link { return Link{Text: name, URL: NexusURL(id)} }

// FileVersion: the file version stamped into a Windows exe / dll / asi ("1.31.0"), or "".
func FileVersion(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sig := []byte{0xBD, 0x04, 0xEF, 0xFE}
	i := -1
	for from := 0; ; { // the same 4 bytes can occur in code: the real VS_FIXEDFILEINFO has struct version 1.0
		k := bytes.Index(b[from:], sig)
		if k < 0 {
			break
		}
		if j := from + k; j+16 <= len(b) && binary.LittleEndian.Uint32(b[j+4:]) == 0x10000 {
			i = j
		}
		from += k + 4
	}
	if i < 0 {
		return ""
	}
	ms := binary.LittleEndian.Uint32(b[i+8:])
	ls := binary.LittleEndian.Uint32(b[i+12:])
	v := fmt.Sprintf("%d.%d.%d", ms>>16, ms&0xFFFF, ls>>16)
	if ls&0xFFFF != 0 {
		v += fmt.Sprintf(".%d", ls&0xFFFF)
	}
	return v
}

// verLess: a < b for dotted numeric versions ("1.29.1" < "1.31.0"; a leading v and letters after a
// number don't count: "v10.1" is 10.1, "1.1.3b" is 1.1.3)
func verLess(a, b string) bool {
	num := func(p string) int {
		p = strings.TrimLeft(p, "vV")
		i := 0
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			i++
		}
		n, _ := strconv.Atoi(p[:i])
		return n
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = num(pa[i])
		}
		if i < len(pb) {
			y = num(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// ---- the game version against RED4ext
func (c *checker) gameVersion() {
	gv := c.o.GameVersion
	for _, fw := range c.r.Frameworks {
		if fw.Name == "RED4ext" && fw.Installed && fw.Version != "" && (gv == "2.5" || strings.HasPrefix(gv, "2.5")) && verLess(fw.Version, "1.31.0") {
			c.add(Finding{Severity: "high", Kind: "framework_outdated", Title: "RED4ext " + fw.Version + " is too old for game version " + gv,
				Detail: "Game 2.5 needs RED4ext 1.31 or newer. With an older one none of the RED4ext plugins load (ArchiveXL, TweakXL, Codeware and every mod that needs them).",
				Mods:   []string{"RED4ext"}, Links: []Link{link("RED4ext", 2380)}})
		}
	}
}

// ---- a file type needs a framework that isn't installed
func (c *checker) missingFrameworks() {
	need := map[string][]string{} // framework -> owners
	for _, f := range c.files {
		var fw string
		switch {
		case strings.HasSuffix(f.low, ".xl") && strings.HasPrefix(f.low, "archive/pc/mod/"):
			fw = "ArchiveXL"
		case f.kind == "TweakXL":
			fw = "TweakXL"
		case strings.HasSuffix(f.low, ".reds"): // not other files that ended up in r6/scripts (the misplaced check lists those)
			fw = "redscript"
		case f.kind == "RED4ext plugin" && strings.HasSuffix(f.low, ".dll"):
			fw = "RED4ext"
		case f.kind == "CET Lua" && strings.HasSuffix(f.low, "init.lua"):
			fw = "Cyber Engine Tweaks"
		case f.kind == "Input Loader":
			fw = "Input Loader"
		}
		if fw != "" {
			need[fw] = append(need[fw], c.nameOf(f.owner))
		}
	}
	// Codeware: scripts that import it
	for _, f := range c.files {
		if f.kind == "REDscript" && strings.HasSuffix(f.low, ".reds") && f.size < 2<<20 {
			if b, err := gamefs.ReadFile(filepath.Join(c.o.Game, filepath.FromSlash(f.rel))); err == nil && reImportCW.Match(b) {
				need["Codeware"] = append(need["Codeware"], c.nameOf(f.owner))
			}
		}
	}
	for i := range c.r.Frameworks {
		fw := &c.r.Frameworks[i]
		users := uniq(need[fw.Name])
		fw.Needed = len(need[fw.Name])
		if fw.Installed || len(users) == 0 {
			continue
		}
		c.add(Finding{Severity: "high", Kind: "framework_missing", Title: fw.Name + " isn't installed, but " + plural(len(users), "mod needs", "mods need") + " it",
			Detail: "Without it these mods don't work: " + list(users, 8) + ".", Mods: users, Links: []Link{link(fw.Name, fw.Nexus)}})
	}
	// REDmod mods need the REDmod DLC and a deploy
	var redmods []string
	for key, fs_ := range c.byOwner {
		if len(fs_) > 0 && fs_[0].kind == "REDmod" {
			redmods = append(redmods, c.nameOf(key))
		}
	}
	if len(redmods) > 0 && !gamefs.IsFile(filepath.Join(c.o.Game, "tools", "redmod", "bin", "redMod.exe")) {
		sort.Strings(redmods)
		c.add(Finding{Severity: "high", Kind: "framework_missing", Title: "REDmod mods without the REDmod DLC",
			Detail: plural(len(redmods), "mod is", "mods are") + " in the mods folder, but the free REDmod DLC isn't installed, so they never load: " + list(redmods, 8) + ".", Mods: redmods})
	}
}

var (
	reImportCW = regexp.MustCompile(`(?m)^\s*import\s+Codeware`)
	reModule   = regexp.MustCompile(`(?m)^\s*module\s+([\w.]+)`)
	reImport   = regexp.MustCompile(`(?m)^\s*import\s+(\w+(?:\.\w+)*)(\.\*|\.\{[^}]*\})?`)
	reSoftImp  = regexp.MustCompile(`@if\(ModuleExists\("([\w.]+)"\)\)`)
	reSoftLine = regexp.MustCompile(`@if\(ModuleExists\("[\w.]+"\)\)\s*\n\s*import[^\n]*`)
)

// ---- REDscript: a module a script imports that no installed script declares
func (c *checker) modules() {
	decl := map[string]bool{}
	type imp struct{ mod, owner string }
	var imps []imp
	for _, f := range c.files {
		if !strings.HasSuffix(f.low, ".reds") || f.size > 2<<20 {
			continue
		}
		b, err := gamefs.ReadFile(filepath.Join(c.o.Game, filepath.FromSlash(f.rel)))
		if err != nil {
			continue
		}
		for _, m := range reModule.FindAllSubmatch(b, -1) {
			decl[string(m[1])] = true
		}
		hard := reSoftLine.ReplaceAll(b, nil) // imports inside @if(ModuleExists(...)) are optional
		for _, m := range reImport.FindAllSubmatch(hard, -1) {
			mod := string(m[1])
			if len(m[2]) == 0 { // import Module.Name.Type: the module is everything before the type
				if i := strings.LastIndex(mod, "."); i > 0 {
					mod = mod[:i]
				}
			}
			imps = append(imps, imp{mod, f.owner})
		}
	}
	presentMod := func(mod string) bool {
		for d := range decl {
			if d == mod || strings.HasPrefix(d, mod+".") || strings.HasPrefix(mod, d+".") {
				return true
			}
		}
		return false
	}
	type miss struct {
		owners map[string]bool
	}
	missing := map[string]*miss{}
	for _, im := range imps {
		root := strings.Split(im.mod, ".")[0]
		if root == "Codeware" || !strings.Contains(im.mod, ".") && !c.providerKnown(im.mod) {
			continue // Codeware is the framework check; one-word imports are often game modules
		}
		if presentMod(im.mod) {
			continue
		}
		m := missing[im.mod]
		if m == nil {
			m = &miss{owners: map[string]bool{}}
			missing[im.mod] = m
		}
		m.owners[c.nameOf(im.owner)] = true
	}
	var keys []string
	for k := range missing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, mod := range keys {
		owners := keysOf(missing[mod].owners)
		f := Finding{Severity: "high", Kind: "module_missing", Title: list(owners, 3) + " needs the REDscript module " + mod + ", which no installed mod has",
			Detail: "The game can't compile its scripts like this, which switches off every REDscript mod.", Mods: owners}
		if ids := c.provider(mod); len(ids) > 0 {
			for _, id := range ids[:min(3, len(ids))] {
				f.Links = append(f.Links, link(c.o.Index.ModName(id), id))
			}
			f.Detail += " It comes from " + c.o.Index.ModName(ids[0]) + "."
		}
		c.add(f)
	}
}

func (c *checker) provider(mod string) []int {
	if c.o.Index == nil {
		return nil
	}
	for m := mod; m != ""; {
		if ids := c.o.Index.Modules[m]; len(ids) > 0 {
			return ids
		}
		i := strings.LastIndex(m, ".")
		if i < 0 {
			break
		}
		m = m[:i]
	}
	return nil
}

func (c *checker) providerKnown(mod string) bool { return len(c.provider(mod)) > 0 }

// ---- requirements listed on Nexus that aren't installed
func (c *checker) requirements() {
	x := c.o.Index
	if x == nil {
		return
	}
	installed := map[int]bool{}
	for _, m := range c.mods {
		if m.Nexus != 0 {
			installed[m.Nexus] = true
		}
	}
	for id := range c.have {
		installed[id] = true
	}
	// mods whose REDscript modules others import: whether those are really needed is what the module
	// check above answers (an import nothing provides breaks the compile), so the Nexus list isn't trusted for them
	hasModules := map[int]bool{}
	for _, ids := range x.Modules {
		for _, id := range ids {
			hasModules[id] = true
		}
	}
	// any file of a mod (any version) present counts as installed
	for _, f := range c.files {
		if id, ok := x.mpath[fnv64(f.low)]; ok {
			installed[id] = true
		}
	}
	type need struct {
		by    []string
		logic bool // at least one mod with code of its own needs it
	}
	missing := map[int]*need{}
	for _, m := range c.mods {
		if m.Nexus == 0 || m.How == "folder name" {
			continue
		}
		for _, req := range x.Reqs[strconv.Itoa(m.Nexus)] {
			if installed[req] || x.ModName(req) == "" {
				continue
			}
			if missing[req] == nil {
				missing[req] = &need{}
			}
			missing[req].by = append(missing[req].by, m.Name)
			if cl := x.Mods[strconv.Itoa(m.Nexus)]; len(cl) > 1 && strings.HasPrefix(cl[1], "logic") {
				missing[req].logic = true
			}
		}
	}
	var ids []int
	for id := range missing {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return len(missing[ids[i]].by) > len(missing[ids[j]].by) })
	// a mod many others need is a library: missing it almost always breaks them. Anything else is often
	// listed as a "requirement" for an optional feature or a compatibility patch, so those go in one list.
	var maybeLinks []Link
	for _, id := range ids {
		by := uniq(missing[id].by)
		if x.dependents[id] >= 20 && missing[id].logic && !hasModules[id] { // asset mods list photo / appearance tools that are optional for them
			c.add(Finding{Severity: "medium", Kind: "requirement_missing", Title: x.ModName(id) + " isn't installed, and " + list(by, 3) + " need" + sIf(len(by) == 1) + " it",
				Detail: fmt.Sprintf("%s is a library %d mods on Nexus build on; without it %s may not work.", x.ModName(id), x.dependents[id], list(by, 3)),
				Mods:   by, Links: []Link{link(x.ModName(id), id)}})
			continue
		}
		l := link(x.ModName(id), id)
		l.Note = "listed by " + list(by, 2)
		maybeLinks = append(maybeLinks, l)
	}
	if len(maybeLinks) > 0 {
		c.add(Finding{Severity: "info", Kind: "requirement_maybe", Title: plural(len(maybeLinks), "mod is", "mods are") + " listed as requirements on Nexus but not installed",
			Detail: "Nexus pages also list optional add-ons and compatibility patches as requirements, so most of these are fine. If one of the mods that lists them misbehaves, check its page.",
			Links:  maybeLinks})
	}
}

// ---- the same mod installed twice, in different versions
func (c *checker) duplicates() {
	by := map[int][]*Mod{}
	for _, m := range c.mods {
		if m.Nexus != 0 && !m.Framework {
			by[m.Nexus] = append(by[m.Nexus], m)
		}
	}
	for id, ms := range by {
		if len(ms) < 2 {
			continue
		}
		vers := map[string]bool{}
		var where []string
		for _, m := range ms {
			// zips of the player's own library are separate files of the mod put in on purpose (a main
			// file and its add-ons have their own versions): they don't make it "installed twice"
			if m.Version != "" && !strings.HasPrefix(m.Key, "lib:") {
				vers[m.Version] = true
			}
			where = append(where, m.Where...)
		}
		if len(vers) < 2 {
			continue
		}
		name := ms[0].Name
		if c.o.Index != nil && c.o.Index.ModName(id) != "" {
			name = c.o.Index.ModName(id)
		}
		c.add(Finding{Severity: "medium", Kind: "installed_twice", Title: name + " is installed twice, in different versions (" + strings.Join(keysOf(vers), ", ") + ")",
			Detail: "Leftovers of an older version can override the new one. Places: " + list(uniq(where), 6) + ".", Mods: []string{name}, Links: []Link{link(name, id)}})
	}
}

// ---- mods unpacked into the wrong place
func (c *checker) misinstalled() {
	nested := map[string][]string{}
	packed := map[string][]string{}
	for _, f := range c.files {
		l := f.low
		switch {
		case strings.Count(l, "archive/pc/mod/") > 1, strings.Contains(l, "r6/scripts/") && strings.Contains(l[len("r6/scripts/"):], "r6/"),
			strings.Contains(l, "/cyber_engine_tweaks/mods/") && strings.Contains(l, "/bin/x64/plugins/cyber_engine_tweaks/mods/") && strings.Count(l, "/mods/") > 1,
			strings.HasPrefix(l, "red4ext/plugins/") && strings.Contains(l[len("red4ext/plugins/"):], "red4ext/"):
			nested[c.nameOf(f.owner)] = append(nested[c.nameOf(f.owner)], f.rel)
		case strings.HasSuffix(l, ".zip") || strings.HasSuffix(l, ".7z") || strings.HasSuffix(l, ".rar"):
			packed[c.nameOf(f.owner)] = append(packed[c.nameOf(f.owner)], f.rel)
		case strings.HasSuffix(l, ".archive") && !strings.HasPrefix(l, "archive/pc/mod/") && !strings.HasPrefix(l, "mods/") && !strings.HasPrefix(l, "red4ext/plugins/"):
			nested[c.nameOf(f.owner)] = append(nested[c.nameOf(f.owner)], f.rel)
		}
	}
	for name, fs_ := range nested {
		c.add(Finding{Severity: "high", Kind: "wrong_folder", Title: name + " is unpacked into the wrong folder",
			Detail: "The game only reads mod files in their usual place, so these are ignored: " + list(fs_, 4) + ". Install the mod again so its folders land directly in the game folder.", Mods: []string{name}})
	}
	for name, fs_ := range packed {
		c.add(Finding{Severity: "medium", Kind: "still_packed", Title: name + ": a zip / 7z inside the game's mod folders",
			Detail: "The game can't read packed files: " + list(fs_, 4) + ". Unpack it (or install it with your mod manager) and delete the packed copy.", Mods: []string{name}})
	}
	for _, leftover := range []string{"bin/x64/plugins/cybercmd.asi", "red4ext/plugins/cybercmd", "bin/x64/plugins/cybercmd"} {
		if gamefs.Exists(filepath.Join(c.o.Game, filepath.FromSlash(leftover))) {
			c.add(Finding{Severity: "medium", Kind: "obsolete", Title: "cybercmd is still installed",
				Detail: leftover + ": cybercmd isn't needed since RED4ext 1.13 and can be removed.", Mods: []string{"cybercmd"}})
			break
		}
	}
}

var crashTypes = []struct{ rx, why string }{
	{`EXCEPTION_ACCESS_VIOLATION`, "an access violation: usually a script or plugin mod, or a broken install"},
	{`integer overflow`, "an integer overflow: often too many or too large mods, or a damaged save"},
	{`Gpu Crash|DEVICE_HUNG|DEVICE_REMOVED`, "a GPU crash: driver, overclock, VRAM or a heavy rendering mod, not a script error"},
	{`Watchdog Timeout`, "the game froze (stuck loading or a script that never ends)"},
}

// ---- what the last logs say about plugins
func (c *checker) logs() {
	dir := filepath.Join(c.o.Game, "red4ext", "logs")
	type lf struct {
		name string
		t    time.Time
	}
	var logs []lf
	gamefs.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(p), ".log") {
			if fi, e := d.Info(); e == nil {
				logs = append(logs, lf{p, fi.ModTime()})
			}
		}
		return nil
	})
	if len(logs) > 0 {
		sort.Slice(logs, func(i, j int) bool { return logs[i].t.After(logs[j].t) })
		since := logs[0].t.Add(-time.Hour)
		seen := map[string]bool{}
		for _, l := range logs {
			if l.t.Before(since) {
				break
			}
			plug := regexp.MustCompile(`-\d{4}-\d\d-\d\d-.*$`).ReplaceAllString(filepath.Base(l.name), "")
			if seen[plug] {
				continue
			}
			seen[plug] = true
			b, err := gamefs.ReadFile(l.name)
			if err != nil {
				continue
			}
			t := string(b)
			switch {
			case strings.Contains(t, "Failed to resolve address for hash"):
				c.add(Finding{Severity: "high", Kind: "plugin_outdated", Title: plug + " doesn't match this game version",
					Detail: "Its log says 'Failed to resolve address for hash': this plugin (or RED4ext) is out of date for game " + c.o.GameVersion + ".", Mods: []string{plug}})
			case strings.Contains(t, "Could not attach the hook") || strings.Contains(t, "Detour error code"):
				c.add(Finding{Severity: "high", Kind: "plugin_outdated", Title: plug + " couldn't hook into the game",
					Detail: "Its log says a hook couldn't be attached: the plugin doesn't match this game version.", Mods: []string{plug}})
			}
			if regexp.MustCompile(`(?i)(error|code)\D{0,30}\b1114\b`).MatchString(t) {
				c.add(Finding{Severity: "high", Kind: "runtime_missing", Title: plug + ": error 1114",
					Detail: "Install or repair the Microsoft Visual C++ 2015-2022 Redistributable (x64).", Mods: []string{plug}})
			}
			if strings.HasPrefix(strings.ToLower(plug), "red4ext") {
				if m := regexp.MustCompile(`\] Message: (.*)`).FindStringSubmatch(t); m != nil {
					for _, ct := range crashTypes {
						if regexp.MustCompile(`(?i)` + ct.rx).MatchString(m[1]) {
							c.add(Finding{Severity: "info", Kind: "last_crash", Title: "The last crash RED4ext logged was " + ct.why, Mods: []string{"RED4ext"}})
							break
						}
					}
				}
			}
		}
	}
	rs := filepath.Join(c.o.Game, "r6", "logs", "redscript_rCURRENT.log")
	if b, err := gamefs.ReadFile(rs); err == nil {
		t := string(b)
		if !strings.Contains(t, "Output successfully saved") && !strings.Contains(t, "scc invoked successfully") && !strings.Contains(t, "Compilation complete") {
			c.add(Finding{Severity: "high", Kind: "scripts_not_compiled", Title: "The last REDscript compile didn't finish",
				Detail: "r6\\logs\\redscript_rCURRENT.log ends without 'compiled': the game started without mod scripts. The errors in that log name the mod.", Mods: []string{"redscript"}})
		}
	}
}

// ---- CET Lua mods that do their work every frame
func (c *checker) lua() {
	for _, f := range c.files {
		isLua := f.kind == "CET Lua" && strings.HasSuffix(f.low, ".lua")
		isReds := f.kind == "REDscript" && strings.HasSuffix(f.low, ".reds")
		if !isLua && !isReds || f.size > 1<<20 {
			continue
		}
		b, err := gamefs.ReadFile(filepath.Join(c.o.Game, filepath.FromSlash(f.rel)))
		if err != nil {
			continue
		}
		m := c.mods[f.owner]
		// stage 2 (2.5E): what the general fix rules could change in this file
		if m != nil {
			for _, x := range RuleFixes(f.rel, string(b)) {
				m.Fixes = append(m.Fixes, FileFix{File: f.rel, RuleFix: x})
			}
		}
		if !isLua {
			continue
		}
		hs := ScanLua(string(b))
		if len(hs) == 0 {
			continue
		}
		for _, h := range hs {
			h.File = f.rel
			if m != nil {
				m.Lua = append(m.Lua, h)
			}
			if h.Kind == "candidate" {
				name := c.nameOf(f.owner)
				c.add(Finding{Severity: "info", Kind: "lua_every_frame", Title: name + " does its work every frame",
					Detail: fmt.Sprintf("%s, line %d: %s. A few times a second would do: Slow down, under CET mods that work every frame.", f.rel, h.Line, h.Why),
					Mods:   []string{name}, Fix: "slow"})
			}
		}
	}
}

// ---- small helpers
func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func list(s []string, n int) string {
	if len(s) <= n {
		return strings.Join(s, ", ")
	}
	return strings.Join(s[:n], ", ") + fmt.Sprintf(" and %d more", len(s)-n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func sIf(b bool) string {
	if b {
		return "s"
	}
	return ""
}
