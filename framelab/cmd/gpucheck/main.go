//go:build windows

// Frame Lab GPU check: writes what every graphics reading source gives on this PC to
// "GPU check.txt" next to the exe. Changes nothing.
package main

import (
	"os"
	"path/filepath"

	"framelab/internal/platform"
)

func main() {
	exe, _ := os.Executable()
	out := filepath.Join(filepath.Dir(exe), "GPU check.txt")
	f, err := os.Create(out)
	if err != nil {
		platform.MessageBox("Frame Lab GPU check", "Could not write "+out+": "+err.Error())
		return
	}
	defer f.Close()
	f.WriteString("\xef\xbb\xbf") // UTF-8 mark so Notepad shows the ° signs
	platform.GpuCheck(func(line string) {
		f.WriteString(line + "\r\n")
		f.Sync() // a driver library that closes the program still leaves everything before it
	})
	platform.MessageBox("Frame Lab GPU check", "Done. Saved to:\n"+out)
}
