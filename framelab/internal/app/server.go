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
	"framelab/internal/optimise"
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
	platform.PawnIOModules = filepath.Join(p.Components, "PawnIO")
	s.Settings = LoadSettings(p)
	s.Ctl = NewController(p, s.Settings)
	s.Ctl.RoutesFor = s.schedRoutes
	if Experimental {
		go pinCoresLoop(p.Data) // Experiments: keep the game on the performance cores
	}
	// 1.2.1: build the file-name list by itself the first time (reports show paths instead of numbers)
	// (2.5: from the WolvenKit console Frame Lab brings along, so it works without a WolvenKit install)
	if !modmap.NamesFile(filepath.Join(p.Data, "names.txt.gz")).Exists() && wolvenKit(p, s.Settings.Get()).Found {
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

// settingPages: the Windows pages a do-it-yourself step can open (2.5)
var settingPages = map[string]string{
	"captures":      "ms-settings:gaming-captures",
	"gamebar":       "ms-settings:gaming-gamebar",
	"gamemode":      "ms-settings:gaming-gamemode",
	"graphics":      "ms-settings:display-advancedgraphics",
	"display":       "ms-settings:display",
	"power":         "ms-settings:powersleep",
	"startup":       "ms-settings:startupapps",
	"update":        "ms-settings:windowsupdate",
	"coreisolation": "windowsdefender://coreisolation",
	"defender":      "windowsdefender://threatsettings",
	"pagefile":      "SystemPropertiesPerformance.exe",
	"apps":          "ms-settings:appsfeatures",
}

func str0(body map[string]any, k string) string { v, _ := body[k].(string); return v }

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
	if !Experimental && r.Method == http.MethodPost && (writerRoute(path) || path == "checks/act" && writerCheck(str0(body, "id"))) {
		writeErr(w, http.StatusForbidden, errors.New(onlyE))
		return
	}
	str := func(k string) string { v, _ := body[k].(string); return v }
	num := func(k string) float64 { v, _ := body[k].(float64); return v }

	switch {
	case path == "state":
		writeJSON(w, s.state())
	case path == "arm" && r.Method == http.MethodPost:
		routes, opts := s.schedSession(int(num("build")))
		if err := s.Ctl.Arm(str("title"), int(num("build")), routes, opts); err != nil {
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
	case path == "settings/info":
		// 2.5: the keys Settings offers and each build's MO2 instance (or the ones found for it)
		writeJSON(w, map[string]any{"hotkeys": HotKeys, "mo2": mo2States(s.Settings.Get())})
	case path == "leftover/remove" && r.Method == http.MethodPost:
		// HitchRecorder / markers found in a build outside a session (e.g. an old manual install)
		cfg := s.Settings.Get()
		i := int(num("build"))
		if i < 0 || i >= len(cfg.Builds) {
			writeErr(w, 400, errors.New("no game build picked yet: Settings > Find game installs, or Add a build"))
			return
		}
		if s.Ctl.Armed() {
			writeErr(w, 400, errors.New("a session is set up: use Cancel and remove on the Record page"))
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
	case path == "optimise" && r.Method == http.MethodPost:
		out, err := s.optimiseReport(int(num("build")))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, out)
	case Experimental && path == "optimise/fix" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		i := int(num("build"))
		// the schedule decides: no fix before the build is measured, none while a retest is running
		if err := s.schedLock(i); err != nil {
			writeErr(w, 423, err)
			return
		}
		var rec *FixRecord
		var err error
		if str("kind") == "recipe" {
			rec, err = s.applyRecipe(i, str("recipe"))
		} else {
			rec, err = s.applyFix(i, str("kind"), str("mod"), str("file"), int(num("line")), int(num("hz")))
		}
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		if b, err := s.buildAt(i); err == nil {
			s.schedChange(b, fixText(rec), rec.ID, false)
		}
		s.checkOptimise(i) // the report shows it as slowed down after the re-check
		writeJSON(w, rec)
	case path == "precheck/search" && r.Method == http.MethodPost: // both editions, read only
		writeJSON(w, map[string]any{"mods": optimise.FindMods(s.optimiseIndex(), s.corpus(), str("q"), 25)})
	case path == "precheck" && r.Method == http.MethodPost:
		pc, err := s.preCheck(int(num("build")), int(num("nexus")))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, pc)
	case Experimental && strings.HasPrefix(path, "workbench") && r.Method == http.MethodPost: // 2.5E only
		out, code, err := s.workbenchAPI(path, body)
		if err != nil {
			writeErr(w, code, err)
			return
		}
		writeJSON(w, out)
	case Experimental && path == "conflicts/fix" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		i := int(num("build"))
		if err := s.schedLock(i); err != nil {
			writeErr(w, 423, err)
			return
		}
		var rec *FixRecord
		var err error
		switch str("kind") {
		case "archive_win":
			rec, err = s.applyArchiveWin(i, str("winner"), str("loser"))
		case "tweak_pick":
			rec, err = s.applyTweakPick(i, str("key"), str("value"))
		case "xl_cooked_type", "file_off":
			rec, err = s.applyLogFix(i, str("kind"), str("file"), int(num("index")))
		default:
			err = errors.New("unknown fix")
		}
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		if b, err := s.buildAt(i); err == nil {
			s.schedChange(b, fixText(rec), rec.ID, false)
			s.checkConflicts(i) // the Conflicts page shows the new state
		}
		writeJSON(w, rec)
	case Experimental && path == "optimise/undo" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		// an undo is always possible; it is a change like any other, so the retest starts over.
		// Only while a game option is on trial it has to wait: dropping the trial is one click and always possible.
		if st, err := s.scheduleStatus(int(num("build"))); err == nil && st.Trial != nil {
			writeErr(w, 423, fmt.Errorf("A game option is on trial (%s), and a trial is one change on its own. Drop the trial or finish it, then undo.", st.Trial.Name))
			return
		}
		rec, err := s.undoFix(str("id"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		i := int(num("build"))
		if b, err := s.buildAt(i); err == nil {
			s.schedChange(b, undoText(rec), rec.ID, true)
		}
		s.checkOptimise(i)
		writeJSON(w, map[string]bool{"ok": true})
	case path == "optimise/check" && r.Method == http.MethodPost:
		i := int(num("build"))
		if err := s.schedOpen(i); err != nil {
			writeErr(w, 423, err)
			return
		}
		if err := s.checkOptimise(i); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case Experimental && path == "optimise/option/try" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		if s.Ctl.Armed() {
			writeErr(w, 400, errors.New("a session is set up: finish or cancel it on the Record page first"))
			return
		}
		if err := s.optionTry(int(num("build")), str("id")); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case Experimental && path == "optimise/option/drop" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		if s.Ctl.Armed() {
			writeErr(w, 400, errors.New("a session is set up: finish or cancel it on the Record page first"))
			return
		}
		if err := s.optionDrop(int(num("build"))); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case Experimental && path == "optimise/option/keep" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		if s.Ctl.Armed() {
			writeErr(w, 400, errors.New("a session is set up: finish or cancel it on the Record page first"))
			return
		}
		if err := s.optionKeep(int(num("build"))); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case Experimental && path == "optimise/option/remove" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		if s.Ctl.Armed() {
			writeErr(w, 400, errors.New("a session is set up: finish or cancel it on the Record page first"))
			return
		}
		if err := s.optionRemove(int(num("build")), str("id")); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case Experimental && path == "conflicts/merge" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		j, err := s.mergeStart(int(num("build")), str("winner"), str("loser"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, j)
	case Experimental && path == "conflicts/merge/status" && r.Method == http.MethodPost:
		writeJSON(w, map[string]any{"job": s.mergeStatus(int(num("build")), str("winner"), str("loser"))})
	case Experimental && path == "conflicts/workbench" && r.Method == http.MethodPost: // both archives of a clash, side by side
		ids, err := s.mergeWorkbench(int(num("build")), str("winner"), str("loser"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"ids": ids})
	case Experimental && path == "gameopts" && r.Method == http.MethodPost: // 2.5E only: the Game options page
		st, err := s.gameOptsStatus(int(num("build")))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, st)
	case Experimental && path == "gameopts/set" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		st, err := s.gameOptSet(int(num("build")), str("id"), str("value"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, st)
	case Experimental && path == "gameopts/mod" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		on, _ := body["on"].(bool)
		st, err := s.gameOptsMod(int(num("build")), on)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, st)
	case Experimental && path == "optimise/schedule" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		st, err := s.scheduleStatus(int(num("build")))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, st)
	case Experimental && path == "optimise/schedule/start" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		if s.Ctl.Armed() {
			writeErr(w, 400, errors.New("a session is set up: finish or cancel it on the Record page first"))
			return
		}
		if err := s.scheduleStart(int(num("build"))); err != nil {
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
	case path == "vram/lighter" && r.Method == http.MethodPost: // both editions: what Frame Lab knows, read only
		i := int(num("build"))
		info, _, err := s.lighterInfo(i, str("mod"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		if Experimental {
			if st, err := s.scheduleStatus(i); err == nil && st.Locked {
				info.Locked = st.Why
			}
		}
		writeJSON(w, info)
	case Experimental && path == "vram/lighter/apply" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		i := int(num("build"))
		if err := s.schedLock(i); err != nil {
			writeErr(w, 423, err)
			return
		}
		rec, err := s.applySwap(i, str("mod"), str("zip"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		if b, err := s.buildAt(i); err == nil {
			s.schedChange(b, fixText(rec), rec.ID, false)
		}
		s.scanVram(i)
		writeJSON(w, rec)
	case path == "vram/scan" && r.Method == http.MethodPost:
		if err := s.scanVram(int(num("build"))); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "pickfolder" && r.Method == http.MethodPost:
		// Settings' Browse buttons: the Windows folder picker; a build folder is checked and tidied
		p, err := platform.PickFolder(str("title"), str("start"))
		if errors.Is(err, platform.ErrPickCancelled) {
			writeJSON(w, map[string]any{"path": ""})
			return
		}
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		out := map[string]any{"path": p}
		if str("kind") == "build" {
			root := gameRoot(p)
			out["path"] = root
			out["game"] = isGameDir(root)
			taken := map[string]bool{}
			for _, b := range s.Settings.Get().Builds {
				taken[strings.ToLower(b.Name)] = true
			}
			out["name"] = buildName(storeOf(root), root, taken)
		}
		writeJSON(w, out)
	case path == "builds/detect" && r.Method == http.MethodPost:
		writeJSON(w, map[string]any{"found": detectBuilds(s.Settings.Get().Builds)})
	case path == "gpuapps":
		writeJSON(w, gpuAppsReport(s.Paths.Data))
	case path == "gpuapps/set" && r.Method == http.MethodPost: // both editions: a per-program Windows graphics preference, exact Undo
		on, _ := body["on"].(bool)
		if err := setGpuApp(s.Paths.Data, str("path"), on); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "gpuapps/all" && r.Method == http.MethodPost: // both editions
		moved, err := moveSuggested(s.Paths.Data)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"moved": moved})
	case path == "experiments":
		game := s.buildPath(int(num("build")))
		nv, nvErr := nvProfile()
		writeJSON(w, map[string]any{"list": experimentStates(s.Paths.Data, game), "game": game, "groups": ExpGroups,
			"nvprofile": nv, "nverror": nvErr, "cpu": platform.CPU()})
	case Experimental && path == "experiments/set" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		on, _ := body["on"].(bool)
		if err := setExperiment(s.Paths.Data, s.buildPath(int(num("build"))), str("id"), on); err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case Experimental && path == "experiments/run" && r.Method == http.MethodPost: // 2.5E only (edition.go)
		msg, err := runExperiment(s.Paths.Data, s.buildPath(int(num("build"))), str("id"))
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]string{"message": msg})
	case path == "checks" && r.Method == http.MethodPost:
		fresh, _ := body["fresh"].(bool)
		rep, err := s.checks(int(num("build")), fresh)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, map[string]any{"report": rep, "undo": undoList(s.Paths.Data)})
	case path == "checks/act" && r.Method == http.MethodPost:
		msg, err := s.checkAction(int(num("build")), str("id"))
		checksForget()
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]string{"message": msg})
	case path == "changes":
		writeJSON(w, changesMade(s.Paths.Data))
	case path == "changes/undo" && r.Method == http.MethodPost:
		n, errs := putEverythingBack(s.Paths.Data)
		checksForget()
		writeJSON(w, map[string]any{"undone": n, "errors": errs})
	case path == "undo":
		writeJSON(w, undoList(s.Paths.Data))
	case path == "undo/everything" && r.Method == http.MethodPost:
		n, errs := undoEverything(s.Paths.Data)
		writeJSON(w, map[string]any{"undone": n, "errors": errs})
	case path == "undo/one" && r.Method == http.MethodPost:
		err := undoOne(s.Paths.Data, str("id"))
		checksForget()
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
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
		if str("what") == "setting" {
			// 2.5: a do-it-yourself step's button opens the exact Windows page (fixed list, nothing else)
			u, ok := settingPages[str("id")]
			if ok {
				platform.OpenURL(u)
			}
			writeJSON(w, map[string]bool{"ok": ok})
			return
		}
		if str("what") == "buildfile" {
			// a folder inside a game build (a mod's folder, the folder of a log or settings file)
			cfg := s.Settings.Get()
			if i := int(num("build")); i >= 0 && i < len(cfg.Builds) {
				root := filepath.Clean(cfg.Builds[i].Path)
				t := filepath.Clean(filepath.Join(root, filepath.FromSlash(str("rel"))))
				if t == root || strings.HasPrefix(strings.ToLower(t), strings.ToLower(root)+string(os.PathSeparator)) {
					if fi, err := os.Stat(t); err == nil && !fi.IsDir() {
						t = filepath.Dir(t)
					}
					target = t
				}
			}
		}
		if str("what") == "usersettings" {
			target = filepath.Dir(platform.UserSettingsPath())
		}
		if str("what") == "nexusfiles" {
			// a Nexus mod's Files tab, optionally at one file: digits only
			id, fid := str("id"), str("file")
			digits := func(x string) bool { return strings.Trim(x, "0123456789") == "" }
			ok := id != "" && len(id) <= 7 && digits(id) && len(fid) <= 9 && digits(fid)
			if ok {
				u := "https://www.nexusmods.com/cyberpunk2077/mods/" + id + "?tab=files"
				if fid != "" {
					u += "&file_id=" + fid
				}
				platform.OpenURL(u)
			}
			writeJSON(w, map[string]bool{"ok": ok})
			return
		}
		if str("what") == "nexus" {
			// a Nexus mod page: the ID only, digits, so nothing else can be opened this way
			id := str("id")
			ok := id != "" && len(id) <= 7 && strings.Trim(id, "0123456789") == ""
			if ok {
				platform.OpenURL("https://www.nexusmods.com/cyberpunk2077/mods/" + id)
			}
			writeJSON(w, map[string]bool{"ok": ok})
			return
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
		b, err := s.Ctl.CurrentAnalysis(id)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	case "frames":
		// 2.5.2: every frame of the session (frames.bin), for the numbers of the part on screen
		b, err := os.ReadFile(filepath.Join(dir, analysis.FileFrames))
		if err != nil {
			writeErr(w, 404, errors.New("this session has no frame list yet: Re-analyse it"))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(b)
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
		if act := s.Ctl.ActiveSession(); act != "" && filepath.Clean(act) == filepath.Clean(dir) {
			writeErr(w, 409, errors.New("this session is still set up on the Record page: cancel it there first (that also removes it when nothing was recorded)"))
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
	Version      string            `json:"version"`
	Edition      string            `json:"edition"`      // "2.5" or "2.5E" (edition.go)
	Experimental bool              `json:"experimental"` // 2.5E: Experiments + Optimise
	Admin        bool              `json:"admin"`
	GPU          string            `json:"gpu"`
	GpuInfo      platform.GpuInfo  `json:"gpu_info"`
	Config       Config            `json:"config"`
	Builds       []BuildState      `json:"builds"`
	Components   map[string]any    `json:"components"`
	Game         map[string]any    `json:"game"`
	Live         map[string]any    `json:"live"`
	Capture      CaptureStatus     `json:"capture"`
	Names        string            `json:"names"`
	Time         string            `json:"time"`
	Keys         map[string]string `json:"keys"` // names of the session keys: felt (F10), mark (F11) (2.5)
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
	st := stateOut{Version: Version, Edition: Edition(), Experimental: Experimental, Admin: platform.IsAdmin(), GPU: platform.GPUName(), GpuInfo: platform.GPU(), Config: cfg, Builds: builds,
		Capture: s.Ctl.Status(), Time: time.Now().Format("15:04:05")}
	fk, mk := cfg.Keys()
	st.Keys = map[string]string{"felt": KeyName(fk), "mark": KeyName(mk)}
	st.Components = map[string]any{
		"presentmon": map[string]any{"found": comp.PresentMon != "", "path": comp.PresentMon, "name": filepath.Base(comp.PresentMon)},
		"hitch_pkg":  map[string]any{"found": comp.HitchPkg != "", "version": bundledHitchVersion(s.Paths)},
		"markers":    map[string]any{"found": comp.Markers != "" || comp.MarkersLua != ""},
		"library":    map[string]any{"found": dirExists(cfg.Library), "path": cfg.Library},
		"wolvenkit":  wolvenKit(s.Paths, cfg),
		"mod_index":  map[string]any{"found": fileExists(optimiseIndexPath(s.Paths)), "path": optimiseIndexPath(s.Paths)},
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
		wkInfo := wolvenKit(s.Paths, cfg)
		wk := filepath.Join(wkInfo.Path, "WolvenKit.Common.dll")
		oodle := ""
		for _, b := range cfg.Builds {
			if p := filepath.Join(b.Path, "bin", "x64", "oo2ext_7_win64.dll"); fileExists(p) {
				oodle = p
				break
			}
		}
		state := "ready"
		switch {
		case !wkInfo.Found:
			state = "error: WolvenKit.Common.dll not found in components\\WolvenKit" + map[bool]string{true: " or " + cfg.WolvenKit, false: ""}[cfg.WolvenKit != ""]
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
	ana, err := s.Ctl.CurrentAnalysis(filepath.Base(dir))
	if err != nil {
		return "", err
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
	frames := ""
	if b, err := os.ReadFile(filepath.Join(dir, analysis.FileFrames)); err == nil {
		frames = "<script>window.FL_FRAMES = \"" + base64.StdEncoding.EncodeToString(b) + "\";</script>\n"
	}
	html = strings.Replace(html, `<link rel="stylesheet" href="uPlot.min.css">`, "", 1)
	html = strings.Replace(html, `<link rel="stylesheet" href="style.css">`, "<style>\n"+css+"\n</style>", 1)
	html = strings.Replace(html, `<script src="uPlot.iife.min.js"></script>`, "", 1)
	html = strings.Replace(html, `<script src="app.js"></script>`,
		"<script>window.FL_REPORT = "+safeData+";</script>\n"+frames+"<script>\n"+strings.ReplaceAll(js, "</script", "<\\/script")+"\n</script>", 1)
	m, _ := analysis.ReadMeta(dir)
	name := "Frame Lab report.html"
	if m != nil && m.Title != "" {
		name = "Frame Lab report - " + strings.TrimSpace(unsafeChars.ReplaceAllString(m.Title, " ")) + ".html"
	}
	out := filepath.Join(dir, name)
	return out, os.WriteFile(out, []byte(html), 0o644)
}
