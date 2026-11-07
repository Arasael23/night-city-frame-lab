package platform

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// SystemVersions (2.0, session snapshot): Windows version and the graphics driver version of the
// card whose name contains gpuName (NVIDIA's 32.0.16.2101 is shown as 621.01).
func SystemVersions(gpuName string) (win, driver string) {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		disp, _, _ := k.GetStringValue("DisplayVersion")
		build, _, _ := k.GetStringValue("CurrentBuild")
		ubr, _, _ := k.GetIntegerValue("UBR")
		name := "Windows 10"
		if b := atoiSafe(build); b >= 22000 {
			name = "Windows 11"
		}
		win = strings.TrimSpace(fmt.Sprintf("%s %s (build %s.%d)", name, disp, build, ubr))
		k.Close()
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return
	}
	defer k.Close()
	subs, _ := k.ReadSubKeyNames(-1)
	for _, s := range subs {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass+`\`+s, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err != nil {
			continue
		}
		desc, _, _ := sk.GetStringValue("DriverDesc")
		ver, _, _ := sk.GetStringValue("DriverVersion")
		sk.Close()
		if desc == "" || ver == "" || (gpuName != "" && !strings.Contains(strings.ToLower(gpuName), strings.ToLower(desc)) && !strings.Contains(strings.ToLower(desc), strings.ToLower(gpuName))) {
			continue
		}
		driver = NiceDriverVersion(desc, ver)
		return
	}
	return
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
