# Night City Frame Lab

A stutter and performance recorder for heavily modded Cyberpunk 2077. It records one play session with every layer on the same clock and explains each slow frame: shader and pipeline builds, world streaming and file loading, entity spawns, REDscript and CET mods (time per mod), VRAM, system RAM and the page file, disk reads, driver interrupts/DPCs, screen resets and GPU limits.

Download and user guide: [Nexus Mods](https://www.nexusmods.com/cyberpunk2077/mods/36756)

For game version 2.5 (2.31 still works). Frame Lab's version numbers are its own, not game versions: Frame Lab 2.31 is the update after 2.0, and the number is a nod to the game's own versions. Tested with an NVIDIA card; AMD and Intel cards are supported from 1.1 as a beta.

**Coming next:** a very experimental part of Frame Lab that goes through your installed mods and fixes what it can: repairing broken or outdated mod scripts, merging scripts from mods that clash over the same game code, and cutting per-frame script cost, with a backup and Undo for every change. The fixes are made on your own PC for your own install and are never uploaded or shared. It's being tested on four game installs with different mod setups first. After that, players will be the beta testers on their own PCs, because an app like this can't be tested on one computer alone, so it will take a while. That's the version that goes through Nexus' safety check, hopefully the last update until Cyberpunk 2077 v3.

## What is in this repository

| Folder | What it is |
|---|---|
| `framelab/` | The Windows app `NightCityFrameLab.exe` (Go, no cgo; UI is HTML/JS shown through WebView2). `vendor/` holds the exact third-party Go code it is built with. |
| `hitchrecorder/` | `HitchRecorder.dll`, the RED4ext plugin that times engine work and script calls during a session (C, with MinHook in `third_party/`). |
| `markers/` | `FrameLab.Markers.reds` (game 2.31) and `FrameLab.Markers.2.5.reds` (game 2.5), the REDscript files that write loading-screen markers to CET's log during a session. |
| `release/` | The README and licence files that go into the download. |
| `build.sh` | Builds the exe and the DLL, and with `package` the release zip. |

The release also contains Intel's PresentMon 2.6.0 (official binary, not built here). See [BUILD.md](BUILD.md).

## Building

See [BUILD.md](BUILD.md). The build is reproducible: with the toolchain listed there, `./build.sh package` gives the same SHA-256 for the exe, the DLL and the zip as the Nexus download.

## Licence

From 1.1.0: all rights reserved, the source is here to read, not to reuse or re-upload (see [LICENSE](LICENSE)). Version 1.0.0 (tag `v1.0.0`) stays under MIT. Third-party parts keep their own licences, see [THIRD_PARTY.md](THIRD_PARTY.md).
