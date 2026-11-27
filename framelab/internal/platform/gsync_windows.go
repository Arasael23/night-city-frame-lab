//go:build windows

package platform

// G-SYNC frame doubling on Windows (see gsync.go): find the running NVIDIA cards and their driver keys
// through SetupAPI, read or write vrrFrameSplitBehavior there, and restart a card's driver so it reads
// the value again (the same as Device Manager or pnputil /restart-device does).

import (
	"errors"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var guidDisplayClass, _ = windows.GUIDFromString("{4d36e968-e325-11ce-bfc1-08002be10318}")

// NvAdapters: the NVIDIA cards Windows has running, with their current value. Read only, no admin needed.
func NvAdapters() []NvAdapter {
	devs, err := windows.SetupDiGetClassDevsEx(&guidDisplayClass, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return nil
	}
	defer devs.Close()
	var out []NvAdapter
	for i := 0; i < 32; i++ {
		d, err := devs.EnumDeviceInfo(i)
		if err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
				break
			}
			continue
		}
		inst, _ := devs.DeviceInstanceID(d)
		if !strings.Contains(strings.ToUpper(inst), "VEN_10DE") {
			continue // not NVIDIA
		}
		drv, _ := devs.DeviceRegistryProperty(d, windows.SPDRP_DRIVER)
		dk, _ := drv.(string)
		if !strings.HasPrefix(strings.ToLower(dk), "{4d36e968-e325-11ce-bfc1-08002be10318}\\") {
			continue
		}
		a := NvAdapter{Instance: inst, DriverKey: dk, Split: SplitDefault}
		if v, err := devs.DeviceRegistryProperty(d, windows.SPDRP_DEVICEDESC); err == nil {
			a.Name, _ = v.(string)
		}
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, classSub(dk), registry.QUERY_VALUE); err == nil {
			if v, ok := readSplit(k); ok {
				a.Split, a.SplitSet = v, true
			}
			k.Close()
		}
		out = append(out, a)
	}
	return out
}

// classSub: the driver key's path under HKLM (driver key = "{class}\000N")
func classSub(driverKey string) string {
	return DisplayClassKey + `\` + driverKey[strings.Index(driverKey, `\`)+1:]
}

// readSplit: the driver takes a DWORD or a 4-byte binary value
func readSplit(k registry.Key) (uint32, bool) {
	if v, _, err := k.GetIntegerValue(SplitValueName); err == nil {
		return uint32(v), true
	}
	if b, _, err := k.GetBinaryValue(SplitValueName); err == nil && len(b) == 4 {
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, true
	}
	return 0, false
}

// SetSplit writes the value in a card's driver key (had = false removes it: back to the driver default).
// Only a key of an NVIDIA card Windows has running is accepted. Needs admin (Frame Lab runs as admin).
func SetSplit(driverKey string, had bool, v uint32) error {
	ok := false
	for _, a := range NvAdapters() {
		if strings.EqualFold(a.DriverKey, driverKey) {
			ok = true
		}
	}
	if !ok {
		return errors.New("that NVIDIA card is not in this PC any more")
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, classSub(driverKey), registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return errors.New("could not open the card's driver settings: " + err.Error())
	}
	defer k.Close()
	if !had {
		if err := k.DeleteValue(SplitValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	return k.SetDWordValue(SplitValueName, v)
}

// RestartAdapter restarts one card's driver: every screen goes black for a few seconds. needReboot is
// set when Windows says the change only takes effect after a restart of Windows.
func RestartAdapter(instance string) (needReboot bool, err error) {
	devs, err := windows.SetupDiGetClassDevsEx(&guidDisplayClass, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return false, err
	}
	defer devs.Close()
	for i := 0; i < 32; i++ {
		d, err := devs.EnumDeviceInfo(i)
		if err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
				break
			}
			continue
		}
		if id, _ := devs.DeviceInstanceID(d); !strings.EqualFold(id, instance) {
			continue
		}
		p := windows.PropChangeParams{
			ClassInstallHeader: *windows.MakeClassInstallHeader(windows.DIF_PROPERTYCHANGE),
			StateChange:        windows.DICS_PROPCHANGE,
			Scope:              windows.DICS_FLAG_GLOBAL,
		}
		if err := devs.SetClassInstallParams(d, &p.ClassInstallHeader, uint32(unsafe.Sizeof(p))); err != nil {
			return false, err
		}
		if err := devs.CallClassInstaller(windows.DIF_PROPERTYCHANGE, d); err != nil {
			return false, err
		}
		if ip, err := devs.DeviceInstallParams(d); err == nil && ip.Flags&(windows.DI_NEEDREBOOT|windows.DI_NEEDRESTART) != 0 {
			return true, nil
		}
		return false, nil
	}
	return false, errors.New("the card was not found")
}

var getTick64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

// BootTime: when Windows started (a Windows restart also restarts the display driver)
func BootTime() time.Time {
	ms, _, _ := getTick64.Call()
	return time.Now().Add(-time.Duration(ms) * time.Millisecond)
}
