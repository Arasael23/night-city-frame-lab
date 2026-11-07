package analysis

// Session snapshot (2.0): what the PC and the build looked like when the session started, so
// Compare can say what changed between two sessions (mods, game settings, versions).

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const FileSnapshot = "snapshot.json"

type ModFile struct {
	Path  string `json:"path"`  // relative to the game folder
	Size  int64  `json:"size"`  // bytes (folders: all files inside)
	MTime int64  `json:"mtime"` // Unix seconds (folders: newest file)
}

type Snapshot struct {
	Game     string            `json:"game_version,omitempty"`
	Driver   string            `json:"driver,omitempty"`
	Windows  string            `json:"windows,omitempty"`
	Settings map[string]string `json:"settings,omitempty"` // UserSettings.json, "group/name" -> value
	Mods     []ModFile         `json:"mods,omitempty"`
}

func WriteSnapshot(dir string, s *Snapshot) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileSnapshot), b, 0o644)
}

func ReadSnapshot(dir string) *Snapshot {
	b, err := os.ReadFile(filepath.Join(dir, FileSnapshot))
	if err != nil {
		return nil
	}
	var s Snapshot
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}
