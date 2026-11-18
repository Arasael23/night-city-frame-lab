package app

// WolvenKit (2.5). Frame Lab brings the WolvenKit console along, unchanged, in components\WolvenKit
// (GPL-3.0; its licence and where its source is are in that folder). Two uses:
//   - the game's file names: WolvenKit.Common.dll holds WolvenKit's list of them, which Frame Lab reads
//     straight out of the file (nothing of WolvenKit runs for that, so it needs no .NET)
//   - unpacking and packing mod archives with WolvenKit.CLI.exe, which is a .NET program: that needs
//     Microsoft's .NET 10 runtime on the PC
// A WolvenKit install of the player's own (Settings > WolvenKit folder) is only used when the bundled
// copy is missing.

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// WolvenKitInfo: the WolvenKit Frame Lab uses.
type WolvenKitInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`    // the folder
	Bundled bool   `json:"bundled"` // Frame Lab's own copy in components\WolvenKit
	CLI     bool   `json:"cli"`     // WolvenKit.CLI.exe is there (the console)
	DotNet  string `json:"dotnet"`  // the .NET 10 runtime on this PC ("10.0.1"), "" when there is none
}

func bundledWolvenKit(p Paths) string { return filepath.Join(p.Components, "WolvenKit") }

// wolvenKit: the bundled console when it is there, else the folder from Settings.
func wolvenKit(p Paths, cfg Config) WolvenKitInfo {
	out := WolvenKitInfo{Path: cfg.WolvenKit, DotNet: dotNetRuntime(10)}
	for i, dir := range []string{bundledWolvenKit(p), cfg.WolvenKit} {
		if dir != "" && fileExists(filepath.Join(dir, "WolvenKit.Common.dll")) {
			out.Found, out.Path, out.Bundled = true, dir, i == 0
			out.CLI = fileExists(filepath.Join(dir, "WolvenKit.CLI.exe"))
			break
		}
	}
	return out
}

// dotNetRuntime: the newest installed .NET runtime of a major version ("" when none). Looked up where
// Microsoft's installer puts it; nothing is started.
func dotNetRuntime(major int) string {
	var found []string
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if root == "" {
			continue
		}
		ents, _ := os.ReadDir(filepath.Join(root, "dotnet", "shared", "Microsoft.NETCore.App"))
		for _, e := range ents {
			if e.IsDir() && strings.HasPrefix(e.Name(), strconv.Itoa(major)+".") {
				found = append(found, e.Name())
			}
		}
	}
	if len(found) == 0 {
		return ""
	}
	sort.Slice(found, func(i, j int) bool { return verLess(found[i], found[j]) })
	return found[len(found)-1]
}

// verLess: "10.0.2" < "10.0.10" (dotted numbers; anything after a dash sorts first)
func verLess(a, b string) bool {
	pa, pb := strings.Split(strings.SplitN(a, "-", 2)[0], "."), strings.Split(strings.SplitN(b, "-", 2)[0], ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return strings.Contains(a, "-") && !strings.Contains(b, "-")
}
