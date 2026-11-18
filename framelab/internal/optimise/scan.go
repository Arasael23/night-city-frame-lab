package optimise

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"framelab/internal/gamefs"
	"framelab/internal/modmap"
)

type Mod struct {
	Key       string       `json:"key"`
	Name      string       `json:"name"`
	Kinds     []string     `json:"kinds"`
	Files     int          `json:"files"`
	Size      int64        `json:"size"`
	Where     []string     `json:"where,omitempty"` // folders / archives it lives in (first few)
	Nexus     int          `json:"nexus,omitempty"` // Nexus Mods id
	Version   string       `json:"version,omitempty"`
	How       string       `json:"how"`              // how it was recognised
	Exact     bool         `json:"exact,omitempty"`  // its files match one Nexus upload exactly
	Approx    bool         `json:"approx,omitempty"` // the version is that of the closest known upload, not an exact match (no update flag from it)
	Framework bool         `json:"framework,omitempty"`
	Latest    string       `json:"latest,omitempty"` // live check: the version Nexus shows
	Update    bool         `json:"update,omitempty"` // live check: Nexus has another version
	Lua       []LuaHandler `json:"lua,omitempty"`
	Fixes     []FileFix    `json:"fixes,omitempty"` // stage 2 (2.5E): changes the general fix rules can make in its files
}

// FileFix: one general rule's change in one of a mod's files.
type FileFix struct {
	File string `json:"file"`
	RuleFix
}

type Link struct {
	Text string `json:"text"`
	URL  string `json:"url"`
	Note string `json:"note,omitempty"` // e.g. which mods list it as a requirement
}

type Finding struct {
	Severity string   `json:"severity"` // high / medium / info
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail,omitempty"`
	Mods     []string `json:"mods,omitempty"`
	Links    []Link   `json:"links,omitempty"`
	Fix      string   `json:"fix,omitempty"` // "slow": Frame Lab can slow it down (Optimise tab, CET mods that work every frame)
}

