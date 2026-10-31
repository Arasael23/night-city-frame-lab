// Package analysis puts every recorded layer of one Frame Lab capture on one clock and works out
// what made each slow frame slow.
package analysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Meta is session.json, written by the capture side.
type Meta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Build     string    `json:"build"`
	Game      string    `json:"game"`
	Created   time.Time `json:"created"`
	QpcFreq   int64     `json:"qpc_freq"`
	AnchorQpc int64     `json:"anchor_qpc"`  // QPC and Unix ms read together by Frame Lab
	AnchorMs  float64   `json:"anchor_unix_ms"`
	SpikeMs   float64   `json:"spike_ms"`
	Layers    LayerInfo `json:"layers"`
	Notes     []string  `json:"notes,omitempty"`
	GPU       string    `json:"gpu,omitempty"`
	Experiments []string `json:"experiments,omitempty"` // Windows settings from the Experiments tab that were on
	Finished  bool      `json:"finished"`
}

type LayerInfo struct {
	Hitch      bool   `json:"hitch"`
	PresentMon bool   `json:"presentmon"`
	Samples    bool   `json:"samples"`
	Scripts    bool   `json:"scripts"` // REDscript + CET time per mod (HitchRecorder, F11 parts)
	HitchVer   string `json:"hitch_version,omitempty"`
}

const (
	FileMeta       = "session.json"
	FileAnalysis   = "analysis.json"
	FileHitch      = "hitch.bin"
	FileHitchLog   = "hitch.log"
	FilePresentMon = "presentmon.csv"
	FileSamples    = "samples.csv"
)

func ReadMeta(dir string) (*Meta, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileMeta))
	if err != nil {
		return nil, err
	}
	m := &Meta{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

func WriteMeta(dir string, m *Meta) error {
	b, _ := json.MarshalIndent(m, "", " ")
	tmp := filepath.Join(dir, FileMeta+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, FileMeta))
}
