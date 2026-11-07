package platform

import (
	"strings"
)

// NiceDriverVersion: NVIDIA's Windows driver version 32.0.16.2101 is the 621.01 people know;
// other vendors keep the Windows number.
func NiceDriverVersion(desc, ver string) string {
	if strings.Contains(strings.ToLower(desc), "nvidia") {
		digits := strings.ReplaceAll(ver, ".", "")
		if len(digits) >= 5 {
			d := digits[len(digits)-5:]
			return d[:3] + "." + d[3:]
		}
	}
	return ver
}
