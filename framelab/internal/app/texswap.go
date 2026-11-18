package app

// Lighter texture versions (VRAM page). Heavy texture mods often come in lighter versions on Nexus
// (ETO 4K / 2K / 1K, skins 4K / 2K ...). Frame Lab opens the mod's Files page, the player downloads the
// lighter file in the browser (no API key; the author's download count works as usual), and Frame Lab
// finds the download in the Downloads folder and checks it: the same mod's game files, less GPU data.
// 2.5E then swaps the archives (the originals are kept and Undo puts them back exactly, a change in the
// test round like every fix); 2.5 shows the steps. Builds run by Mod Organizer 2 or Vortex get the steps
// too: the swap belongs in the mod manager there.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"framelab/internal/gamefs"
	"framelab/internal/modmap"
	"framelab/internal/optimise"
	"framelab/internal/platform"
	"framelab/internal/vram"
)

type SwapArchive struct {
	Name  string  `json:"name"`
	MB    float64 `json:"mb"`
	Files int     `json:"files"`
	tmp   string
	hash  []uint64
}

type SwapCand struct {
	Zip      string        `json:"zip"` // file name in the Downloads folder
	State    string        `json:"state"`
	Problem  string        `json:"problem,omitempty"`
	Archives []SwapArchive `json:"archives,omitempty"`
	MB       float64       `json:"mb"`    // GPU data of its archives
	Match    float64       `json:"match"` // share of the installed archives' game files it carries
	path     string
	dir      string
}

type SwapInfo struct {
	Mod       string             `json:"mod"`
	Nexus     int                `json:"nexus"`
	Archives  []string           `json:"archives"` // installed, relative to the game folder
	MB        float64            `json:"mb"`
	Variants  []optimise.Variant `json:"variants,omitempty"`
	Downloads string             `json:"downloads"`
	Managed   string             `json:"managed,omitempty"` // "Mod Organizer 2" / "Vortex" / "REDmod": by hand
	Locked    string             `json:"locked,omitempty"`  // 2.5E: why the swap waits (the test round)
	Cands     []SwapCand         `json:"cands"`
}

var (
	swapMu    sync.Mutex
	swapJobs  = map[string]*SwapCand{}
	swapClean sync.Once
)

// swapTidy: unpacked downloads nobody needs any more (left from an earlier start, or the download is gone)
func (s *Server) swapTidy() {
	root := filepath.Join(s.Paths.Data, "texswap")
	swapClean.Do(func() { os.RemoveAll(root) }) // jobs live in memory: after a start every folder is stale
	swapMu.Lock()
	defer swapMu.Unlock()
	for k, x := range swapJobs {
		if x.State != "checking" && !fileExists(x.path) {
			os.RemoveAll(x.dir)
			delete(swapJobs, k)
		}
	}
}

// downloadsDir: where the browser saves downloads (a variable for the tests)
var downloadsDir = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

func shaFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// moveFile: rename, or copy + remove across drives (keeps the time, so the report's size+time check holds)
func moveFile(src, dst string) error {
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		in.Close()
		return err
	}
	_, err = io.Copy(out, in)
	in.Close()
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return os.Remove(src)
}

// addedAt: the hash of the big file the same fix put at this path ("" = none)
func addedAt(files []FixFile, real string) string {
	for _, f := range files {
		if f.Big && f.Before == "" && strings.EqualFold(f.Real, real) {
			return f.After
		}
	}
	return ""
}

// bigUndoStep: for a moved big file, whether Undo can put it back ("skip" = already as it was).
// added: what the same fix put at this path (it goes first, so the original can come back there).
func bigUndoStep(f FixFile, added string) (string, bool, error) {
	if f.After == "" { // the original was moved away: back from the kept copy
		if _, err := os.Stat(f.Real); err == nil {
			h, _ := shaFile(f.Real)
			if h == f.Before {
				return "skip", true, nil
			}
			if added == "" || h != added {
				return "", false, nil // something else is in its place now
			}
		}
		if h, err := shaFile(f.Backup); err != nil || h != f.Before {
			return "", false, errors.New("a kept original is missing or changed: nothing was touched")
		}
		return "", true, nil
	}
	h, err := shaFile(f.Real) // the archive Frame Lab put in
	if err != nil {
		return "skip", true, nil // already gone
	}
	if h != f.After {
		return "", false, nil
	}
	return "", true, nil
}

