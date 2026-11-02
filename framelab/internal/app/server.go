package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"framelab/internal/analysis"
	"framelab/internal/modmap"
	"framelab/internal/platform"
)

type Server struct {
	Paths    Paths
	Settings *Settings
	Ctl      *Controller
	UI       fs.FS
	Token    string
	URL      string

	namesMu    sync.Mutex
	namesState string // "", "building", "ready", "error: ..."
}

func NewServer(p Paths, ui fs.FS) *Server {
	b := make([]byte, 16)
	rand.Read(b)
	s := &Server{Paths: p, UI: ui, Token: hex.EncodeToString(b)}
	platform.SetDataDir(p.Data)
	s.Settings = LoadSettings(p)
	s.Ctl = NewController(p, s.Settings)
	// 1.2.1: build the file-name list by itself the first time (reports show paths instead of numbers)
	if cfg := s.Settings.Get(); !modmap.NamesFile(filepath.Join(p.Data, "names.txt.gz")).Exists() &&
		fileExists(filepath.Join(cfg.WolvenKit, "WolvenKit.Common.dll")) {
		s.buildNames()
	}
	return s
}

// Start listens on 127.0.0.1 (random port) and returns the URL for the window.
func (s *Server) Start(addr string) (string, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.api)
	mux.Handle("/", http.FileServer(http.FS(s.UI)))
	go http.Serve(ln, noCache(mux))
	s.URL = fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), s.Token)
	return s.URL, nil
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Token") != s.Token {
		writeErr(w, http.StatusForbidden, errors.New("bad token"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	var body map[string]any
	if r.Method == http.MethodPost {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	}
	str := func(k string) string { v, _ := body[k].(string); return v }
	num := func(k string) float64 { v, _ := body[k].(float64); return v }

	switch {
	case path == "state":
		writeJSON(w, s.state())
	case path == "arm" && r.Method == http.MethodPost:
		if err := s.Ctl.Arm(str("title"), int(num("build"))); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "disarm" && r.Method == http.MethodPost:
		if err := s.Ctl.Disarm(); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "reset" && r.Method == http.MethodPost:
		s.Ctl.Reset()
		writeJSON(w, map[string]bool{"ok": true})
	case path == "sessions":
		writeJSON(w, s.Ctl.Sessions())
	case strings.HasPrefix(path, "session/"):
		s.sessionAPI(w, r, strings.TrimPrefix(path, "session/"), body)
	case path == "settings" && r.Method == http.MethodPost:
		var c Config
		b, _ := json.Marshal(body)
		if err := json.Unmarshal(b, &c); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.Settings.Set(c); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, s.Settings.Get())
	case path == "leftover/remove" && r.Method == http.MethodPost:
		// HitchRecorder / markers found in a build outside a session (e.g. an old manual install)
		cfg := s.Settings.Get()
		i := int(num("build"))
		if i < 0 || i >= len(cfg.Builds) {
			writeErr(w, 400, errors.New("no game build picked yet: Settings > Find game installs, or Add a build"))
			return
		}
		if s.Ctl.Armed() {
			writeErr(w, 400, errors.New("a session is set up: use Cancel and remove on the Capture tab"))
			return
		}
		if err := RemoveSessionParts(cfg.Builds[i].Path); err != nil {
			writeErr(w, 400, err)
			return
		}
		buildsMu.Lock()
		buildsCacheAt = time.Time{} // show the change straight away
		buildsMu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
	case path == "names/build" && r.Method == http.MethodPost:
		s.buildNames()
		writeJSON(w, map[string]bool{"ok": true})
	case path == "conflicts" && r.Method == http.MethodPost:
		out, err := s.conflictReport(int(num("build")))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, out)
	case path == "conflicts/check" && r.Method == http.MethodPost:
		if err := s.checkConflicts(int(num("build"))); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "vram":
		out, err := s.vramReport(int(num("build")))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, out)
	case path == "vram/scan" && r.Method == http.MethodPost:
		if err := s.scanVram(int(num("build"))); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "builds/detect" && r.Method == http.MethodPost:
		writeJSON(w, map[string]any{"found": detectBuilds(s.Settings.Get().Builds)})
	case path == "gpuapps":
		writeJSON(w, gpuAppsReport(s.Paths.Data))
	case path == "gpuapps/set" && r.Method == http.MethodPost:
		on, _ := body["on"].(bool)
		if err := setGpuApp(s.Paths.Data, str("path"), on); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "gpuapps/all" && r.Method == http.MethodPost:
		moved, err := moveSuggested(s.Paths.Data)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"moved": moved})
	case path == "experiments":
		game := s.buildPath(int(num("build")))
		writeJSON(w, map[string]any{"list": experimentStates(s.Paths.Data, game), "game": game})
	case path == "experiments/set" && r.Method == http.MethodPost:
		on, _ := body["on"].(bool)
		if err := setExperiment(s.Paths.Data, s.buildPath(int(num("build"))), str("id"), on); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "experiments/run" && r.Method == http.MethodPost:
		msg, err := runExperiment(s.Paths.Data, s.buildPath(int(num("build"))), str("id"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]string{"message": msg})
	case path == "open" && r.Method == http.MethodPost:
		target := ""
		switch str("what") {
		case "sessions":
			target = s.Paths.Sessions
		case "session":
			target, _ = s.Ctl.SessionDir(str("id"))
		case "hitch":
			cfg := s.Settings.Get()
			if i := int(num("build")); i >= 0 && i < len(cfg.Builds) {
				target = filepath.Join(cfg.Builds[i].Path, "red4ext", "plugins", "HitchRecorder")
			}
		}
		if target != "" {
			platform.OpenFolder(target)
		}
		writeJSON(w, map[string]bool{"ok": target != ""})
	default:
		writeErr(w, 404, errors.New("unknown call "+path))
	}
}

