package analysis

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Other programs on the graphics card (1.4+): every 2 s while the game runs, the sampler writes
// how much graphics memory each other program holds on the game's graphics card (Windows'
// "GPU Process Memory" counters). gpu_apps.csv: qpc,pid,name,dedicated_mb,shared_mb,path
// One row per program with 20 MB or more, plus a "_all_others" row with the total of every
// program but the game (also the small ones).

const (
	FileGpuApps   = "gpu_apps.csv"
	gpuAppsTotal  = "_all_others"
	gpuAppsMinRow = 20.0
)

// GpuAppClass says whether a program belongs on the main graphics card, and why.
//
//	game     the game
//	keep     needs the main card: recorder / streaming, the graphics driver's own tools, video editors, overlays
//	windows  part of Windows: draws the desktop on the card the screen is plugged into
//	store    Microsoft Store app: Windows keeps its setting under the app's name, not its file
//	suggest  doesn't need the main card (browser, chat, launcher, music): fine on integrated graphics
//	other    not on Frame Lab's list
func GpuAppClass(name, path string) (class, reason string) {
	n := strings.ToLower(name)
	p := strings.ToLower(path)
	if r, ok := gpuKeep[n]; ok {
		if n == "cyberpunk2077.exe" {
			return "game", r
		}
		return "keep", r
	}
	if r, ok := gpuWindows[n]; ok {
		return "windows", r
	}
	if p != "" {
		win := strings.ToLower(os.Getenv("SystemRoot"))
		if win == "" {
			win = `c:\windows`
		}
		if strings.HasPrefix(p, win+`\`) {
			return "windows", "part of Windows"
		}
		if strings.Contains(p, `\windowsapps\`) {
			return "store", "Microsoft Store app: set it in Windows Settings (Graphics)"
		}
	}
	if r, ok := gpuSuggest[n]; ok {
		return "suggest", r
	}
	if n == "msedgewebview2.exe" {
		return "other", "Edge WebView2: shared by many apps (Widgets, Teams, Frame Lab's own window) and its folder changes with every Edge update"
	}
	return "other", "not on Frame Lab's list: move it only if it isn't a game, a recorder or a video tool"
}

var reVersioned = regexp.MustCompile(`\\app-\d|\\\d+\.\d+\.\d+[.\d]*\\`)

// GpuPathVersioned: the program lives in a folder named after its version, so the Windows
// setting (kept per file path) has to be made again after it updates.
func GpuPathVersioned(path string) bool { return reVersioned.MatchString(path) }

var gpuKeep = map[string]string{
	"cyberpunk2077.exe":        "the game",
	"redprelauncher.exe":       "the game's launcher",
	"obs64.exe":                "recording / streaming: keep it on the card the game is on",
	"obs32.exe":                "recording / streaming: keep it on the card the game is on",
	"obs-browser-page.exe":     "OBS browser sources: keep with OBS",
	"streamlabs obs.exe":       "recording / streaming",
	"streamlabs desktop.exe":   "recording / streaming",
	"xsplit.core.exe":          "recording / streaming",
	"medal.exe":                "recording",
	"nvidia app.exe":           "NVIDIA's own app",
	"nvidia overlay.exe":       "NVIDIA overlay (recording, instant replay, performance overlay)",
	"nvidia share.exe":         "NVIDIA recording / instant replay",
	"nvidia web helper.exe":    "NVIDIA's own app",
	"nvcontainer.exe":          "NVIDIA driver service",
	"nvdisplay.container.exe":  "NVIDIA driver service",
	"nvsphelper64.exe":         "NVIDIA recording",
	"nvcplui.exe":              "NVIDIA Control Panel",
	"radeonsoftware.exe":       "AMD Software (Adrenalin)",
	"amdrsserv.exe":            "AMD recording / instant replay",
	"amdow.exe":                "AMD overlay",
	"atieclxx.exe":             "AMD driver service",
	"resolve.exe":              "video editing",
	"adobe premiere pro.exe":   "video editing",
	"afterfx.exe":              "video editing",
	"capframex.exe":            "frame time capture",
	"presentmon-2.6.0-x64.exe": "Frame Lab's frame recorder",
	"rtss.exe":                 "RivaTuner overlay (hooks into the game)",
	"msiafterburner.exe":       "MSI Afterburner",
}

var gpuWindows = map[string]string{
	"dwm.exe":                     "Windows: draws the desktop and every window, on the card the screen is plugged into",
	"csrss.exe":                   "part of Windows",
	"explorer.exe":                "Windows Explorer / taskbar",
	"searchhost.exe":              "Windows search",
	"startmenuexperiencehost.exe": "Windows Start menu",
	"shellexperiencehost.exe":     "Windows shell",
	"textinputhost.exe":           "Windows text input",
	"applicationframehost.exe":    "part of Windows",
	"lockapp.exe":                 "Windows lock screen",
	"system":                      "part of Windows",
	"widgets.exe":                 "Windows Widgets",
	"phoneexperiencehost.exe":     "Windows Phone Link",
	"gamebar.exe":                 "Xbox Game Bar",
	"gamebarftserver.exe":         "Xbox Game Bar",
}

var gpuSuggest = map[string]string{
	"chrome.exe":              "browser",
	"msedge.exe":              "browser",
	"vivaldi.exe":             "browser",
	"firefox.exe":             "browser",
	"brave.exe":               "browser",
	"opera.exe":               "browser",
	"discord.exe":             "chat / voice (its folder changes with each update)",
	"discordptb.exe":          "chat / voice",
	"discordcanary.exe":       "chat / voice",
	"claude.exe":              "desktop app",
	"spotify.exe":             "music",
	"ms-teams.exe":            "chat / calls",
	"teams.exe":               "chat / calls",
	"slack.exe":               "chat",
	"whatsapp.exe":            "chat",
	"telegram.exe":            "chat",
	"signal.exe":              "chat",
	"steam.exe":               "launcher",
	"steamwebhelper.exe":      "Steam's window (launcher)",
	"galaxyclient.exe":        "GOG Galaxy (launcher; the game itself is set on its own)",
	"galaxyclient helper.exe": "GOG Galaxy's window (launcher)",
	"epicgameslauncher.exe":   "launcher",
	"epicwebhelper.exe":       "launcher",
	"eadesktop.exe":           "launcher",
	"battle.net.exe":          "launcher",
	"upc.exe":                 "Ubisoft Connect (launcher)",
	"code.exe":                "code editor",
	"notion.exe":              "notes",
	"obsidian.exe":            "notes",
	"wallpaper64.exe":         "animated wallpaper",
	"wallpaper32.exe":         "animated wallpaper",
	"lghub.exe":               "Logitech G HUB",
	"icue.exe":                "Corsair iCUE",
	"signalrgb.exe":           "RGB lighting",
}

// GpuAppUse: one program over the time played.
type GpuAppUse struct {
	Name    string  `json:"name"`
	TypMB   float64 `json:"typ_mb"` // median while playing (counting the times it wasn't running as 0)
	MaxMB   float64 `json:"max_mb"`
	SeenPct float64 `json:"seen_pct"` // share of the play time it held 20 MB or more
	Class   string  `json:"class"`
	Reason  string  `json:"reason"`
	Shared  bool    `json:"shared,omitempty"` // its number includes the game's own memory (overlay / desktop): more than everything else on the card together
}

type GpuAppsSummary struct {
	Recorded     bool        `json:"recorded"`
	OthersTypMB  float64     `json:"others_typ_mb"` // everything but the game on the card (whole card minus the game), median while playing
	OthersMaxMB  float64     `json:"others_max_mb"`
	MovableTypMB float64     `json:"movable_typ_mb"` // the part held by programs that could run on integrated graphics
	Apps         []GpuAppUse `json:"apps"`
}

// Windows counts every piece of graphics memory a program has opened, also the game's own that an
// overlay or the desktop compositor (dwm) holds a handle to. Those programs then show more than the
// whole card minus the game, which can't be theirs (1.4.0 added them up: "37.9 GB" on a 16 GB card).
const gpuSharedMargin = 50.0

type gpuAppRow struct {
	q          int64
	name, path string
	ded, sh    float64
}

func readGpuApps(dir string) ([]gpuAppRow, error) {
	f, err := os.Open(filepath.Join(dir, FileGpuApps))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []gpuAppRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		p := strings.SplitN(strings.TrimRight(sc.Text(), "\r"), ",", 6)
		if len(p) < 5 {
			continue
		}
		q, err := strconv.ParseInt(p[0], 10, 64)
		if err != nil {
			continue
		}
		d, _ := strconv.ParseFloat(p[3], 64)
		s, _ := strconv.ParseFloat(p[4], 64)
		r := gpuAppRow{q: q, name: p[2], ded: d, sh: s}
		if len(p) == 6 {
			r.path = p[5]
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].q < out[j].q })
	return out, sc.Err()
}

// gpuAppsLayer: the total of other programs per sample time, and the summary over the time played.
type gpuAppsLayer struct {
	t     []int64   // sample times (qpc)
	total []float64 // MB held by every program but the game
}

// at: the total at (or just before) q
func (l *gpuAppsLayer) at(q int64) (float64, bool) {
	i := sort.Search(len(l.t), func(i int) bool { return l.t[i] > q }) - 1
	if i < 0 {
		return 0, false
	}
	return l.total[i], true
}

// loadGpuApps: othTyp / othMax are everything but the game on the card while playing (whole card
// minus the game, from the samples); 0 = not known, then the programs' own total is used.
func loadGpuApps(dir string, inWin, playing func(int64) bool, othTyp, othMax float64) (*gpuAppsLayer, *GpuAppsSummary) {
	rows, err := readGpuApps(dir)
	if err != nil || len(rows) == 0 {
		return nil, nil
	}
	l := &gpuAppsLayer{}
	type per struct {
		path string
		at   map[int64]float64
	}
	apps := map[string]*per{}
	var playT []int64
	var totPlay []float64
	for i := 0; i < len(rows); {
		q := rows[i].q
		j := i
		for j < len(rows) && rows[j].q == q {
			j++
		}
		if inWin(q) {
			for _, r := range rows[i:j] {
				if r.name == gpuAppsTotal {
					l.t = append(l.t, q)
					l.total = append(l.total, r.ded)
					if playing(q) {
						totPlay = append(totPlay, r.ded)
					}
				}
			}
			if playing(q) {
				playT = append(playT, q)
				for _, r := range rows[i:j] {
					if r.name == gpuAppsTotal {
						continue
					}
					k := strings.ToLower(r.name)
					a := apps[k]
					if a == nil {
						a = &per{path: r.path, at: map[int64]float64{}}
						apps[k] = a
					}
					a.at[q] += r.ded
				}
			}
		}
		i = j
	}
	s := &GpuAppsSummary{Recorded: true}
	if othTyp > 0 {
		s.OthersTypMB, s.OthersMaxMB = math.Round(othTyp), math.Round(othMax)
	} else if len(totPlay) > 0 {
		srt := append([]float64(nil), totPlay...)
		sort.Float64s(srt)
		s.OthersTypMB, s.OthersMaxMB = math.Round(pctl(srt, 50)), math.Round(srt[len(srt)-1])
	}
	names := map[string]string{} // lower -> as written
	for _, r := range rows {
		names[strings.ToLower(r.name)] = r.name
	}
	for k, a := range apps {
		vals := make([]float64, len(playT))
		seen, mx := 0, 0.0
		for i, q := range playT {
			vals[i] = a.at[q]
			if vals[i] >= gpuAppsMinRow {
				seen++
			}
			mx = math.Max(mx, vals[i])
		}
		sort.Float64s(vals)
		cl, why := GpuAppClass(names[k], a.path)
		u := GpuAppUse{Name: names[k], TypMB: math.Round(pctl(vals, 50)), MaxMB: math.Round(mx), Class: cl, Reason: why}
		if len(playT) > 0 {
			u.SeenPct = math.Round(100 * float64(seen) / float64(len(playT)))
		}
		if u.MaxMB < gpuAppsMinRow {
			continue
		}
		if othTyp > 0 && u.TypMB > othTyp+gpuSharedMargin {
			u.Shared = true
		}
		if (cl == "suggest" || cl == "other") && !u.Shared {
			s.MovableTypMB += u.TypMB
		}
		s.Apps = append(s.Apps, u)
	}
	sort.Slice(s.Apps, func(i, j int) bool {
		if s.Apps[i].TypMB != s.Apps[j].TypMB {
			return s.Apps[i].TypMB > s.Apps[j].TypMB
		}
		return s.Apps[i].MaxMB > s.Apps[j].MaxMB
	})
	if len(s.Apps) > 15 {
		s.Apps = s.Apps[:15]
	}
	return l, s
}
