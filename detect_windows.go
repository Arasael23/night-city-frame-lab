//go:build windows

package platform

// Where Cyberpunk 2077 is installed: Steam (its library folders), GOG (Galaxy's registry entries),
// Epic (the launcher's install manifests) and the running game. Read-only.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func regString(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_32KEY)
	if err != nil {
		k, err = registry.OpenKey(root, path, registry.QUERY_VALUE)
		if err != nil {
			return ""
		}
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}

var reVdfPath = regexp.MustCompile(`"path"\s+"([^"]+)"`)

func steamLibraries() []string {
	var roots []string
	for _, r := range []string{
		regString(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath"),
		regString(registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath"),
	} {
		if r != "" {
			roots = append(roots, filepath.Clean(strings.ReplaceAll(r, "/", `\`)))
		}
	}
	libs := append([]string(nil), roots...)
	for _, r := range roots {
		b, err := os.ReadFile(filepath.Join(r, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range reVdfPath.FindAllSubmatch(b, -1) {
			libs = append(libs, filepath.Clean(strings.ReplaceAll(string(m[1]), `\\`, `\`)))
		}
	}
	return libs
}

func gogGames() []string {
	var out []string
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\GOG.com\Games`, registry.ENUMERATE_SUB_KEYS|registry.WOW64_32KEY)
	if err != nil {
		return nil
	}
	defer k.Close()
	subs, _ := k.ReadSubKeyNames(-1)
	for _, s := range subs {
		base := `SOFTWARE\GOG.com\Games\` + s
		name := regString(registry.LOCAL_MACHINE, base, "gameName")
		if s != "1423049311" && !strings.Contains(strings.ToLower(name), "cyberpunk 2077") {
			continue
		}
		if p := regString(registry.LOCAL_MACHINE, base, "path"); p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

func epicGames() []string {
	var out []string
	dir := filepath.Join(os.Getenv("ProgramData"), "Epic", "EpicGamesLauncher", "Data", "Manifests")
	items, _ := filepath.Glob(filepath.Join(dir, "*.item"))
	for _, it := range items {
		b, err := os.ReadFile(it)
		if err != nil {
			continue
		}
		var m struct {
			DisplayName     string
			InstallLocation string
		}
		if json.Unmarshal(b, &m) == nil && strings.Contains(strings.ToLower(m.DisplayName), "cyberpunk 2077") && m.InstallLocation != "" {
			out = append(out, filepath.Clean(m.InstallLocation))
		}
	}
	return out
}

// GameInstallHints: what Steam, GOG, Epic and the running game say.
func GameInstallHints() []InstallHint {
	var out []InstallHint
	for _, lib := range steamLibraries() {
		out = append(out, InstallHint{"Steam", filepath.Join(lib, "steamapps", "common", "Cyberpunk 2077")})
	}
	for _, p := range gogGames() {
		out = append(out, InstallHint{"GOG", p})
	}
	for _, p := range epicGames() {
		out = append(out, InstallHint{"Epic", p})
	}
	for _, g := range FindGame() {
		if g.Path != "" { // ...\bin\x64\Cyberpunk2077.exe
			out = append(out, InstallHint{"", filepath.Dir(filepath.Dir(filepath.Dir(g.Path)))})
		}
	}
	return out
}

// Drives: the fixed drives' roots ("C:\", "D:\", ...).
func Drives() []string {
	var out []string
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return []string{`C:\`}
	}
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if t := windows.GetDriveType(p); t == windows.DRIVE_FIXED || t == windows.DRIVE_REMOVABLE {
			out = append(out, root)
		}
	}
	return out
}