// lighterInfo: what Frame Lab knows about a lighter version of one mod of the build (VRAM report label)
func (s *Server) lighterInfo(i int, mod string) (*SwapInfo, BuildCfg, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, b, err
	}
	bs, err := os.ReadFile(s.vramPath(b.Name))
	if err != nil {
		return nil, b, errors.New("scan the build on the VRAM page first")
	}
	var rep vram.Report
	if err := json.Unmarshal(bs, &rep); err != nil {
		return nil, b, err
	}
	var m *vram.Mod
	for k := range rep.Mods {
		if rep.Mods[k].Label == mod {
			m = &rep.Mods[k]
		}
	}
	if m == nil {
		return nil, b, errors.New("that mod isn't in the last VRAM scan: scan the build again")
	}
	s.swapTidy()
	info := &SwapInfo{Mod: m.Label, Downloads: downloadsDir(), Cands: []SwapCand{}}
	info.Nexus, _ = strconv.Atoi(m.Nexus)
	have := map[uint64]bool{}
	for _, rel := range m.Archives {
		if !strings.HasPrefix(strings.ToLower(rel), "archive/pc/mod/") {
			info.Managed = "REDmod"
		}
		info.Archives = append(info.Archives, rel)
		mb, hs, err := vram.ArchiveGPU(gamefs.Real(filepath.Join(b.Path, filepath.FromSlash(rel))))
		if err != nil {
			continue
		}
		info.MB += mb
		for _, h := range hs {
			have[h] = true
		}
	}
	c := s.corpus()
	if info.Nexus == 0 && c != nil && len(have) > 0 { // recognised by its game files
		var hs []uint64
		for h := range have {
			hs = append(hs, h)
		}
		if a, share := c.RecognizeArchive(hs); share >= 0.6 {
			info.Nexus = a.Nexus
		}
	}
	info.Variants = c.VariantsOf(info.Nexus)
	if gamefs.Managed(b.Path) != nil {
		info.Managed = "Mod Organizer 2"
	} else if len(modmap.VortexSources(b.Path)) > 0 {
		info.Managed = "Vortex"
	}
	if info.Nexus == 0 {
		return info, b, nil
	}
	// downloads of this mod from the last two weeks (Nexus names them <name>-<mod id>-<version>-<time>)
	ents, _ := os.ReadDir(info.Downloads)
	type dl struct {
		name string
		at   time.Time
		size int64
	}
	var dls []dl
	for _, e := range ents {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".zip" && ext != ".7z" && ext != ".rar") || modmap.NexusID(e.Name()) != strconv.Itoa(info.Nexus) {
			continue
		}
		fi, err := e.Info()
		if err != nil || time.Since(fi.ModTime()) > 14*24*time.Hour {
			continue
		}
		dls = append(dls, dl{e.Name(), fi.ModTime(), fi.Size()})
	}
	sort.Slice(dls, func(i, j int) bool { return dls[i].at.After(dls[j].at) })
	if len(dls) > 4 {
		dls = dls[:4]
	}
	for _, d := range dls {
		p := filepath.Join(info.Downloads, d.name)
		key := fmt.Sprintf("%s|%d|%d|%s", p, d.size, d.at.Unix(), mod)
		swapMu.Lock()
		j := swapJobs[key]
		if j == nil {
			sum := sha256.Sum256([]byte(key))
			j = &SwapCand{Zip: d.name, State: "checking", path: p, dir: filepath.Join(s.Paths.Data, "texswap", hex.EncodeToString(sum[:6]))}
			swapJobs[key] = j
			go s.checkCand(j, have, info.MB, info.Archives, b.Path)
		}
		cp := *j
		swapMu.Unlock()
		info.Cands = append(info.Cands, cp)
	}
	return info, b, nil
}

