package app

// Undo journal: every change Frame Lab makes outside its own folder (Checks fixes now, more later)
// keeps what was there before in data\undo.json, so it can be put back one at a time or all at once.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"framelab/internal/platform"
)

type UndoEntry struct {
	ID   string          `json:"id"`
	What string          `json:"what"`
	Kind string          `json:"kind"` // fth, files, readonly, bcd_clear, cpusets_clear
	Data json.RawMessage `json:"data"`
	At   time.Time       `json:"at"`
}

type UndoFile struct {
	Path string `json:"path"`
	Was  []byte `json:"was"`
}

var undoMu sync.Mutex

func undoPath(data string) string { return filepath.Join(data, "undo.json") }

func undoLoad(data string) []UndoEntry {
	var out []UndoEntry
	if b, err := os.ReadFile(undoPath(data)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func undoSave(data string, l []UndoEntry) error {
	os.MkdirAll(data, 0o755)
	b, _ := json.MarshalIndent(l, "", "  ")
	tmp := undoPath(data) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, undoPath(data))
}

// undoAdd keeps the first saved state for an ID: a second fix of the same thing must not replace
// the original with an already-changed one. File and FTH entries are merged instead.
func undoAdd(data string, e UndoEntry) {
	undoMu.Lock()
	defer undoMu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now()
	}
	l := undoLoad(data)
	for i, x := range l {
		if x.ID != e.ID {
			continue
		}
		switch e.Kind {
		case "files":
			var a, b []UndoFile
			json.Unmarshal(x.Data, &a)
			json.Unmarshal(e.Data, &b)
			have := map[string]bool{}
			for _, f := range a {
				have[f.Path] = true
			}
			for _, f := range b {
				if !have[f.Path] {
					a = append(a, f)
				}
			}
			l[i].Data = mustJSON(a)
		case "fth":
			var a, b map[string][]byte
			json.Unmarshal(x.Data, &a)
			json.Unmarshal(e.Data, &b)
			if a == nil {
				a = map[string][]byte{}
			}
			for k, v := range b {
				if _, ok := a[k]; !ok {
					a[k] = v
				}
			}
			l[i].Data = mustJSON(a)
		}
		undoSave(data, l)
		return
	}
	undoSave(data, append(l, e))
}

func undoList(data string) []UndoEntry {
	undoMu.Lock()
	defer undoMu.Unlock()
	return undoLoad(data)
}

// undoOne puts one change back and drops it from the journal
func undoOne(data, id string) error {
	undoMu.Lock()
	defer undoMu.Unlock()
	l := undoLoad(data)
	for i, e := range l {
		if e.ID != id {
			continue
		}
		if err := undoApply(e); err != nil {
			return err
		}
		return undoSave(data, append(l[:i:i], l[i+1:]...))
	}
	return errors.New("nothing to undo for " + id)
}

// undoAll puts everything back, newest first; what fails stays in the journal
func undoAll(data string) (int, []string) {
	undoMu.Lock()
	defer undoMu.Unlock()
	l := undoLoad(data)
	var keep []UndoEntry
	var errs []string
	n := 0
	for i := len(l) - 1; i >= 0; i-- {
		if err := undoApply(l[i]); err != nil {
			errs = append(errs, l[i].What+": "+err.Error())
			keep = append([]UndoEntry{l[i]}, keep...)
			continue
		}
		n++
	}
	undoSave(data, keep)
	return n, errs
}

func undoApply(e UndoEntry) error {
	switch e.Kind {
	case "fth":
		var m map[string][]byte
		if err := json.Unmarshal(e.Data, &m); err != nil {
			return err
		}
		return platform.RestoreFTH(m)
	case "files":
		var fs []UndoFile
		if err := json.Unmarshal(e.Data, &fs); err != nil {
			return err
		}
		for _, f := range fs {
			if _, err := os.Stat(filepath.Dir(f.Path)); err != nil {
				continue // the mod was removed since: nothing to put back
			}
			if err := os.WriteFile(f.Path, f.Was, 0o644); err != nil {
				return err
			}
		}
		return nil
	case "readonly":
		var p string
		if err := json.Unmarshal(e.Data, &p); err != nil {
			return err
		}
		fi, err := os.Stat(p)
		if err != nil {
			return nil
		}
		return os.Chmod(p, fi.Mode()&^0o222)
	case "bcd_clear":
		var m map[string]string
		if err := json.Unmarshal(e.Data, &m); err != nil {
			return err
		}
		return platform.RestoreBootTimer(m)
	case "cpusets_clear":
		var b []byte
		if err := json.Unmarshal(e.Data, &b); err != nil {
			return err
		}
		if len(b) == 0 {
			return nil
		}
		return platform.RestoreReservedCpuSets(b)
	}
	return errors.New("unknown kind " + e.Kind)
}