type Framework struct {
	Name      string `json:"name"`
	Nexus     int    `json:"nexus"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Needed    int    `json:"needed,omitempty"` // files in the build that need it
}

type Report struct {
	Build       string      `json:"build"`
	Game        string      `json:"game"`
	GameVersion string      `json:"game_version"`
	Generated   time.Time   `json:"generated"`
	TookS       float64     `json:"took_s"`
	Files       int         `json:"files"`
	Recognised  int         `json:"recognised"` // mods matched to a Nexus page
	Index       bool        `json:"index"`
	Mods        []Mod       `json:"mods"`
	Frameworks  []Framework `json:"frameworks"`
	Findings    []Finding   `json:"findings"`
	Live        *LiveState  `json:"live,omitempty"`
	Verdict     string      `json:"verdict,omitempty"` // the build in one sentence ("Do this next", 2.5)
	Actions     []Action    `json:"actions,omitempty"` // what to do, most important first (2.5)
	Notes       []string    `json:"notes,omitempty"`
}

type Options struct {
	Build, Game, GameVersion string
	Index                    *Index
	Library                  *modmap.Library // the player's own mod zips, when Frame Lab knows where they are
	Corpus                   *Corpus         // what the downloaded mods carry (may be nil)
}

// libKey: the key the mod library knows a file's folder / archive by
func libKey(f *file) string {
	switch f.kind {
	case "archive":
		if strings.HasSuffix(f.low, ".archive") {
			return "archive:" + filepath.Base(f.low)
		}
	case "REDscript":
		return "reds:" + strings.ToLower(f.group)
	case "TweakXL":
		return "tweak:" + strings.ToLower(f.group)
	case "CET Lua":
		return "cet:" + strings.ToLower(f.group)
	case "RED4ext plugin":
		return "red4ext:" + strings.ToLower(f.group)
	}
	return ""
}

func NexusURL(id int) string {
	return "https://www.nexusmods.com/cyberpunk2077/mods/" + strconv.Itoa(id)
}

// frameworks Frame Lab recognises by one file, with the Nexus id players get them from
var frameworks = []struct {
	name  string
	nexus int
	file  string // relative to the game folder; its version stamp is read when it is a .dll / .asi / .exe
}{
	{"RED4ext", 2380, "red4ext/RED4ext.dll"},
	{"Cyber Engine Tweaks", 107, "bin/x64/plugins/cyber_engine_tweaks.asi"},
	{"redscript", 1511, "engine/tools/scc.exe"},
	{"ArchiveXL", 4198, "red4ext/plugins/ArchiveXL/ArchiveXL.dll"},
	{"TweakXL", 4197, "red4ext/plugins/TweakXL/TweakXL.dll"},
	{"Codeware", 7780, "red4ext/plugins/Codeware/Codeware.dll"},
	{"Mod Settings", 4885, "red4ext/plugins/mod_settings/mod_settings.dll"},
	{"Input Loader", 4575, "red4ext/plugins/input_loader/input_loader.dll"},
	{"Audioware", 12001, "red4ext/plugins/Audioware/Audioware.dll"},
	{"RedFileSystem", 13378, "red4ext/plugins/RedFileSystem/RedFileSystem.dll"},
	{"RedData", 14139, "red4ext/plugins/RedData/RedData.dll"},
	{"Native Settings UI", 3518, "bin/x64/plugins/cyber_engine_tweaks/mods/nativeSettings/init.lua"},
}

// where mods live in a game folder
var modRoots = []string{"archive/pc/mod", "r6/scripts", "r6/tweaks", "r6/input", "bin/x64/plugins", "red4ext/plugins", "mods"}

type file struct {
	rel   string // forward slashes, as on disk
	low   string
	size  int64
	kind  string
	group string
	owner string // key of the mod it belongs to
}

var (
	reVortex = regexp.MustCompile(`^(.+?)-(\d{1,7})-([0-9][0-9A-Za-z-]*?)-(\d{9,11})$`)
	reNexus  = regexp.MustCompile(`(?i)nexusmods\.com/(?:games/)?cyberpunk2077/mods/(\d{1,7})`)
	reStem   = regexp.MustCompile(`(?i)(\.archive)?(\.xl)?$|\.(reds|yaml|yml|tweak|lua|dll|asi|xml)$`)
)

// kindAndGroup: what sort of mod file this is and the folder / archive it belongs to.
func kindAndGroup(rel string) (kind, group string) {
	p := strings.Split(rel, "/")
	low := strings.ToLower(rel)
	switch {
	case strings.HasPrefix(low, "bin/x64/plugins/cyber_engine_tweaks/mods/") && len(p) > 6:
		if strings.EqualFold(p[5], "NightCityFrameLabOptions") {
			return "CET Lua", "Night City Frame Lab: kept game options" // Frame Lab's own (Optimise > Game options)
		}
		return "CET Lua", p[5]
	case strings.HasPrefix(low, "bin/x64/plugins/cyber_engine_tweaks/"):
		return "", "" // CET itself
	case strings.HasPrefix(low, "bin/x64/plugins/"):
		if len(p) > 4 {
			return "ASI plugin", p[3]
		}
		return "ASI plugin", reStem.ReplaceAllString(p[len(p)-1], "")
	case strings.HasPrefix(low, "red4ext/plugins/") && len(p) > 3:
		return "RED4ext plugin", p[2]
	case strings.HasPrefix(low, "r6/scripts/"):
		if len(p) > 3 {
			return "REDscript", p[2]
		}
		return "REDscript", reStem.ReplaceAllString(p[len(p)-1], "")
	case strings.HasPrefix(low, "r6/tweaks/"):
		if len(p) > 3 {
			return "TweakXL", p[2]
		}
		return "TweakXL", reStem.ReplaceAllString(p[len(p)-1], "")
	case strings.HasPrefix(low, "r6/input/"):
		return "Input Loader", reStem.ReplaceAllString(p[len(p)-1], "")
	case strings.HasPrefix(low, "mods/") && len(p) > 2:
		return "REDmod", p[1]
	case strings.HasPrefix(low, "archive/pc/mod/"):
		if !strings.HasSuffix(low, ".archive") && !strings.HasSuffix(low, ".xl") && !strings.HasSuffix(low, ".zip") && !strings.HasSuffix(low, ".7z") && !strings.HasSuffix(low, ".rar") {
			return "", "" // readmes, notes
		}
		return "archive", reStem.ReplaceAllString(p[len(p)-1], "")
	}
	return "", ""
}

func skipFile(low string) bool {
	return strings.HasSuffix(low, ".log") || strings.Contains(low, "/logs/") || strings.HasSuffix(low, "vortex.deployment.json") ||
		strings.HasSuffix(low, ".tmp") || strings.HasSuffix(low, ".bak")
}

// Scan reads a build's mod folders and returns what it found. Read only.
func Scan(o Options) *Report {
	t0 := time.Now()
	r := &Report{Build: o.Build, Game: o.Game, GameVersion: o.GameVersion, Generated: t0, Index: o.Index != nil}
	game := o.Game

	// ---- every mod file
	var files []*file
	for _, root := range modRoots {
		base := filepath.Join(game, filepath.FromSlash(root))
		gamefs.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(game, p)
			if e != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			low := strings.ToLower(rel)
			if skipFile(low) {
				return nil
			}
			k, g := kindAndGroup(rel)
			if k == "" {
				return nil
			}
			var size int64
			if fi, e := d.Info(); e == nil {
				size = fi.Size()
			}
			files = append(files, &file{rel: rel, low: low, size: size, kind: k, group: g})
			return nil
		})
	}
	r.Files = len(files)
	present := make(map[string]bool, len(files))
	for _, f := range files {
		present[f.low] = true
	}

	// ---- who each file belongs to, strongest source first
	mo2 := gamefs.ModFiles(game)
	var mo2Mods string
	if inst, _ := gamefs.Status(game); inst != nil {
		mo2Mods = inst.ModsDir
	}
	vortex := modmap.VortexSources(game)
	mods := map[string]*Mod{}
	get := func(key, name, how string) *Mod {
		m := mods[key]
		if m == nil {
			m = &Mod{Key: key, Name: name, How: how}
			mods[key] = m
		}
		return m
	}
	frameworkFile := map[string]int{}
	for i, fw := range frameworks {
		frameworkFile[strings.ToLower(fw.file)] = i
	}

	// index: which Nexus upload has the most of its files here
	count := map[int]int{}
	cands := map[*file][]int{}
	if x := o.Index; x != nil {
		for _, f := range files {
			if c := x.sig[sigOf(f.rel, f.size)]; len(c) > 0 {
				cands[f] = c
				for _, u := range c {
					count[u]++
				}
			}
		}
	}
	ratio := func(u int) float64 {
		if n := o.Index.NSig[u]; n > 0 {
			return float64(count[u]) / float64(n)
		}
		return 0
	}
	bestUpload := map[int]int{} // nexus id -> upload with the best match
	for _, f := range files {
		switch {
		case mo2 != nil && mo2[f.low] != "":
			name := mo2[f.low]
			m := get("mo2:"+strings.ToLower(name), name, "Mod Organizer 2")
			if m.Nexus == 0 && mo2Mods != "" {
				m.Nexus, m.Version = readMeta(filepath.Join(mo2Mods, name, "meta.ini"))
			}
			f.owner = m.Key
		case vortex[f.low] != "":
			src := vortex[f.low]
			name, id, ver := src, 0, ""
			if mm := reVortex.FindStringSubmatch(src); mm != nil {
				name, ver = mm[1], strings.ReplaceAll(mm[3], "-", ".")
				id, _ = strconv.Atoi(mm[2])
			}
			m := get("vx:"+strings.ToLower(src), name, "Vortex")
			m.Nexus, m.Version = id, ver
			f.owner = m.Key
		case len(cands[f]) > 0:
			best := -1
			for _, u := range cands[f] {
				if best < 0 || ratio(u) > ratio(best) || (ratio(u) == ratio(best) && (count[u] > count[best] ||
					count[u] == count[best] && newerUpload(o.Index, u, best))) {
					best = u
				}
			}
			up := o.Index.upload(best)
			key := "nx:" + strconv.Itoa(up.Nexus)
			m := get(key, o.Index.ModName(up.Nexus), "matched files")
			m.Nexus = up.Nexus
			if b, ok := bestUpload[up.Nexus]; !ok || ratio(best) > ratio(b) || ratio(best) == ratio(b) && newerUpload(o.Index, best, b) {
				bestUpload[up.Nexus] = best
			}
			f.owner = key
		default:
			if i, ok := frameworkFile[f.low]; ok {
				fw := frameworks[i]
				key := "nx:" + strconv.Itoa(fw.nexus)
				m := get(key, fw.name, "framework")
				m.Nexus = fw.nexus
				f.owner = key
				continue
			}
			if k := libKey(f); k != "" && o.Library != nil {
				if label, rel := o.Library.Find(k); rel != "" {
					key := "lib:" + strings.ToLower(rel)
					m := get(key, label, "your mod library")
					if id, _ := strconv.Atoi(modmap.NexusID(rel)); id > 0 {
						m.Nexus = id
						if m.Version == "" {
							m.Version = modmap.NexusVersion(rel) // the version in the zip's name
						}
					}
					f.owner = key
					continue
				}
			}
			key := "grp:" + strings.ToLower(f.kind+":"+f.group)
			get(key, f.group, "folder name")
			f.owner = key
		}
	}
	for id, u := range bestUpload {
		m := mods["nx:"+strconv.Itoa(id)]
		up := o.Index.upload(u)
		m.Version = up.Version
		m.Exact = ratio(u) >= 0.8
		m.Approx = !m.Exact
	}
	// a mod with several files on Nexus (a main file and add-ons, each with its own version): its version
	// is the highest among the uploads whose files are all here, not that of whichever matched best
	for f, cs := range cands {
		m := mods[f.owner]
		if m == nil || !strings.HasPrefix(f.owner, "nx:") {
			continue
		}
		for _, u := range cs {
			up := o.Index.upload(u)
			if up.Nexus == m.Nexus && ratio(u) >= 0.8 && (m.Approx || verLess(m.Version, up.Version)) {
				m.Version, m.Exact, m.Approx = up.Version, true, false
			}
		}
	}
	// the player's own library zip knows the version too: it wins when the files aren't one known upload
	// exactly (a version newer than the index, or edited files) or when its version is the newer one
	if o.Library != nil {
		for _, f := range files {
			m := mods[f.owner]
			if m == nil || m.Nexus == 0 || !strings.HasPrefix(f.owner, "nx:") {
				continue
			}
			k := libKey(f)
			if k == "" {
				continue
			}
			_, rel := o.Library.Find(k)
			if rel == "" {
				continue
			}
			if id, _ := strconv.Atoi(modmap.NexusID(rel)); id != m.Nexus {
				continue
			}
			if v := modmap.NexusVersion(rel); v != "" && (m.Approx || m.Version == "" || verLess(m.Version, v)) {
				m.Version, m.Approx = v, false
			}
		}
	}

	// folders / archives with no other clue: a Nexus link in their readme
	byOwner := map[string][]*file{}
	for _, f := range files {
		byOwner[f.owner] = append(byOwner[f.owner], f)
	}
	for key, m := range mods {
		if m.Nexus != 0 || !strings.HasPrefix(key, "grp:") {
			continue
		}
		if id := readmeLink(game, byOwner[key]); id != 0 {
			m.Nexus, m.How = id, "Nexus link in its readme"
			if n := o.Index.ModName(id); n != "" {
				m.Name = n
			}
		}
	}

	// a mod found two ways (some files matched on Nexus, the rest through the library): one entry
	for key, m := range mods {
		if m.Nexus == 0 || strings.HasPrefix(key, "nx:") {
			continue
		}
		if nx := mods["nx:"+strconv.Itoa(m.Nexus)]; nx != nil && strings.HasPrefix(key, "lib:") {
			// the library zip's version counts when it is the newer one (the main file, next to an add-on
			// the index matched) or when the matched files aren't one known upload exactly
			if m.Version != "" && (nx.Approx || nx.Version == "" || verLess(nx.Version, m.Version)) {
				nx.Version, nx.Approx = m.Version, false
			}
			for _, f := range byOwner[key] {
				f.owner = nx.Key
			}
			byOwner[nx.Key] = append(byOwner[nx.Key], byOwner[key]...)
			delete(byOwner, key)
			delete(mods, key)
		}
	}
	// archives of the library Frame Lab can't look inside (.rar, .7z): their names still say which version
	// of a mod was downloaded, which counts when it is newer than the upload the files matched
	if o.Library != nil && len(o.Library.Others) > 0 {
		newest := map[int]string{}
		for _, rel := range o.Library.Others {
			id, _ := strconv.Atoi(modmap.NexusID(rel))
			if v := modmap.NexusVersion(rel); id > 0 && v != "" && (newest[id] == "" || verLess(newest[id], v)) {
				newest[id] = v
			}
		}
		for key, m := range mods {
			if v := newest[m.Nexus]; v != "" && strings.HasPrefix(key, "nx:") && m.Version != "" && verLess(m.Version, v) {
				m.Version, m.Approx = v, false
			}
		}
	}
	// leftovers listed by folder name that carry a recognised mod's own name (AdvancedCrashReporter next to
	// Advanced Crash Reporter: files the mod made itself, or extras of another version): part of that mod
	byName := map[string]string{}
	for key, m := range mods {
		if m.Nexus == 0 {
			continue
		}
		n := normName(m.Name)
		if len(n) < 6 {
			continue
		}
		if _, dup := byName[n]; dup {
			byName[n] = "" // two mods with that name: don't guess
		} else {
			byName[n] = key
		}
	}
	for key := range mods {
		if !strings.HasPrefix(key, "grp:") {
			continue
		}
		g := key[strings.LastIndexByte(key, ':')+1:]
		if to := byName[normName(g)]; to != "" {
			for _, f := range byOwner[key] {
				f.owner = to
			}
			byOwner[to] = append(byOwner[to], byOwner[key]...)
			delete(byOwner, key)
			delete(mods, key)
		}
	}

	// totals per mod
	for key, fs_ := range byOwner {
		m := mods[key]
		kinds := map[string]bool{}
		where := map[string]bool{}
		for _, f := range fs_ {
			m.Files++
			m.Size += f.size
			kinds[f.kind] = true
			if len(where) < 6 {
				where[whereOf(f)] = true
			}
		}
		for k := range kinds {
			m.Kinds = append(m.Kinds, k)
		}
		sort.Strings(m.Kinds)
		for w := range where {
			m.Where = append(m.Where, w)
		}
		sort.Strings(m.Where)
	}

	// ---- frameworks
	have := map[int]bool{}
	for i, fw := range frameworks {
		st := Framework{Name: fw.name, Nexus: fw.nexus}
		p := filepath.Join(game, filepath.FromSlash(fw.file))
		if gamefs.IsFile(p) {
			st.Installed = true
			have[fw.nexus] = true
			if ext := strings.ToLower(filepath.Ext(p)); ext == ".dll" || ext == ".asi" || ext == ".exe" {
				st.Version = FileVersion(gamefs.Real(p))
			}
			if m := mods["nx:"+strconv.Itoa(fw.nexus)]; m != nil {
				m.Framework = true
				if st.Version != "" {
					m.Version = st.Version
				} else if m.Exact {
					st.Version = m.Version // no version stamp in its files (redscript, Audioware ...): the Nexus upload its files match
				}
			} else {
				for _, m := range mods {
					if m.Nexus == fw.nexus {
						m.Framework = true
					}
				}
			}
		}
		r.Frameworks = append(r.Frameworks, st)
		_ = i
	}

	// ---- archives not recognised by their files: by the game files they carry (corpus), e.g. a renamed one
	if o.Corpus != nil {
		for _, f := range files {
			m := mods[f.owner]
			if m == nil || m.Nexus != 0 || !strings.HasPrefix(f.low, "archive/pc/mod/") || !strings.HasSuffix(f.low, ".archive") {
				continue
			}
			hs, err := modmap.ArchiveHashes(gamefs.Real(filepath.Join(o.Game, filepath.FromSlash(f.rel))))
			if err != nil || len(hs) < 5 {
				continue
			}
			a, share := o.Corpus.RecognizeArchive(hs)
			if a.Nexus == 0 || share < 0.6 {
				continue
			}
			m.Nexus = a.Nexus
			if n := o.Index.ModName(a.Nexus); n != "" {
				m.Name = n
			}
			m.How = "what its archive carries: " + strconv.Itoa(int(share*100+0.5)) + "% of " + filepath.Base(f.rel) + "'s game files are in " + a.Name
			m.Approx = true
		}
	}

	// ---- mods list
	for _, m := range mods {
		if m.Name == "" {
			m.Name = "Nexus mod " + strconv.Itoa(m.Nexus)
		}
		if m.Nexus != 0 {
			r.Recognised++
		}
		r.Mods = append(r.Mods, *m)
	}
	sort.Slice(r.Mods, func(i, j int) bool { return strings.ToLower(r.Mods[i].Name) < strings.ToLower(r.Mods[j].Name) })

	// ---- checks
	ck := &checker{r: r, o: o, files: files, present: present, mods: mods, have: have, byOwner: byOwner}
	ck.gameVersion()
	ck.missingFrameworks()
	ck.modules()
	ck.requirements()
	ck.duplicates()
	ck.misinstalled()
	ck.logs()
	ck.lua()
	sort.SliceStable(r.Findings, func(i, j int) bool { return sevRank(r.Findings[i].Severity) < sevRank(r.Findings[j].Severity) })
	// mods list again: the Lua results were added to the map entries
	for i := range r.Mods {
		if m := mods[r.Mods[i].Key]; m != nil {
			r.Mods[i].Lua = m.Lua
			r.Mods[i].Fixes = m.Fixes
		}
	}
	if r.Files == 0 {
		r.Notes = append(r.Notes, "No mod files in this build (archive\\pc\\mod, r6\\scripts, r6\\tweaks, bin\\x64\\plugins, red4ext and mods are empty or not there): it looks like an unmodded copy, so there's nothing to check yet.")
	}
	if o.Index == nil {
		r.Notes = append(r.Notes, "The mod index (components\\Optimise\\mods-index.json.gz) wasn't found, so mods are listed by folder name only.")
	}
	r.TookS = time.Since(t0).Seconds()
	return r
}

func sevRank(s string) int {
	switch s {
	case "high":
		return 0
	case "medium":
		return 1
	}
	return 2
}

func whereOf(f *file) string {
	p := strings.Split(f.rel, "/")
	switch f.kind {
	case "archive", "Input Loader":
		return f.rel
	case "CET Lua":
		return strings.Join(p[:6], "/")
	case "REDmod":
		return strings.Join(p[:2], "/")
	}
	if len(p) > 3 {
		return strings.Join(p[:3], "/")
	}
	return f.rel
}

// readMeta: Nexus id and version from an MO2 mod's meta.ini
func readMeta(path string) (id int, ver string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(k)) {
		case "modid":
			id, _ = strconv.Atoi(strings.TrimSpace(v))
		case "version":
			ver = strings.Trim(strings.TrimSpace(v), `"`)
			ver = strings.TrimSuffix(strings.TrimPrefix(ver, "d"), ".0.0")
		}
	}
	if id < 0 {
		id = 0
	}
	return
}