var docExt = map[string]bool{".txt": true, ".md": true, ".pdf": true, ".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".url": true, ".html": true, ".ini": false}

// checkCand unpacks a download's archives into Frame Lab's data folder and compares them with the mod
func (s *Server) checkCand(j *SwapCand, have map[uint64]bool, haveMB float64, installed []string, game string) {
	fail := func(p string) {
		os.RemoveAll(j.dir) // nothing to keep
		swapMu.Lock()
		j.State, j.Problem = "no", p
		swapMu.Unlock()
	}
	os.RemoveAll(j.dir)
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		fail(err.Error())
		return
	}
	var archives []string
	var other []string
	switch strings.ToLower(filepath.Ext(j.path)) {
	case ".zip":
		zr, err := zip.OpenReader(j.path)
		if err != nil {
			fail("can't open the download: " + err.Error())
			return
		}
		for _, f := range zr.File {
			name := strings.ReplaceAll(f.Name, `\`, "/")
			if f.FileInfo().IsDir() || strings.Contains(name, "..") {
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".archive" {
				if !docExt[ext] {
					other = append(other, name)
				}
				continue
			}
			dst := filepath.Join(j.dir, filepath.Base(name))
			if err := unzipOne(f, dst); err != nil {
				zr.Close()
				fail("can't unpack " + filepath.Base(name) + ": " + err.Error())
				return
			}
			archives = append(archives, dst)
		}
		zr.Close()
	default: // .7z / .rar: with 7-Zip when it is installed
		sz := sevenZip()
		if sz == "" {
			fail("it is a " + strings.TrimPrefix(filepath.Ext(j.path), ".") + " file and 7-Zip isn't installed: install 7-Zip, or swap the files by hand (steps below)")
			return
		}
		cmd := exec.Command(sz, "x", "-y", "-o"+j.dir, j.path)
		platform.HideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			fail("7-Zip couldn't unpack it: " + strings.TrimSpace(string(out)))
			return
		}
		filepath.Walk(j.dir, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext == ".archive" {
				archives = append(archives, p)
			} else if !docExt[ext] {
				rel, _ := filepath.Rel(j.dir, p)
				other = append(other, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	if len(other) > 0 {
		fail(fmt.Sprintf("it has more than texture archives (%s): install it by hand", strings.Join(other[:min(3, len(other))], ", ")))
		return
	}
	if len(archives) == 0 {
		fail("there is no .archive in it")
		return
	}
	var arcs []SwapArchive
	var mb float64
	got := 0
	seen := map[uint64]bool{}
	ours := map[string]bool{}
	for _, rel := range installed {
		ours[strings.ToLower(filepath.Base(rel))] = true
	}
	for _, p := range archives {
		amb, hs, err := vram.ArchiveGPU(p)
		if err != nil {
			fail(filepath.Base(p) + ": " + err.Error())
			return
		}
		name := filepath.Base(p)
		if !ours[strings.ToLower(name)] {
			if _, err := os.Stat(filepath.Join(game, "archive", "pc", "mod", name)); err == nil {
				fail(name + " is already in the build and belongs to another mod")
				return
			}
		}
		arcs = append(arcs, SwapArchive{Name: name, MB: amb, Files: len(hs), tmp: p, hash: hs})
		mb += amb
		for _, h := range hs {
			if have[h] && !seen[h] {
				seen[h] = true
				got++
			}
		}
	}
	match := 0.0
	if len(have) > 0 {
		match = float64(got) / float64(len(have))
	}
	swapMu.Lock()
	defer swapMu.Unlock()
	j.Archives, j.MB, j.Match = arcs, mb, match
	switch {
	case match < 0.8:
		j.State, j.Problem = "no", fmt.Sprintf("it carries only %.0f%% of the installed mod's game files: another file of the mod (an add-on or patch) or another mod", match*100)
	case mb >= haveMB*0.98:
		j.State, j.Problem = "no", "it isn't lighter than what is installed"
	default:
		j.State = "ready"
	}
	if j.State != "ready" {
		os.RemoveAll(j.dir)
	}
}

func unzipOne(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sevenZip() string {
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)")} {
		if root == "" {
			continue
		}
		if p := filepath.Join(root, "7-Zip", "7z.exe"); fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath("7z"); err == nil {
		return p
	}
	return ""
}

// applySwap (2.5E): the installed archives out (kept), the download's in
func (s *Server) applySwap(i int, mod, zipName string) (*FixRecord, error) {
	info, b, err := s.lighterInfo(i, mod)
	if err != nil {
		return nil, err
	}
	if info.Managed != "" {
		return nil, errors.New(info.Managed + " manages this build's files: install the lighter download there and turn the old one off (steps below)")
	}
	if gameRunning(b.Path) {
		return nil, errors.New("close the game first")
	}
	var j *SwapCand
	swapMu.Lock()
	for _, x := range swapJobs {
		if x.Zip == zipName && x.State == "ready" && strings.EqualFold(filepath.Dir(x.path), info.Downloads) {
			j = x
		}
	}
	swapMu.Unlock()
	if j == nil {
		return nil, errors.New("that download isn't checked (or isn't a lighter version of this mod)")
	}
	fixMu.Lock()
	defer fixMu.Unlock()
	all := s.loadFixes()
	rec := FixRecord{Kind: "texture_swap", Mod: mod}
	rec.ID = time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", len(all)+1)
	rec.Build, rec.Applied = b.Name, time.Now().Format("2006-01-02 15:04")
	bdir := filepath.Join(s.Paths.Data, "optimise", "backups", rec.ID)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return nil, err
	}
	var done []FixFile
	rollback := func() {
		for k := len(done) - 1; k >= 0; k-- {
			f := done[k]
			if f.Before == "" {
				moveFile(f.Real, filepath.Join(j.dir, filepath.Base(f.Real)))
			} else {
				moveFile(f.Backup, f.Real)
			}
		}
	}
	var oldNames, newNames []string
	for _, rel := range info.Archives {
		real := gamefs.Real(filepath.Join(b.Path, filepath.FromSlash(rel)))
		h, err := shaFile(real)
		if err != nil {
			rollback()
			return nil, err
		}
		bk := filepath.Join(bdir, filepath.Base(real))
		if err := moveFile(real, bk); err != nil {
			rollback()
			return nil, fmt.Errorf("could not move %s aside: %w. Nothing was changed", rel, err)
		}
		done = append(done, FixFile{Path: rel, Real: real, Before: h, After: "", Backup: bk, Big: true})
		oldNames = append(oldNames, filepath.Base(rel))
	}
	for _, a := range j.Archives {
		rel := "archive/pc/mod/" + a.Name
		dst := filepath.Join(b.Path, "archive", "pc", "mod", a.Name)
		if fileExists(dst) {
			rollback()
			return nil, errors.New(a.Name + " is in the build already. Nothing was changed")
		}
		if err := moveFile(a.tmp, dst); err != nil {
			rollback()
			return nil, fmt.Errorf("could not put %s in: %w. Nothing was changed", a.Name, err)
		}
		h, err := shaFile(dst)
		fi, serr := os.Stat(dst)
		if err != nil || serr != nil {
			done = append(done, FixFile{Path: rel, Real: dst, Big: true})
			rollback()
			return nil, errors.New("could not read " + a.Name + " back. Nothing was changed")
		}
		done = append(done, FixFile{Path: rel, Real: dst, Before: "", After: h, Big: true, Size: fi.Size(), MTime: fi.ModTime().UnixNano()})
		newNames = append(newNames, a.Name)
	}
	rec.Files = done
	rec.File, rec.Real, rec.Before, rec.Backup = done[0].Path, done[0].Real, done[0].Before, done[0].Backup
	rec.What = fmt.Sprintf("%s swapped for %s (from %s): GPU data %s -> %s", strings.Join(oldNames, ", "), strings.Join(newNames, ", "), j.Zip, mbStr(info.MB), mbStr(j.MB))
	all = append(all, rec)
	if err := s.saveFixes(all); err != nil {
		rollback()
		return nil, err
	}
	swapMu.Lock()
	for k, x := range swapJobs {
		if x == j {
			delete(swapJobs, k)
		}
	}
	swapMu.Unlock()
	os.RemoveAll(j.dir)
	s.Ctl.diag("texture swap " + rec.ID + ": " + rec.Mod + ": " + rec.What)
	return &rec, nil
}

func mbStr(mb float64) string {
	if mb >= 1000 {
		return fmt.Sprintf("%.2f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}
