package app

// Workbench (2.5E Optimise stage 4): edit a mod by hand inside Frame Lab, with checks while you type
// and advice on what to do and not. A project is a copy in data\workbench\<id>:
//
//	original\   the files as they were when the project was made (for "show changes" and the checks)
//	work\       the files being edited
//	raw\        archive projects: the game files WolvenKit unpacked from the .archive
//	build\      archive projects: the files packed again at install
//	out\        archive projects: the packed .archive
//
// Nothing in the game build changes until Install, which goes through the fix ledger like every other
// fix (original kept, exact Undo, a change in the test round). Archives are unpacked / turned into
// readable JSON / packed with the WolvenKit console Frame Lab ships (needs Microsoft's .NET 10 runtime).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"framelab/internal/gamefs"
	"framelab/internal/optimise"
	"framelab/internal/platform"
)

type WBProject struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Build     string   `json:"build"`
	Source    string   `json:"source"` // relative to the game folder: a file, a folder or an .archive
	Kind      string   `json:"kind"`   // files | archive
	Created   string   `json:"created"`
	State     string   `json:"state"`         // ready | unpacking | packing | error: ...
	Log       string   `json:"log,omitempty"` // the last WolvenKit output (end of it)
	Installed string   `json:"installed,omitempty"`
	Files     []WBFile `json:"files,omitempty"` // filled when listed
}

type WBFile struct {
	Path    string `json:"path"` // relative to work\ (files projects: also the path in the game folder)
	Size    int64  `json:"size"`
	Text    bool   `json:"text"`
	Changed bool   `json:"changed"`
	New     bool   `json:"new,omitempty"` // not in original\
}

var (
	wbMu   sync.Mutex
	wbText = map[string]bool{".reds": true, ".lua": true, ".xl": true, ".yaml": true, ".yml": true, ".json": true, ".ini": true, ".txt": true,
		".toml": true, ".csv": true, ".tweak": true, ".md": true, ".script": true, ".inkcharcustomization": false}
	wbRoots = []string{"r6/scripts/", "r6/tweaks/", "archive/pc/mod/", "bin/x64/plugins/cyber_engine_tweaks/mods/", "mods/", "red4ext/plugins/"}
)

func (s *Server) wbDir() string           { return filepath.Join(s.Paths.Data, "workbench") }
func (s *Server) wbPath(id string) string { return filepath.Join(s.wbDir(), id) }
func isTextFile(p string) bool            { return wbText[strings.ToLower(filepath.Ext(p))] }
func wbAllowed(rel string) bool {
	low := strings.ToLower(rel)
	if strings.Contains(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	for _, r := range wbRoots {
		if strings.HasPrefix(low, r) && len(low) > len(r) {
			return true
		}
	}
	return false
}

func (s *Server) wbLoad(id string) (*WBProject, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return nil, errors.New("no such project")
	}
	b, err := os.ReadFile(filepath.Join(s.wbPath(id), "project.json"))
	if err != nil {
		return nil, errors.New("no such project")
	}
	p := &WBProject{}
	return p, json.Unmarshal(b, p)
}

func (s *Server) wbSave(p *WBProject) error {
	p.Files = nil
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(filepath.Join(s.wbPath(p.ID), "project.json"), b, 0o644)
}

