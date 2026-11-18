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
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Build       string    `json:"build"`
	Game        string    `json:"game"`
	Created     time.Time `json:"created"`
	QpcFreq     int64     `json:"qpc_freq"`
	AnchorQpc   int64     `json:"anchor_qpc"` // QPC and Unix ms read together by Frame Lab
	AnchorMs    float64   `json:"anchor_unix_ms"`
	SpikeMs     float64   `json:"spike_ms"`
	Layers      LayerInfo `json:"layers"`
	Notes       []string  `json:"notes,omitempty"`
	GPU         string    `json:"gpu,omitempty"`
	GpuVendor   string    `json:"gpu_vendor,omitempty"`  // "nvidia", "amd", "intel" (1.1+; empty = NVIDIA in older sessions)
	GpuSource   string    `json:"gpu_source,omitempty"`  // where the card's readings came from
	GpuReads    []string  `json:"gpu_reads,omitempty"`   // which readings the card's driver gave
	Experiments []string  `json:"experiments,omitempty"` // Windows settings from the Experiments tab that were on
	Finished    bool      `json:"finished"`
	FeltKey     string    `json:"felt_key,omitempty"` // name of the "I felt a stutter" key (2.5; empty = F10)
	MarkKey     string    `json:"mark_key,omitempty"` // name of the "mark a part" key (2.5; empty = F11)
	Test        bool      `json:"test,omitempty"`     // a test recording for the Optimise schedule: script time is measured for the whole run, not only in marked parts (2.5)
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
	FileFrames     = "frames.bin" // 2.5.2: every frame (time, frame time) for the numbers of any part of the session
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

// Keys: names of the session's stutter and mark keys (F10 / F11 before 2.5).
func (m *Meta) Keys() (felt, mark string) {
	felt, mark = "F10", "F11"
	if m == nil {
		return
	}
	if m.FeltKey != "" {
		felt = m.FeltKey
	}
	if m.MarkKey != "" {
		mark = m.MarkKey
	}
	return
}
