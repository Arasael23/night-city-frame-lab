//go:build !windows

package platform

// Dev stand-in for the Windows experiments: remembers on/off in memory.

import (
	"errors"
	"sync"
)

var (
	fakeMu  sync.Mutex
	fakeExp = map[string]bool{"defender": false, "power": false}
)

func ExpRead(id, game string) (on bool, detail string, ok bool) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	switch id {
	case "standby", "shadercache", "standbyauto":
		return false, "", true
	case "cfg":
		return false, "Control Flow Guard: notset (system default)", true
	case "pstate":
		if GPU().Vendor != "nvidia" {
			return false, "no NVIDIA graphics card found", false
		}
	}
	if fakeExp[id] {
		return true, "on (dev)", true
	}
	return false, "default (dev)", true
}

func ExpOn(id, game string) (map[string]string, error) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	fakeExp[id] = true
	return map[string]string{"was": "off"}, nil
}

func ExpRestore(id, game string, prev map[string]string) error {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	fakeExp[id] = false
	return nil
}

func PurgeStandby() error { return nil }

func ExpRun(id, game string) (string, error) {
	switch id {
	case "standby":
		return "Standby memory cleared (dev).", nil
	case "shadercache":
		return "Deleted 1,234 shader cache files (812 MB) (dev).", nil
	}
	return "", errors.New("not an action")
}