// wbList: every project, newest first
func (s *Server) wbList() []WBProject {
	ents, _ := os.ReadDir(s.wbDir())
	out := []WBProject{}
	for _, e := range ents {
		if p, err := s.wbLoad(e.Name()); err == nil {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// wbSources: what a build has that can be opened in the workbench (mod folders and files)
type WBSource struct {
	Rel   string `json:"rel"`
	Kind  string `json:"kind"` // folder | file | archive
	Group string `json:"group"`
	Size  int64  `json:"size,omitempty"`
}

func (s *Server) wbSources(b BuildCfg) []WBSource {
	var out []WBSource
	list := func(dir, group string, folders, files bool, ext ...string) {
		ents, _ := gamefs.ReadDir(filepath.Join(b.Path, filepath.FromSlash(dir)))
		for _, e := range ents {
			rel := dir + e.Name()
			if e.IsDir() && folders {
				out = append(out, WBSource{Rel: rel, Kind: "folder", Group: group})
				continue
			}
			if e.IsDir() || !files {
				continue
			}
			low := strings.ToLower(e.Name())
			for _, x := range ext {
				if strings.HasSuffix(low, x) {
					var size int64
					if fi, err := e.Info(); err == nil {
						size = fi.Size()
					}
					k := "file"
					if x == ".archive" {
						k = "archive"
					}
					out = append(out, WBSource{Rel: rel, Kind: k, Group: group, Size: size})
					break
				}
			}
		}
	}
	list("r6/scripts/", "REDscript", true, true, ".reds")
	list("bin/x64/plugins/cyber_engine_tweaks/mods/", "CET mods", true, false)
	list("r6/tweaks/", "TweakXL", true, true, ".yaml", ".yml", ".tweak")
	list("archive/pc/mod/", "Archives and ArchiveXL", false, true, ".archive", ".xl")
	list("mods/", "REDmod", true, false)
	return out
}

// wbNew makes a project for a file, folder or archive of a build.
func (s *Server) wbNew(i int, src string) (*WBProject, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, err
	}
	src = strings.TrimSuffix(filepath.ToSlash(src), "/")
	if !wbAllowed(src) {
		return nil, errors.New("only mod files and folders can be opened (r6\\scripts, r6\\tweaks, archive\\pc\\mod, CET mods, REDmod)")
	}
	real := filepath.Join(b.Path, filepath.FromSlash(src))
	st, err := gamefs.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("%s isn't in %s", src, b.Name)
	}
	p := &WBProject{ID: time.Now().Format("20060102-150405"), Name: filepath.Base(src), Build: b.Name, Source: src, Kind: "files",
		Created: time.Now().Format("2006-01-02 15:04"), State: "ready"}
	for k := 2; fileExists(s.wbPath(p.ID)); k++ {
		p.ID = time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", k)
	}
	dir := s.wbPath(p.ID)
	if strings.HasSuffix(strings.ToLower(src), ".archive") {
		p.Kind = "archive"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := s.wbSave(p); err != nil {
			return nil, err
		}
		go s.wbUnpack(p.ID, gamefs.Real(real))
		return p, nil
	}
	// files: every text file of the file or folder, at its path in the game folder
	n := 0
	copyOne := func(full string) error {
		rel, err := filepath.Rel(b.Path, full)
		if err != nil {
			return err
		}
		data, err := gamefs.ReadFile(full)
		if err != nil {
			return err
		}
		for _, sub := range []string{"original", "work"} {
			dst := filepath.Join(dir, sub, rel)
			os.MkdirAll(filepath.Dir(dst), 0o755)
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return err
			}
		}
		n++
		return nil
	}
	if st.IsDir() {
		err = gamefs.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !isTextFile(path) {
				return nil
			}
			if fi, e := d.Info(); e == nil && fi.Size() > 4<<20 {
				return nil
			}
			if n >= 500 {
				return errors.New("more than 500 text files: open a smaller folder")
			}
			return copyOne(path)
		})
	} else {
		if !isTextFile(real) {
			return nil, errors.New("not a text file: open its .archive, or a script, tweak, .xl or Lua file")
		}
		err = copyOne(real)
	}
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if n == 0 {
		os.RemoveAll(dir)
		return nil, errors.New("no text files there (scripts, Lua, tweaks, .xl, json, ini)")
	}
	return p, s.wbSave(p)
}

// ---------------------------------------------------------------- WolvenKit console

// wkArgs: the console's command lines. data\workbench\wolvenkit.json can replace them (a later
// WolvenKit may name its options differently); {in} and {out} are filled in.
func (s *Server) wkArgs(step string) []string {
	def := map[string][]string{
		"unbundle":    {"unbundle", "-p", "{in}", "-o", "{out}"},
		"serialize":   {"convert", "serialize", "-p", "{in}", "-o", "{out}"},
		"deserialize": {"convert", "deserialize", "-p", "{in}", "-o", "{out}"},
		"pack":        {"pack", "-p", "{in}", "-o", "{out}"},
	}
	if b, err := os.ReadFile(filepath.Join(s.wbDir(), "wolvenkit.json")); err == nil {
		var m map[string][]string
		if json.Unmarshal(b, &m) == nil && len(m[step]) > 0 {
			return m[step]
		}
	}
	return def[step]
}

