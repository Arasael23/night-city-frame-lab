//go:build !windows

package platform

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"time"
)

// Dev stand-in: FRAMELAB_FAKE_NV=default (or a value in hex) gives one NVIDIA card; changes are kept
// in memory. Without it there is no NVIDIA card (the Linux beta never shows the row).
var fakeNv struct {
	sync.Mutex
	changed  bool
	had      bool
	v        uint32
	restarts int
}

func NvAdapters() []NvAdapter {
	s := os.Getenv("FRAMELAB_FAKE_NV")
	if s == "" {
		return nil
	}
	a := NvAdapter{Name: "NVIDIA GeForce RTX 5080", Instance: `PCI\VEN_10DE&DEV_2C02&SUBSYS_00000000&REV_A1\4&1`,
		DriverKey: `{4d36e968-e325-11ce-bfc1-08002be10318}\0001`, Split: SplitDefault}
	if v, err := strconv.ParseUint(s, 16, 32); err == nil {
		a.Split, a.SplitSet = uint32(v), true
	}
	fakeNv.Lock()
	defer fakeNv.Unlock()
	if fakeNv.changed {
		a.Split, a.SplitSet = SplitDefault, false
		if fakeNv.had {
			a.Split, a.SplitSet = fakeNv.v, true
		}
	}
	return []NvAdapter{a}
}

func SetSplit(driverKey string, had bool, v uint32) error {
	if os.Getenv("FRAMELAB_FAKE_NV") == "" {
		return errors.New("only on Windows")
	}
	fakeNv.Lock()
	fakeNv.changed, fakeNv.had, fakeNv.v = true, had, v
	fakeNv.Unlock()
	return nil
}

func RestartAdapter(instance string) (needReboot bool, err error) {
	if os.Getenv("FRAMELAB_FAKE_NV") == "" {
		return false, errors.New("only on Windows")
	}
	fakeNv.Lock()
	fakeNv.restarts++
	fakeNv.Unlock()
	return false, nil
}

func BootTime() time.Time { return time.Time{} }
