//go:build windows

package platform

// The NVIDIA driver's settings for Cyberpunk 2077, read through the driver's own nvapi64.dll
// (driver settings API, layout from NVIDIA's NVAPI headers, MIT). Read only here: the two NVIDIA
// Experiments (exp3_windows.go) are the only place Frame Lab writes driver settings.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	nvInitialize                = 0x0150e828
	nvDrsCreateSession          = 0x0694d52e
	nvDrsDestroySession         = 0xdad9cff8
	nvDrsLoadSettings           = 0x375dbd6b
	nvDrsFindAppByName          = 0xeee566b2
	nvDrsGetSetting             = 0x73bf8338
	nvDrsGetCurrentGlobal       = 0x617bff9f
	nvSettingSize               = 12320 // NVDRS_SETTING_V1 (pack 4)
	nvSettingCurrentU32         = 8220  // offset of u32CurrentValue
	nvSettingLocation           = 4108  // offset of settingLocation
	nvApplicationSize           = 20492 // NVDRS_APPLICATION_V4
	nvSettingNotFound     int32 = -160
)

type nvSettingDef struct {
	id     uint32
	name   string
	note   string
	values map[uint32]string
	fps    bool // a number of frames per second, 0 = off
	num    bool // a plain number, 0 = the app decides
}

var nvSettingDefs = []nvSettingDef{
	{id: 0x10835002, name: "Max frame rate", fps: true},
	{id: 0x007BA09E, name: "Max pre-rendered frames", num: true, note: "Low latency mode On sets 1"},
	{id: 0x00A879CF, name: "Vertical sync", values: map[uint32]string{0x60925292: "App decides", 0x08416747: "Off", 0x47814940: "On",
		0x32610244: "Half refresh rate", 0x71271021: "Third refresh rate", 0x13245256: "Quarter refresh rate", 0x18888888: "Fast"}},
	{id: 0x1057EB71, name: "Power management mode", values: map[uint32]string{0: "Adaptive", 1: "Prefer maximum performance", 2: "Driver controlled",
		3: "Prefer consistent performance", 4: "Prefer minimum", 5: "Normal"}},
	{id: 0x00CE2691, name: "Texture filtering quality", values: map[uint32]string{0xfffffff6: "High quality", 0: "Quality", 0x0a: "Performance", 0x14: "High performance"}},
	{id: 0x10A879CF, name: "G-SYNC", values: map[uint32]string{0: "Allowed", 1: "Forced off", 2: "Not allowed", 3: "ULMB", 4: "Fixed refresh"}},
}

type nvapi struct{ qi *windows.LazyProc }

func (n nvapi) call(id uint32, args ...uintptr) int32 {
	f, _, _ := n.qi.Call(uintptr(id))
	if f == 0 {
		return -3 // NVAPI_NO_IMPLEMENTATION
	}
	r, _, _ := syscall.SyscallN(f, args...)
	return int32(r)
}

// NvProfile reads the settings above for Cyberpunk2077.exe: from the game's driver profile, else
// the global profile, else the driver's default.
func NvProfile() ([]NvSetting, error) {
	dll := windows.NewLazySystemDLL("nvapi64.dll")
	if err := dll.Load(); err != nil {
		return nil, errors.New("no NVIDIA driver (nvapi64.dll) on this PC")
	}
	n := nvapi{qi: dll.NewProc("nvapi_QueryInterface")}
	if err := n.qi.Find(); err != nil {
		return nil, errors.New("the NVIDIA driver's settings API is missing")
	}
	if r := n.call(nvInitialize); r != 0 {
		return nil, fmt.Errorf("the NVIDIA driver didn't start its settings API (%d)", r)
	}
	var sess uintptr
	if r := n.call(nvDrsCreateSession, uintptr(unsafe.Pointer(&sess))); r != 0 || sess == 0 {
		return nil, fmt.Errorf("could not open the driver settings (%d)", r)
	}
	defer n.call(nvDrsDestroySession, sess)
	if r := n.call(nvDrsLoadSettings, sess); r != 0 {
		return nil, fmt.Errorf("could not load the driver settings (%d)", r)
	}
	var name [2048]uint16
	copy(name[:], windows.StringToUTF16("cyberpunk2077.exe"))
	app := make([]byte, nvApplicationSize)
	binary.LittleEndian.PutUint32(app, nvApplicationSize|4<<16)
	var prof, global uintptr
	n.call(nvDrsFindAppByName, sess, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&prof)), uintptr(unsafe.Pointer(&app[0])))
	n.call(nvDrsGetCurrentGlobal, sess, uintptr(unsafe.Pointer(&global)))
	get := func(h uintptr, id uint32) (uint32, uint32, bool) {
		if h == 0 {
			return 0, 0, false
		}
		buf := make([]byte, nvSettingSize)
		binary.LittleEndian.PutUint32(buf, nvSettingSize|1<<16)
		if r := n.call(nvDrsGetSetting, sess, h, uintptr(id), uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
			return 0, 0, false
		}
		return binary.LittleEndian.Uint32(buf[nvSettingCurrentU32:]), binary.LittleEndian.Uint32(buf[nvSettingLocation:]), true
	}
	var out []NvSetting
	for _, d := range nvSettingDefs {
		v, loc, ok := get(prof, d.id)
		from := "game profile"
		if ok && loc == 1 {
			from = "global"
		} else if ok && loc >= 2 {
			from = "driver default"
		}
		if !ok {
			if v, _, ok = get(global, d.id); ok {
				from = "global"
			}
		}
		s := NvSetting{Name: d.name, Note: d.note, From: from}
		switch {
		case !ok:
			s.Value, s.From = "Driver default", "driver default"
		case d.fps && v == 0:
			s.Value = "Off"
		case d.fps:
			s.Value = fmt.Sprintf("%d fps", v)
		case d.num && v == 0:
			s.Value = "App decides"
		case d.num:
			s.Value = fmt.Sprint(v)
		default:
			if t, ok := d.values[v]; ok {
				s.Value = t
			} else {
				s.Value = fmt.Sprintf("0x%08X", v)
			}
		}
		out = append(out, s)
	}
	return out, nil
}