func (s *Server) sessionAPI(w http.ResponseWriter, r *http.Request, rest string, body map[string]any) {
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	dir, err := s.Ctl.SessionDir(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	switch action {
	case "":
		p := filepath.Join(dir, analysis.FileAnalysis)
		if b, err := os.ReadFile(p); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
			return
		}
		res, err := s.Ctl.Analyse(id)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, res)
	case "analyse":
		res, err := s.Ctl.Analyse(id)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, res)
	case "rename":
		m, err := analysis.ReadMeta(dir)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if t, _ := body["title"].(string); strings.TrimSpace(t) != "" {
			m.Title = strings.TrimSpace(t)
			analysis.WriteMeta(dir, m)
			os.Remove(filepath.Join(dir, analysis.FileAnalysis))
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "delete":
		if r.Method != http.MethodPost {
			writeErr(w, 405, errors.New("POST only"))
			return
		}
		if err := os.RemoveAll(dir); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "trace":
		t, _ := body["t"].(float64)
		span, _ := body["span"].(float64)
		out, err := analysis.ExportTrace(dir, t, span, modmap.NamesFile(filepath.Join(s.Paths.Data, "names.txt.gz")))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]string{"path": out})
	case "export":
		out, err := s.exportReport(dir)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, map[string]string{"path": out})
	default:
		writeErr(w, 404, errors.New("unknown action"))
	}
}

// ---------------------------------------------------------------- state for the window

type stateOut struct {
	Version    string          `json:"version"`
	Admin      bool            `json:"admin"`
	GPU        string          `json:"gpu"`
	GpuInfo    platform.GpuInfo `json:"gpu_info"`
	Config     Config          `json:"config"`
	Builds     []BuildState    `json:"builds"`
	Components map[string]any  `json:"components"`
	Game       map[string]any  `json:"game"`
	Live       map[string]any  `json:"live"`
	Capture    CaptureStatus   `json:"capture"`
	Names      string          `json:"names"`
	Time       string          `json:"time"`
}

var (
	buildsCache   []BuildState
	buildsCacheAt time.Time
	buildsMu      sync.Mutex
)

func (s *Server) state() stateOut {
	cfg := s.Settings.Get()
	comp := FindComponents(s.Paths, cfg)
	buildsMu.Lock()
	if time.Since(buildsCacheAt) > 3*time.Second {
		buildsCache, buildsCacheAt = BuildStates(cfg), time.Now()
	}
	builds := buildsCache
	buildsMu.Unlock()
	if builds == nil {
		builds = []BuildState{} // [] not null: no install found yet
	}
	if cfg.Builds == nil {
		cfg.Builds = []BuildCfg{}
	}
	st := stateOut{Version: Version, Admin: platform.IsAdmin(), GPU: platform.GPUName(), GpuInfo: platform.GPU(), Config: cfg, Builds: builds,
		Capture: s.Ctl.Status(), Time: time.Now().Format("15:04:05")}
	st.Components = map[string]any{
		"presentmon": map[string]any{"found": comp.PresentMon != "", "path": comp.PresentMon, "name": filepath.Base(comp.PresentMon)},
		"hitch_pkg":  map[string]any{"found": comp.HitchPkg != "", "version": bundledHitchVersion(s.Paths)},
		"markers":    map[string]any{"found": comp.Markers != ""},
		"library":    map[string]any{"found": dirExists(cfg.Library), "path": cfg.Library},
		"wolvenkit":  map[string]any{"found": fileExists(filepath.Join(cfg.WolvenKit, "WolvenKit.Common.dll")), "path": cfg.WolvenKit},
	}
	s.namesMu.Lock()
	st.Names = s.namesState
	s.namesMu.Unlock()
	if st.Names == "" && modmap.NamesFile(filepath.Join(s.Paths.Data, "names.txt.gz")).Exists() {
		st.Names = "ready"
	}
	games := platform.FindGame()
	st.Game = map[string]any{"running": len(games) > 0}
	if len(games) > 0 {
		st.Game["pid"] = games[0].Pid
		st.Game["path"] = games[0].Path
		bi := -1
		for i, b := range cfg.Builds {
			if strings.HasPrefix(strings.ToLower(games[0].Path), strings.ToLower(filepath.Clean(b.Path))+string(os.PathSeparator)) {
				bi = i
			}
		}
		st.Game["build"] = bi
	}
	st.Live = map[string]any{}
	if len(games) > 0 {
		if hs := platform.ReadHitchState(); hs.Valid {
			st.Live["hitch"] = hs
		}
	}
	s.Ctl.mu.Lock()
	smp := s.Ctl.sampler
	s.Ctl.mu.Unlock()
	if smp != nil {
		st.Live["pc"] = smp.Last()
	} else if g := platform.LiveGPU(); g != nil {
		st.Live["pc"] = g
	}
	return st
}