// runWKHook stands in for the WolvenKit console in the tests
var runWKHook func(step, in, out string) (string, error)

func (s *Server) runWK(step, in, out string) (string, error) {
	if runWKHook != nil {
		return runWKHook(step, in, out)
	}
	cfg := s.Settings.Get()
	wk := wolvenKit(s.Paths, cfg)
	exe := filepath.Join(wk.Path, "WolvenKit.CLI.exe")
	if !wk.Found || !fileExists(exe) {
		return "", errors.New("the WolvenKit console isn't there (components\\WolvenKit\\WolvenKit.CLI.exe)")
	}
	if wk.DotNet == "" {
		return "", errors.New("the WolvenKit console needs Microsoft's .NET 10 runtime, which isn't on this PC. Get '.NET Runtime 10' (x64) from Microsoft's .NET download page, then try again")
	}
	var args []string
	for _, a := range s.wkArgs(step) {
		args = append(args, strings.NewReplacer("{in}", in, "{out}", out).Replace(a))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = wk.Path
	platform.HideWindow(cmd)
	b, err := cmd.CombinedOutput()
	text := string(b)
	if len(text) > 6000 {
		text = "…" + text[len(text)-6000:]
	}
	if err != nil {
		return text, fmt.Errorf("WolvenKit %s failed: %v", step, err)
	}
	return text, nil
}

func (s *Server) wbSet(id string, f func(p *WBProject)) {
	wbMu.Lock()
	defer wbMu.Unlock()
	if p, err := s.wbLoad(id); err == nil {
		f(p)
		s.wbSave(p)
	}
}

// wbUnpack: archive -> raw\ (game files) -> work\ (readable JSON next to them) -> original\
func (s *Server) wbUnpack(id, archive string) {
	s.wbSet(id, func(p *WBProject) { p.State = "unpacking" })
	dir := s.wbPath(id)
	raw, work := filepath.Join(dir, "raw"), filepath.Join(dir, "work")
	log, err := s.runWK("unbundle", archive, raw)
	if err == nil {
		var l2 string
		l2, err = s.runWK("serialize", raw, work)
		log += "\n" + l2
	}
	if err == nil {
		// files the console couldn't turn into JSON stay as they are (copied, not editable here)
		filepath.WalkDir(raw, func(path string, d fs.DirEntry, e error) error {
			if e != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(raw, path)
			if !fileExists(filepath.Join(work, rel+".json")) && !fileExists(filepath.Join(work, rel)) {
				os.MkdirAll(filepath.Dir(filepath.Join(work, rel)), 0o755)
				copyFile(path, filepath.Join(work, rel))
			}
			return nil
		})
		err = copyTree(work, filepath.Join(dir, "original"))
	}
	s.wbSet(id, func(p *WBProject) {
		p.Log = log
		if err != nil {
			p.State = "error: " + err.Error()
		} else {
			p.State = "ready"
		}
	})
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

// ---------------------------------------------------------------- files

func (s *Server) wbFiles(p *WBProject) []WBFile {
	dir := s.wbPath(p.ID)
	work, orig := filepath.Join(dir, "work"), filepath.Join(dir, "original")
	var out []WBFile
	filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(work, path)
		fi, _ := d.Info()
		f := WBFile{Path: filepath.ToSlash(rel), Text: isTextFile(path)}
		if fi != nil {
			f.Size = fi.Size()
		}
		a, e1 := os.ReadFile(path)
		b, e2 := os.ReadFile(filepath.Join(orig, rel))
		switch {
		case e2 != nil:
			f.New = true
		case e1 == nil && sha(a) != sha(b):
			f.Changed = true
		}
		out = append(out, f)
		return nil
	})
	return out
}

// wbFile: a file inside work\ (the path checked so nothing outside the project can be read or written)
func (s *Server) wbFile(p *WBProject, rel, sub string) (string, error) {
	rel = filepath.FromSlash(rel)
	if rel == "" || strings.Contains(rel, "..") || filepath.IsAbs(rel) {
		return "", errors.New("no such file")
	}
	return filepath.Join(s.wbPath(p.ID), sub, rel), nil
}

