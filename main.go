//go:build windows

// Night City Frame Lab: one window that records and explains Cyberpunk 2077 stutters.
package main

import (
	"os"
	"path/filepath"

	webview2 "github.com/jchv/go-webview2"

	"framelab/internal/app"
	"framelab/internal/platform"
	"framelab/ui"
)

func main() {
	exe, _ := os.Executable()
	root := filepath.Dir(exe)
	if !platform.SingleInstance() {
		platform.MessageBox("Night City Frame Lab", "Frame Lab is already open.")
		return
	}
	srv := app.NewServer(app.NewPaths(root), ui.FS)
	url, err := srv.Start("")
	if err != nil {
		platform.MessageBox("Night City Frame Lab", "Could not start: "+err.Error())
		return
	}
	data := filepath.Join(os.Getenv("LOCALAPPDATA"), "NightCityFrameLab", "WebView2")
	os.MkdirAll(data, 0o755)
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     os.Getenv("FRAMELAB_DEBUG") == "1",
		AutoFocus: true,
		DataPath:  data,
		WindowOptions: webview2.WindowOptions{
			Title:  "Night City Frame Lab",
			Width:  1480,
			Height: 940,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		platform.MessageBox("Night City Frame Lab", "Microsoft Edge WebView2 is missing. Install the WebView2 Runtime from Microsoft and start Frame Lab again.")
		srv.Ctl.Shutdown()
		return
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
	srv.Ctl.Shutdown()
}