func bundledHitchVersion(p Paths) string {
	b, err := os.ReadFile(filepath.Join(p.Components, "HitchRecorder", "red4ext", "plugins", "HitchRecorder", "HitchRecorder.dll"))
	if err != nil {
		return ""
	}
	if m := reHitchVer.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return "?"
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (s *Server) buildNames() {
	s.namesMu.Lock()
	if s.namesState == "building" {
		s.namesMu.Unlock()
		return
	}
	s.namesState = "building"
	s.namesMu.Unlock()
	go func() {
		cfg := s.Settings.Get()
		wk := filepath.Join(cfg.WolvenKit, "WolvenKit.Common.dll")
		oodle := ""
		for _, b := range cfg.Builds {
			if p := filepath.Join(b.Path, "bin", "x64", "oo2ext_7_win64.dll"); fileExists(p) {
				oodle = p
				break
			}
		}
		state := "ready"
		switch {
		case !fileExists(wk):
			state = "error: WolvenKit.Common.dll not found in " + cfg.WolvenKit
		case oodle == "":
			state = "error: oo2ext_7_win64.dll not found in any game build"
		default:
			if err := platform.ExtractNames(wk, oodle, filepath.Join(s.Paths.Data, "names.txt.gz")); err != nil {
				state = "error: " + err.Error()
			}
		}
		s.namesMu.Lock()
		s.namesState = state
		s.namesMu.Unlock()
	}()
}

// exportReport writes one self-contained HTML file (the report view + the session's data) into the session folder.
func (s *Server) exportReport(dir string) (string, error) {
	ana, err := os.ReadFile(filepath.Join(dir, analysis.FileAnalysis))
	if err != nil {
		if _, err := s.Ctl.Analyse(filepath.Base(dir)); err != nil {
			return "", err
		}
		if ana, err = os.ReadFile(filepath.Join(dir, analysis.FileAnalysis)); err != nil {
			return "", err
		}
	}
	read := func(n string) string { b, _ := fs.ReadFile(s.UI, n); return string(b) }
	html := read("index.html")
	css := read("uPlot.min.css") + "\n" + read("style.css")
	// The window loads its fonts from fonts/; a saved report has no folder next to it, so put them inside.
	for _, w := range []string{"Light", "Regular", "Medium", "SemiBold"} {
		ref := `url("fonts/Inter-` + w + `.woff2")`
		if b, err := fs.ReadFile(s.UI, "fonts/Inter-"+w+".woff2"); err == nil {
			css = strings.Replace(css, ref, `url("data:font/woff2;base64,`+base64.StdEncoding.EncodeToString(b)+`")`, 1)
		}
	}
	js := read("uPlot.iife.min.js") + "\n" + read("app.js")
	safeData := strings.ReplaceAll(string(ana), "</", "<\\/")
	html = strings.Replace(html, `<link rel="stylesheet" href="uPlot.min.css">`, "", 1)
	html = strings.Replace(html, `<link rel="stylesheet" href="style.css">`, "<style>\n"+css+"\n</style>", 1)
	html = strings.Replace(html, `<script src="uPlot.iife.min.js"></script>`, "", 1)
	html = strings.Replace(html, `<script src="app.js"></script>`,
		"<script>window.FL_REPORT = "+safeData+";</script>\n<script>\n"+strings.ReplaceAll(js, "</script", "<\\/script")+"\n</script>", 1)
	m, _ := analysis.ReadMeta(dir)
	name := "Frame Lab report.html"
	if m != nil && m.Title != "" {
		name = "Frame Lab report - " + strings.TrimSpace(unsafeChars.ReplaceAllString(m.Title, " ")) + ".html"
	}
	out := filepath.Join(dir, name)
	return out, os.WriteFile(out, []byte(html), 0o644)
}
