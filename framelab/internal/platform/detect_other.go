//go:build !windows

package platform

import (
	"os"
	"strings"
)

// Dev stand-in: FRAMELAB_FAKE_INSTALLS="Steam=/path;GOG=/path;=/path"
func GameInstallHints() []InstallHint {
	var out []InstallHint
	for _, e := range strings.Split(os.Getenv("FRAMELAB_FAKE_INSTALLS"), ";") {
		if i := strings.IndexByte(e, '='); i >= 0 && e[i+1:] != "" {
			out = append(out, InstallHint{e[:i], e[i+1:]})
		}
	}
	return out
}

func Drives() []string { return nil }
