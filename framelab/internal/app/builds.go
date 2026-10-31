package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

)

type HitchInfo struct {
	Installed    bool    `json:"installed"`
	Version      string  `json:"version"`
	Mode         int     `json:"mode"` // 0 always, 1 F11
	Recordings   int     `json:"recordings"`
	RecordingsMB float64 `json:"recordings_mb"`
	Folder       string  `json:"folder"`
}

var reHitchVer = regexp.MustCompile(`HITCHREC_VERSION=(\d+\.\d+\.\d+)`)

func HitchStatus(game string) HitchInfo {
	dir := filepath.Join(game, "red4ext", "plugins", "HitchRecorder")
	h := HitchInfo{Folder: dir, Mode: 1}
	b, err := os.ReadFile(filepath.Join(dir, "HitchRecorder.dll"))
	if err != nil {
		return h
	}
	h.Installed = true
	if m := reHitchVer.FindSubmatch(b); m != nil {
		h.Version = string(m[1])
	} else {
		h.Version = "0.1"
		h.Mode = 0 // 0.1 had no capture key
	}
	if ini, err := os.ReadFile(filepath.Join(dir, "HitchRecorder.ini")); err == nil && h.Version != "0.1" {
		for _, line := range strings.Split(string(ini), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "capturemode=") {
				v, _ := strconv.Atoi(strings.TrimSpace(line[len("capturemode="):]))
				h.Mode = v
			}
		}
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "recordings"))
	for _, e := range ents {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			if strings.HasSuffix(e.Name(), ".bin") {
				h.Recordings++
			}
			h.RecordingsMB += float64(info.Size()) / 1048576
		}
	}
	return h
}

type BuildState struct {
	Index       int       `json:"index"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Exists      bool      `json:"exists"`
	GameVersion string    `json:"game_version"`
	Hitch       HitchInfo `json:"hitch"`
	CET         bool      `json:"cet"` // CET in the build: CET Lua layer + loading-screen markers
	ModArchives int       `json:"mod_archives"`
	Leftover    bool      `json:"leftover"` // HitchRecorder / markers in the build outside a session
}

var (
	verCache   = map[string]string{}
	verCacheMu sync.Mutex
)

// gameVersion reads the product version from Cyberpunk2077.exe's version resource (cached by size and time).
func gameVersion(exe string) string {
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	key := fmt.Sprintf("%s|%d|%d", exe, st.Size(), st.ModTime().Unix())
	verCacheMu.Lock()
	if v, ok := verCache[key]; ok {
		verCacheMu.Unlock()
		return v
	}
	verCacheMu.Unlock()
	v := "?"
	if b, err := os.ReadFile(exe); err == nil {
		sig := []byte{0xBD, 0x04, 0xEF, 0xFE}
		if i := bytes.LastIndex(b, sig); i >= 0 && i+24 <= len(b) {
			ms := binary.LittleEndian.Uint32(b[i+16:]) // dwProductVersionMS
			v = fmt.Sprintf("%d.%d", ms>>16, ms&0xFFFF)
		}
	}
	verCacheMu.Lock()
	verCache[key] = v
	verCacheMu.Unlock()
	return v
}

func BuildStates(c Config) []BuildState {
	var out []BuildState
	for i, b := range c.Builds {
		bs := BuildState{Index: i, Name: b.Name, Path: b.Path}
		exe := filepath.Join(b.Path, "bin", "x64", "Cyberpunk2077.exe")
		if _, err := os.Stat(exe); err == nil {
			bs.Exists = true
			bs.GameVersion = gameVersion(exe)
			bs.Hitch = HitchStatus(b.Path)
			bs.Leftover = SessionPartsLeft(b.Path)
			bs.CET = HasCET(b.Path)
			m, _ := filepath.Glob(filepath.Join(b.Path, "archive", "pc", "mod", "*.archive"))
			bs.ModArchives = len(m)
		}
		out = append(out, bs)
	}
	return out
}

// HasCET: Cyber Engine Tweaks is in the build (HitchRecorder can time its mods; the markers need it).
func HasCET(game string) bool {
	return fileExists(filepath.Join(game, "bin", "x64", "plugins", "cyber_engine_tweaks.asi"))
}
