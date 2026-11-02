# Third-party parts

| Part | Version | Licence | Where |
|---|---|---|---|
| go-webview2 (John Chadwick) | commit 56598839c808 | MIT | `framelab/vendor/github.com/jchv/go-webview2` |
| Microsoft WebView2Loader.dll (inside go-webview2) | as shipped by go-webview2 | Microsoft WebView2 SDK licence (`webviewloader/sdk/LICENSE.txt`) | embedded in the exe |
| go-winloader (John Chadwick) | commit c1995be93bd1 | ISC (see LICENSE.md) | `framelab/vendor/github.com/jchv/go-winloader` |
| golang.org/x/sys (The Go Authors) | v0.30.0 | BSD-3-Clause | `framelab/vendor/golang.org/x/sys` |
| uPlot (Leon Sorokin) | 1.6.32 | MIT | `framelab/ui/uPlot.*` |
| Inter font (The Inter Project Authors) | 4.0, cut down to the characters the window uses | SIL Open Font License 1.1 (`framelab/ui/fonts/OFL.txt`) | `framelab/ui/fonts/`, embedded in the exe |
| MinHook (Tsuda Kageyu) | commit 8af6b4acae5a | BSD-2-Clause | `hitchrecorder/third_party/minhook` |
| PresentMon (Intel) | 2.6.0 release binary | MIT | not in this repository; bundled in the download |

Ideas and data layouts used, with no code copied:
- redscript-dap by jekky (MIT): the game binds each script function to its source file at load; HitchRecorder uses this to trace a function back to its .reds file.
- LuaJIT (Mike Pall, MIT) as built into Cyber Engine Tweaks (yamashi and contributors, MIT): the protected-call entry and the function/prototype layout, read to time CET mods.
- Game function hashes from ArchiveXL (psiberx) and RED4ext.SDK.
- AMD ADLX SDK (AMD, MIT): the function-table layout of the ADLX interfaces, used to read Radeon cards through the driver's own amdadlx64.dll. Nothing of the SDK is included.
