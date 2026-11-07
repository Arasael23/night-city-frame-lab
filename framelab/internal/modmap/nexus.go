package modmap

// Nexus Mods IDs for archives, so a mod can link to its page: from the name of the download it came
// from (library zip, or the mod folder Vortex made from the download).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Nexus names downloads "<name>-<mod id>-<version, dots as dashes>-<upload time>.<ext>"
	reNexusDl = regexp.MustCompile(`^(.+?)-(\d{2,6})-(?:[0-9a-zA-Z]+-)*\d{9,10}(?:\.[A-Za-z0-9]{1,4})?$`)
	// libraries that keep the ID as its own word: "Advanced Crash Reporter 34403 1.4.0 ..."
	reNexusWord = regexp.MustCompile(`(?:^|\s)(\d{3,6})\s+v?\d`)
)

// NexusID: the mod ID in a download's file name, or "".
func NexusID(name string) string {
	b := filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if m := reNexusDl.FindStringSubmatch(b); m != nil && strings.TrimSpace(m[1]) != "" {
		return m[2]
	}
	if m := reNexusWord.FindStringSubmatch(strings.TrimSuffix(b, filepath.Ext(b))); m != nil {
		return m[1]
	}
	return ""
}

// VortexSources: the files Vortex deployed into a build, each with the mod folder it came from
// (named after the download). Vortex keeps a vortex.deployment.json in each folder it deploys to.
// Keys: lower-case paths relative to the game folder, with forward slashes.
func VortexSources(game string) map[string]string {
	out := map[string]string{}
	for _, dir := range []string{"", filepath.Join("archive", "pc", "mod")} {
		b, err := os.ReadFile(filepath.Join(game, dir, "vortex.deployment.json"))
		if err != nil {
			continue
		}
		var m struct {
			Files []struct {
				RelPath string `json:"relPath"`
				Source  string `json:"source"`
			} `json:"files"`
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		for _, f := range m.Files {
			rel := strings.ToLower(strings.ReplaceAll(filepath.Join(dir, f.RelPath), `\`, "/"))
			if f.Source != "" {
				out[rel] = f.Source
			}
		}
	}
	return out
}
