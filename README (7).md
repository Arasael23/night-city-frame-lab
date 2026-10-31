# Night City Frame Lab

A stutter and performance recorder for heavily modded Cyberpunk 2077. It records one play session with every layer on the same clock and explains each slow frame: shader and pipeline builds, world streaming and file loading, entity spawns, REDscript and CET mods (time per mod), VRAM, system RAM and the page file, disk reads, driver interrupts/DPCs, screen resets and GPU limits.

Download and user guide: [Nexus Mods](https://www.nexusmods.com/cyberpunk2077/mods/36756)

Made and tested on game version 2.31 with an NVIDIA card. Not tested on 2.5 or on AMD/Intel cards yet.

## What is in this repository

| Folder | What it is |
|---|---|
| `framelab/` | The Windows app `NightCityFrameLab.exe` (Go, no cgo; UI is HTML/JS shown through WebView2). `vendor/` holds the exact third-party Go code it is built with. |
| `hitchrecorder/` | `HitchRecorder.dll`, the RED4ext plugin that times engine work and script calls during a session (C, with MinHook in `third_party/`). |
| `markers/` | `FrameLab.Markers.reds`, the REDscript file that writes loading-screen markers to CET's log during a session. |
| `release/` | The README and licence files that go into the download. |
| `build.sh` | Builds the exe and the DLL, and with `package` the release zip. |

The release also contains Intel's PresentMon 2.6.0 (official binary, not built here). See [BUILD.md](BUILD.md).

## Building

See [BUILD.md](BUILD.md). The build is reproducible: with the toolchain listed there, `./build.sh package` gives the same SHA-256 for the exe, the DLL and the zip as the Nexus download.

## Licence

MIT, see [LICENSE](LICENSE). Third-party parts keep their own licences, see [THIRD_PARTY.md](THIRD_PARTY.md).
