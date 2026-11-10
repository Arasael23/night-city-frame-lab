package gamefs

// Mod Organizer 2 instances: ModOrganizer.ini (where the game, the mods, the profiles and the
// overwrite folder are) and the active profile's modlist.txt (which mods are on, in what priority).

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MO2 is one Mod Organizer 2 instance.
type MO2 struct {
	Dir          string   `json:"dir"`  // folder with ModOrganizer.ini
	Name         string   `json:"name"` // instance name (folder name)
	GamePath     string   `json:"game_path"`
	Profile      string   `json:"profile"`
	ModsDir      string   `json:"mods_dir"`
	ProfilesDir  string   `json:"profiles_dir"`
	OverwriteDir string   `json:"overwrite_dir"`
	Mods         []string `json:"-"` // enabled mods of the profile, lowest priority first
	Enabled      int      `json:"enabled"`
	Separators   int      `json:"-"`
}

// IniPath: a path the user gave (the instance folder or ModOrganizer.ini itself) to the ini file.
func IniPath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), `"`)
	if strings.EqualFold(filepath.Base(p), "ModOrganizer.ini") {
		return p
	}
	return filepath.Join(p, "ModOrganizer.ini")
}

// readIni: section -> key (lower case) -> value, Qt's escapes undone.
func readIni(path string) (map[string]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]map[string]string{}
	sec := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' && strings.HasSuffix(line, "]") {
			sec = strings.ToLower(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if out[sec] == nil {
			out[sec] = map[string]string{}
		}
		out[sec][strings.ToLower(strings.TrimSpace(k))] = qtValue(strings.TrimSpace(v))
	}
	return out, sc.Err()
}

// qtValue undoes QSettings' ini quoting: @ByteArray(...), "..." and backslash escapes.
func qtValue(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	if strings.HasPrefix(v, "@ByteArray(") && strings.HasSuffix(v, ")") {
		v = v[len("@ByteArray(") : len(v)-1]
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\\' && i+1 < len(v) {
			i++
			switch v[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"', '\\':
				b.WriteByte(v[i])
			default:
				b.WriteByte('\\')
				b.WriteByte(v[i])
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ReadMO2 reads an instance: its folders, the selected profile and that profile's enabled mods.
func ReadMO2(dir string) (*MO2, error) {
	ini := IniPath(dir)
	dir = filepath.Dir(ini)
	s, err := readIni(ini)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no ModOrganizer.ini in %s", dir)
		}
		return nil, err
	}
	g, set := s["general"], s["settings"]
	m := &MO2{Dir: dir, Name: filepath.Base(dir), GamePath: filepath.Clean(fromIni(g["gamepath"])), Profile: g["selected_profile"]}
	if m.Profile == "" {
		m.Profile = "Default"
	}
	base := fromIni(set["base_directory"])
	if base == "" {
		base = dir
	}
	at := func(key, def string) string {
		v := fromIni(set[key])
		if v == "" {
			v = filepath.Join(base, def)
		}
		v = replaceFold(v, "%BASE_DIR%", base)
		return filepath.Clean(v)
	}
	m.ModsDir = at("mod_directory", "mods")
	m.ProfilesDir = at("profiles_directory", "profiles")
	m.OverwriteDir = at("overwrite_directory", "overwrite")
	if err := m.readModlist(); err != nil {
		return m, err
	}
	return m, nil
}

func fromIni(v string) string { return filepath.FromSlash(strings.TrimSpace(v)) }

func replaceFold(s, old, new string) string {
	if i := strings.Index(strings.ToUpper(s), old); i >= 0 {
		return s[:i] + new + s[i+len(old):]
	}
	return s
}

// readModlist: profiles\<profile>\modlist.txt lists the mods highest priority first; "+" on,
// "-" off, "*" unmanaged (files already in the game folder). Separators are mods with no files.
func (m *MO2) readModlist() error {
	p := filepath.Join(m.ProfilesDir, m.Profile, "modlist.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("can't read the mod list of profile %q (%s): %w", m.Profile, p, err)
	}
	var on []string
	for _, line := range strings.Split(strings.TrimPrefix(string(b), "\ufeff"), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 2 || line[0] != '+' {
			continue
		}
		name := line[1:]
		if strings.HasSuffix(name, "_separator") {
			m.Separators++
			continue
		}
		on = append(on, name)
	}
	// lowest priority first
	for i := len(on) - 1; i >= 0; i-- {
		m.Mods = append(m.Mods, on[i])
	}
	m.Enabled = len(m.Mods)
	return nil
}

// sig changes when the instance or its mod list changes.
func sigOf(dir string) string {
	ini := IniPath(dir)
	var parts []string
	if st, err := os.Stat(ini); err == nil {
		parts = append(parts, fmt.Sprint(st.ModTime().UnixNano(), st.Size()))
	}
	if m, err := ReadMO2(dir); err == nil {
		if st, err := os.Stat(filepath.Join(m.ProfilesDir, m.Profile, "modlist.txt")); err == nil {
			parts = append(parts, m.Profile, fmt.Sprint(st.ModTime().UnixNano(), st.Size()))
		}
	}
	return strings.Join(parts, "|")
}

// NexusID: the Nexus Mods ID MO2 saved for a mod (meta.ini modid), or "".
func (m *MO2) NexusID(mod string) string {
	if m == nil || mod == "" {
		return ""
	}
	s, err := readIni(filepath.Join(m.ModsDir, mod, "meta.ini"))
	if err != nil {
		return ""
	}
	if id := strings.TrimSpace(s["general"]["modid"]); id != "" && id != "0" && id != "-1" {
		return id
	}
	return ""
}

// SameGame: the instance runs the game in this folder.
func (m *MO2) SameGame(game string) bool {
	return m != nil && m.GamePath != "" && strings.EqualFold(filepath.Clean(m.GamePath), filepath.Clean(game))
}

// FindMO2 lists the instances on this PC that run the game in this folder: MO2's global
// instances (%LOCALAPPDATA%\ModOrganizer), the folder of a running ModOrganizer.exe and the usual
// places people put a portable MO2.
func FindMO2(game string, extra []string) []*MO2 {
	var dirs []string
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		if ents, err := os.ReadDir(filepath.Join(la, "ModOrganizer")); err == nil {
			for _, e := range ents {
				if e.IsDir() {
					dirs = append(dirs, filepath.Join(la, "ModOrganizer", e.Name()))
				}
			}
		}
	}
	dirs = append(dirs, extra...)
	if game != "" {
		parent := filepath.Dir(filepath.Clean(game))
		for _, n := range []string{"MO2", "Mod Organizer 2", "ModOrganizer", "ModOrganizer2"} {
			dirs = append(dirs, filepath.Join(parent, n), filepath.Join(game, n))
		}
	}
	seen := map[string]bool{}
	var out []*MO2
	for _, d := range dirs {
		k := strings.ToLower(filepath.Clean(d))
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, err := os.Stat(IniPath(d)); err != nil {
			continue
		}
		m, err := ReadMO2(d)
		if m == nil || (err != nil && m.ModsDir == "") {
			continue
		}
		if game == "" || m.SameGame(game) {
			out = append(out, m)
		}
	}
	return out
}
