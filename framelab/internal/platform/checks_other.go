//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// Dev build: sample facts so the Checks tab can be looked at off Windows.
func ReadCheckFacts() CheckFacts {
	return CheckFacts{
		Displays:  []DisplayFact{{Name: `\\.\DISPLAY1`, Monitor: "Hisense TV", Primary: true, Width: 3840, Height: 2160, Hz: 120, MaxHz: 120}},
		RAM:       RAMFact{Known: true, Type: "DDR5", Modules: 2, SizeGB: 32, MTs: 6000, RatedMTs: 4800},
		PCIe:      PCIeFact{Known: true, CurGen: 1, CurWidth: 16, MaxGen: 5, MaxWidth: 16},
		MSI:       []MSIFact{{Device: "NVIDIA GeForce RTX 5080", MSI: true, Known: true}},
		BootTimer: map[string]string{},
		FTH:       []string{`C:\Games\Cyberpunk 2077\bin\x64\Cyberpunk2077.exe`},
		HVCI:      true, VBS: true,
	}
}

func PCIeNow() PCIeFact {
	return PCIeFact{Known: true, CurGen: 5, CurWidth: 16, MaxGen: 5, MaxWidth: 16}
}

func FileVersion(path string) (string, string) {
	if _, err := os.Stat(path); err != nil {
		return "", ""
	}
	n := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(n, "dlss"):
		return "310.2.1.0", "NVIDIA DLSS"
	case n == "dxgi.dll":
		return "6.4.1.0", "ReShade"
	}
	return "1.0.0.0", ""
}

func ExeSignature(path string) string { return "ok" }

func DriveInfo(path string) DriveFact {
	if path == `\` { // the system drive
		return DriveFact{Root: `C:\`, Bus: "NVMe", SSD: true, Known: true, Model: "WD_BLACK SN850X 1TB", FreeGB: 74, TotalGB: 931}
	}
	return DriveFact{Root: `D:\`, Bus: "NVMe", SSD: true, Known: true, Model: "Samsung SSD 990 PRO 2TB", FreeGB: 119, TotalGB: 1863}
}

func UserSettingsPath() string { return filepath.Join(dataDir, "UserSettings.json") }

func FileReadOnly(path string) (bool, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, false
	}
	return fi.Mode().Perm()&0o200 == 0, true
}

func FTHEntries() []string                         { return nil }
func ClearFTH() (map[string][]byte, error)         { return map[string][]byte{}, nil }
func RestoreFTH(saved map[string][]byte) error     { return nil }
func PawnIOVersion() string                        { return "" }
func ClearBootTimer() (map[string]string, error)   { return map[string]string{}, nil }
func RestoreBootTimer(was map[string]string) error { return nil }
func ClearReservedCpuSets() ([]byte, error)        { return nil, nil }
func RestoreReservedCpuSets(b []byte) error        { return nil }
