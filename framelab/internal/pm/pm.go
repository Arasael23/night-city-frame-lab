// Package pm reads PresentMon console CSV output (1.x, 2.x and 3.x column names).
package pm

import (
	"bufio"
	"encoding/csv"
	"io"
	"os"
	"strconv"
	"strings"
)

type Frame struct {
	Qpc       int64   // CPU start of the frame (QPC), or -1 when the CSV has no QPC column
	TimeS     float64 // seconds since PresentMon started (fallback clock)
	FrameMs   float64 // time from this frame's CPU start to the next one (app frame time)
	CPUBusyMs float64
	GPUBusyMs float64
	GPUTimeMs float64
	DisplayMs float64 // how long it stayed on screen (NaN when not displayed)
	Generated bool    // made by frame generation (DLSS FG etc.), not the game
	Dropped   bool
	InputMs   float64 // input-to-photon latency of this frame (-1 = none: no input that frame or no column)
}

type Capture struct {
	Process   string
	Frames    []Frame // all frames of the main swap chain, in order
	HasQpc    bool
	HasGPU    bool
	Generated int // number of generated frames
	Columns   []string
}

func num(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "nan" || s == "NaN" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func Read(path, process string) (*Capture, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, process)
}

// Parse keeps the rows of `process` (exe name, case-insensitive; empty = all) and, if the process
// presented to several swap chains, only the busiest one.
func Parse(r io.Reader, process string) (*Capture, error) {
	cr := csv.NewReader(bufio.NewReaderSize(r, 1<<20))
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = false
	head, err := cr.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range head {
		idx[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	col := func(names ...string) int {
		for _, n := range names {
			if i, ok := idx[n]; ok {
				return i
			}
		}
		return -1
	}
	cApp := col("Application")
	cSwap := col("SwapChainAddress")
	cQpc := col("CPUStartQPC")
	cTime := col("CPUStartTime", "TimeInSeconds")
	cFrame := col("MsBetweenAppStart", "FrameTime", "MsBetweenPresents")
	cCPU := col("MsCPUBusy", "CPUBusy")
	cGPU := col("MsGPUBusy", "GPUBusy", "MsGPUActive")
	cGPUT := col("MsGPUTime", "GPUTime")
	cDisp := col("MsBetweenDisplayChange", "DisplayedTime")
	cType := col("FrameType")
	cDrop := col("Dropped")
	cInput := col("MsAllInputToPhotonLatency", "MsClickToPhotonLatency")

	type sc struct{ frames []Frame }
	bySwap := map[string]*sc{}
	cap := &Capture{Process: process, HasQpc: cQpc >= 0, HasGPU: cGPU >= 0, Columns: head}
	get := func(rec []string, c int) string {
		if c < 0 || c >= len(rec) {
			return ""
		}
		return rec[c]
	}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if process != "" && cApp >= 0 && !strings.EqualFold(get(rec, cApp), process) {
			continue
		}
		fr := Frame{Qpc: -1}
		if v, ok := num(get(rec, cQpc)); ok {
			fr.Qpc = int64(v)
		}
		fr.TimeS, _ = num(get(rec, cTime))
		fr.FrameMs, _ = num(get(rec, cFrame))
		fr.CPUBusyMs, _ = num(get(rec, cCPU))
		fr.GPUBusyMs, _ = num(get(rec, cGPU))
		fr.GPUTimeMs, _ = num(get(rec, cGPUT))
		if v, ok := num(get(rec, cDisp)); ok {
			fr.DisplayMs = v
		} else {
			fr.DisplayMs = -1
		}
		t := strings.TrimSpace(get(rec, cType))
		fr.Generated = t != "" && !strings.EqualFold(t, "Application") && !strings.EqualFold(t, "NotSet")
		fr.InputMs = -1
		if v, ok := num(get(rec, cInput)); ok && v > 0 {
			fr.InputMs = v
		}
		d := strings.TrimSpace(get(rec, cDrop))
		fr.Dropped = d == "1" || strings.EqualFold(d, "true")
		key := get(rec, cSwap)
		s := bySwap[key]
		if s == nil {
			s = &sc{}
			bySwap[key] = s
		}
		s.frames = append(s.frames, fr)
	}
	var best *sc
	for _, s := range bySwap {
		if best == nil || len(s.frames) > len(best.frames) {
			best = s
		}
	}
	if best != nil {
		cap.Frames = best.frames
	}
	for i := range cap.Frames {
		if cap.Frames[i].Generated {
			cap.Generated++
		}
	}
	return cap, nil
}

// AppFrames returns only the frames the game rendered itself.
func (c *Capture) AppFrames() []Frame {
	out := make([]Frame, 0, len(c.Frames))
	for _, f := range c.Frames {
		if !f.Generated {
			out = append(out, f)
		}
	}
	return out
}
