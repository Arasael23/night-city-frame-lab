# Third-party parts

| Part | Version | Licence | Where |
|---|---|---|---|
| go-webview2 (John Chadwick) | commit 56598839c808 | MIT | `framelab/vendor/github.com/jchv/go-webview2` |
| Microsoft WebView2Loader.dll (inside go-webview2) | as shipped by go-webview2 | Microsoft WebView2 SDK licence (`webviewloader/sdk/LICENSE.txt`) | embedded in the exe |
| go-winloader (John Chadwick) | commit c1995be93bd1 | ISC (see LICENSE.md) | `framelab/vendor/github.com/jchv/go-winloader` |
| golang.org/x/sys (The Go Authors) | v0.30.0 | BSD-3-Clause | `framelab/vendor/golang.org/x/sys` |
| uPlot (Leon Sorokin) | 1.6.32 | MIT | `framelab/ui/uPlot.*` |
| Saira font (The Saira Project Authors) | 400, 500, 600, 700, Latin + Latin Extended (from Fontsource 5.3.0) | SIL Open Font License 1.1 (`framelab/ui/fonts/OFL-Saira.txt`) | `framelab/ui/fonts/`, embedded in the exe |
| JetBrains Mono font (The JetBrains Mono Project Authors) | 400, 500, 600, Latin + Latin Extended (from Fontsource 5.3.0) | SIL Open Font License 1.1 (`framelab/ui/fonts/OFL-JetBrainsMono.txt`) | `framelab/ui/fonts/`, embedded in the exe |
| Jura font (The Jura Project Authors) | 500, 600, 700, Latin (from Fontsource 5.3.0); the logo is an SVG drawn from its letters (the two A from its Greek capital lambda) | SIL Open Font License 1.1 (`framelab/ui/fonts/OFL-Jura.txt`) | `framelab/ui/fonts/`, `framelab/ui/index.html` (logo), embedded in the exe |
| MinHook (Tsuda Kageyu) | commit 8af6b4acae5a | BSD-2-Clause | `hitchrecorder/third_party/minhook` |
| PresentMon (Intel) | 2.6.0 release binary | MIT | not in this repository; bundled in the download |
| PawnIO module AMDFamily17 (namazso and contributors) | PawnIO.Modules 0.2.11, signed release build | LGPL-2.1 | `release/pawnio/AMDFamily17.bin`, unchanged; source at github.com/namazso/PawnIO.Modules |
| WolvenKit console (the WolvenKit project and contributors) | 9.1.0 release (`WolvenKit.Console-9.1.0.zip`) | GPL-3.0 | not in this repository; bundled unpacked and unchanged in the 2.5E download (`components\WolvenKit`), started as a separate program; see `release/wolvenkit/` |

Ideas and data layouts used, with no code copied:
- redscript-dap by jekky (MIT): the game binds each script function to its source file at load; HitchRecorder uses this to trace a function back to its .reds file.
- LuaJIT (Mike Pall, MIT) as built into Cyber Engine Tweaks (yamashi and contributors, MIT): the protected-call entry and the function/prototype layout, read to time CET mods.
- Game function hashes from ArchiveXL (psiberx) and RED4ext.SDK.
- AMD ADLX SDK (AMD, MIT): the function-table layout of the ADLX interfaces, used to read Radeon cards through the driver's own amdadlx64.dll. Nothing of the SDK is included.
- Cyber Engine Tweaks' GameOptions (yamashi and contributors, MIT): Frame Lab's CET mods (`markers/cet`, and in 2.5E `markers/options`) read and set the game's own options through it.