// wbRead: a file with its original and the checks
func (s *Server) wbRead(id, rel string) (map[string]any, error) {
	p, err := s.wbLoad(id)
	if err != nil {
		return nil, err
	}
	w, err := s.wbFile(p, rel, "work")
	if err != nil {
		return nil, err
	}
	o, _ := s.wbFile(p, rel, "original")
	b, err := os.ReadFile(w)
	if err != nil {
		return nil, errors.New("no such file")
	}
	if !isTextFile(w) || len(b) > 8<<20 {
		return map[string]any{"path": rel, "binary": true, "size": len(b)}, nil
	}
	ob, _ := os.ReadFile(o)
	gp := s.wbGamePath(p, rel)
	return map[string]any{"path": rel, "text": string(b), "original": string(ob), "kind": optimise.FileKind(gp),
		"lint": optimise.Lint(gp, string(b), string(ob))}, nil
}

// wbGamePath: where a work file belongs in the game folder (for the checks, which look at the path)
func (s *Server) wbGamePath(p *WBProject, rel string) string {
	if p.Kind == "files" {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(rel) // archive files: base\...\x.mesh.json: the extension decides
}

func (s *Server) wbWrite(id, rel, text string) ([]optimise.LintItem, error) {
	p, err := s.wbLoad(id)
	if err != nil {
		return nil, err
	}
	if p.State != "ready" {
		return nil, errors.New("wait until the project is " + map[bool]string{true: "unpacked", false: "ready"}[p.State == "unpacking"])
	}
	w, err := s.wbFile(p, rel, "work")
	if err != nil {
		return nil, err
	}
	if !fileExists(w) || !isTextFile(w) {
		return nil, errors.New("no such text file")
	}
	if err := os.WriteFile(w, []byte(text), 0o644); err != nil {
		return nil, err
	}
	o, _ := s.wbFile(p, rel, "original")
	ob, _ := os.ReadFile(o)
	return optimise.Lint(s.wbGamePath(p, rel), text, string(ob)), nil
}

// wbRevert: the original again
func (s *Server) wbRevert(id, rel string) error {
	p, err := s.wbLoad(id)
	if err != nil {
		return err
	}
	w, err := s.wbFile(p, rel, "work")
	if err != nil {
		return err
	}
	o, _ := s.wbFile(p, rel, "original")
	return copyFile(o, w)
}

// ---------------------------------------------------------------- install

// wbErrors: the error-level checks of every changed file (install waits for them unless forced)
func (s *Server) wbErrors(p *WBProject) []string {
	var out []string
	for _, f := range s.wbFiles(p) {
		if !f.Changed || !f.Text {
			continue
		}
		w, _ := s.wbFile(p, f.Path, "work")
		o, _ := s.wbFile(p, f.Path, "original")
		a, _ := os.ReadFile(w)
		b, _ := os.ReadFile(o)
		for _, x := range optimise.Lint(s.wbGamePath(p, f.Path), string(a), string(b)) {
			if x.Level == "error" {
				out = append(out, fmt.Sprintf("%s, line %d: %s", f.Path, x.Line, x.Text))
			}
		}
	}
	return out
}

func (s *Server) wbInstall(i int, id string, force bool) (*FixRecord, []string, error) {
	p, err := s.wbLoad(id)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.buildAt(i)
	if err != nil {
		return nil, nil, err
	}
	if b.Name != p.Build {
		return nil, nil, fmt.Errorf("this project is for %s: pick that build first", p.Build)
	}
	if p.State != "ready" {
		return nil, nil, errors.New("the project isn't ready (" + p.State + ")")
	}
	if gameRunning(b.Path) {
		return nil, nil, errors.New("close the game first")
	}
	if errs := s.wbErrors(p); len(errs) > 0 && !force {
		return nil, errs, errors.New("fix the errors first")
	}
	var changed []WBFile
	for _, f := range s.wbFiles(p) {
		if f.Changed || f.New {
			changed = append(changed, f)
		}
	}
	if len(changed) == 0 {
		return nil, nil, errors.New("nothing changed yet")
	}
	if p.Kind == "archive" {
		go s.wbPack(i, p.ID)
		return nil, nil, nil
	}
	files := map[string][]byte{}
	for _, f := range changed {
		w, _ := s.wbFile(p, f.Path, "work")
		o, _ := s.wbFile(p, f.Path, "original")
		data, err := os.ReadFile(w)
		if err != nil {
			return nil, nil, err
		}
		// the build's file must still be the one the project started from
		cur, err := gamefs.ReadFile(filepath.Join(b.Path, filepath.FromSlash(f.Path)))
		ob, oerr := os.ReadFile(o)
		if oerr == nil && (err != nil || sha(cur) != sha(ob)) {
			return nil, nil, fmt.Errorf("%s changed in the build since the project was made (an update?): make a new project", f.Path)
		}
		if !wbAllowed(f.Path) {
			return nil, nil, errors.New("not a mod file: " + f.Path)
		}
		files[f.Path] = data
	}
	rec := FixRecord{Kind: "workbench", Mod: p.Name, What: fmt.Sprintf("edited in the Workbench: %d file%s", len(files), map[bool]string{true: "", false: "s"}[len(files) == 1])}
	r, err := s.writeFixFiles(b, rec, files)
	if err == nil {
		s.wbSet(p.ID, func(x *WBProject) { x.Installed = r.ID })
	}
	return r, nil, err
}

// wbPack: changed JSON back into game files, pack, and put the new archive in place of the old one
func (s *Server) wbPack(i int, id string) {
	s.wbSet(id, func(p *WBProject) { p.State = "packing" })
	p, _ := s.wbLoad(id)
	b, berr := s.buildAt(i)
	dir := s.wbPath(id)
	stem := strings.TrimSuffix(filepath.Base(p.Source), filepath.Ext(p.Source))
	build := filepath.Join(dir, "build", stem)
	os.RemoveAll(filepath.Join(dir, "build"))
	os.RemoveAll(filepath.Join(dir, "out"))
	log := ""
	err := berr
	if err == nil {
		err = copyTree(filepath.Join(dir, "raw"), build)
	}
	if err == nil {
		for _, f := range s.wbFiles(p) {
			if !(f.Changed || f.New) {
				continue
			}
			w, _ := s.wbFile(p, f.Path, "work")
			if strings.HasSuffix(strings.ToLower(f.Path), ".json") {
				target := filepath.Dir(filepath.Join(build, filepath.FromSlash(f.Path)))
				var l string
				l, err = s.runWK("deserialize", w, target)
				log += l + "\n"
			} else {
				err = copyFile(w, filepath.Join(build, filepath.FromSlash(f.Path)))
			}
			if err != nil {
				break
			}
		}
	}
	var packed string
	if err == nil {
		var l string
		l, err = s.runWK("pack", build, filepath.Join(dir, "out"))
		log += l
		packed = filepath.Join(dir, "out", stem+".archive")
		if err == nil && !fileExists(packed) {
			err = errors.New("WolvenKit didn't make " + stem + ".archive")
		}
	}
	var rec *FixRecord
	if err == nil && gameRunning(b.Path) {
		err = errors.New("the game was started meanwhile: close it and install again")
	}
	if err == nil {
		var data []byte
		if data, err = os.ReadFile(packed); err == nil {
			r := FixRecord{Kind: "workbench", Mod: p.Name, What: "archive edited in the Workbench and packed again"}
			rec, err = s.writeFixFiles(b, r, map[string][]byte{p.Source: data})
		}
	}
	if rec != nil {
		s.schedChange(b, fixText(rec), rec.ID, false)
	}
	s.wbSet(id, func(x *WBProject) {
		x.Log = log
		if err != nil {
			x.State = "ready"
			x.Log = "Install failed: " + err.Error() + "\n" + log
		} else {
			x.State, x.Installed = "ready", rec.ID
		}
	})
}

func (s *Server) wbDelete(id string) error {
	if _, err := s.wbLoad(id); err != nil {
		return err
	}
	return os.RemoveAll(s.wbPath(id))
}

// wbOpenFile: the project for a file of a build (an open one whose source holds it, or a new one for its
// mod folder), and the file's path inside it
func (s *Server) wbOpenFile(i int, rel string) (*WBProject, string, error) {
	b, err := s.buildAt(i)
	if err != nil {
		return nil, "", err
	}
	rel = filepath.ToSlash(rel)
	for _, p := range s.wbList() {
		if p.Build == b.Name && p.Kind == "files" && (p.Source == rel || strings.HasPrefix(rel, p.Source+"/")) {
			pp := p
			return &pp, rel, nil
		}
	}
	src := rel
	parts := strings.Split(rel, "/")
	switch {
	case strings.HasPrefix(rel, "r6/scripts/") && len(parts) > 3:
		src = strings.Join(parts[:3], "/")
	case strings.HasPrefix(rel, "bin/x64/plugins/cyber_engine_tweaks/mods/") && len(parts) > 6:
		src = strings.Join(parts[:6], "/")
	case strings.HasPrefix(rel, "r6/tweaks/") && len(parts) > 3:
		src = strings.Join(parts[:3], "/")
	}
	p, err := s.wbNew(i, src)
	return p, rel, err
}

// workbenchAPI: the workbench's routes (2.5E). Only workbench/install changes the game build.
func (s *Server) workbenchAPI(path string, body map[string]any) (any, int, error) {
	str := func(k string) string { v, _ := body[k].(string); return v }
	num := func(k string) int { v, _ := body[k].(float64); return int(v) }
	switch path {
	case "workbench":
		out := map[string]any{"projects": s.wbList(), "wolvenkit": wolvenKit(s.Paths, s.Settings.Get())}
		if b, err := s.buildAt(num("build")); err == nil {
			out["sources"] = s.wbSources(b)
			out["build"] = b.Name
		}
		return out, 200, nil
	case "workbench/new":
		p, err := s.wbNew(num("build"), str("source"))
		if err != nil {
			return nil, 422, err
		}
		return p, 200, nil
	case "workbench/open":
		p, rel, err := s.wbOpenFile(num("build"), str("file"))
		if err != nil {
			return nil, 422, err
		}
		return map[string]any{"project": p, "path": rel}, 200, nil
	case "workbench/project":
		p, err := s.wbLoad(str("id"))
		if err != nil {
			return nil, 404, err
		}
		if p.State == "ready" {
			p.Files = s.wbFiles(p)
		}
		return p, 200, nil
	case "workbench/read":
		out, err := s.wbRead(str("id"), str("path"))
		if err != nil {
			return nil, 404, err
		}
		return out, 200, nil
	case "workbench/save":
		lint, err := s.wbWrite(str("id"), str("path"), str("text"))
		if err != nil {
			return nil, 422, err
		}
		return map[string]any{"lint": lint}, 200, nil
	case "workbench/check", "workbench/diff":
		p, err := s.wbLoad(str("id"))
		if err != nil {
			return nil, 404, err
		}
		o, err := s.wbFile(p, str("path"), "original")
		if err != nil {
			return nil, 404, err
		}
		ob, _ := os.ReadFile(o)
		if path == "workbench/diff" {
			return map[string]any{"hunks": optimise.LineDiff(strings.Split(strings.ReplaceAll(string(ob), "\r\n", "\n"), "\n"), strings.Split(strings.ReplaceAll(str("text"), "\r\n", "\n"), "\n"))}, 200, nil
		}
		return map[string]any{"lint": optimise.Lint(s.wbGamePath(p, str("path")), str("text"), string(ob))}, 200, nil
	case "workbench/revert":
		if err := s.wbRevert(str("id"), str("path")); err != nil {
			return nil, 422, err
		}
		return map[string]bool{"ok": true}, 200, nil
	case "workbench/delete":
		if err := s.wbDelete(str("id")); err != nil {
			return nil, 422, err
		}
		return map[string]bool{"ok": true}, 200, nil
	case "workbench/install":
		i := num("build")
		if err := s.schedLock(i); err != nil {
			return nil, 423, err
		}
		force, _ := body["force"].(bool)
		rec, errs, err := s.wbInstall(i, str("id"), force)
		if err != nil {
			if len(errs) > 0 {
				return map[string]any{"errors": errs}, 409, fmt.Errorf("%s: %s", err.Error(), strings.Join(errs, "; "))
			}
			return nil, 422, err
		}
		if rec == nil {
			return map[string]any{"packing": true}, 200, nil // archives: packing runs on, the project shows it
		}
		if b, err := s.buildAt(i); err == nil {
			s.schedChange(b, fixText(rec), rec.ID, false)
		}
		return rec, 200, nil
	}
	return nil, 404, errors.New("unknown workbench call")
}