// readmeLink: the one Nexus mod a readme / info file in these files links to (0 if none or several)
func readmeLink(game string, fs_ []*file) int {
	ids := map[int]bool{}
	for _, f := range fs_ {
		base := strings.ToLower(filepath.Base(f.low))
		if !(strings.HasPrefix(base, "readme") || strings.HasSuffix(base, ".md") || base == "info.json") || f.size > 256<<10 {
			continue
		}
		b, err := gamefs.ReadFile(filepath.Join(game, filepath.FromSlash(f.rel)))
		if err != nil {
			continue
		}
		for _, m := range reNexus.FindAllSubmatch(b, -1) {
			if id, _ := strconv.Atoi(string(m[1])); id > 0 {
				ids[id] = true
			}
		}
	}
	if len(ids) == 1 {
		for id := range ids {
			return id
		}
	}
	return 0
}

var reNotAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// normName: a mod or folder name with only its letters and digits ("Advanced Crash Reporter" = "AdvancedCrashReporter")
func normName(s string) string { return reNotAlnum.ReplaceAllString(strings.ToLower(s), "") }

// newerUpload: a was uploaded after b (higher Nexus file id). When several versions ship the same files,
// the installed one is taken to be the newest of them, not the first version that had those files.
func newerUpload(x *Index, a, b int) bool { return x.upload(a).FileID > x.upload(b).FileID }
