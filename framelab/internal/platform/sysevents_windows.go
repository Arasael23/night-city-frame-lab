package platform

// System event log entries that matter for a crash (2.0): the display driver stopping
// (Display 4101), hardware errors (WHEA-Logger) and unexpected restarts (Kernel-Power 41).
// Read with wevtutil after the game closes; nothing is changed.

import (
	"os/exec"
	"time"
)

func SystemEvents(since time.Time) []SysEvent {
	q := "*[System[(EventID=4101 or EventID=41 or Provider[@Name='Microsoft-Windows-WHEA-Logger'] or Provider[@Name='nvlddmkm']) and TimeCreated[@SystemTime>='" +
		since.UTC().Format("2006-01-02T15:04:05.000Z") + "']]]"
	cmd := exec.Command("wevtutil", "qe", "System", "/q:"+q, "/f:RenderedXml", "/c:40", "/rd:true")
	HideWindow(cmd)
	if out, err := cmd.Output(); err == nil {
		if ev := parseEventsXML(string(out)); len(ev) > 0 || len(out) == 0 {
			return ev
		}
	}
	cmd = exec.Command("wevtutil", "qe", "System", "/q:"+q, "/f:text", "/c:40", "/rd:true")
	HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseEvents(string(out))
}
