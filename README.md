# Night City Frame Lab

A stutter and performance recorder for heavily modded Cyberpunk 2077. It records one play session with every layer on the same clock and explains each slow frame: shader and pipeline builds, world streaming and file loading, entity spawns, REDscript and CET mods (time per mod), VRAM, system RAM and the page file, disk reads, driver interrupts/DPCs, screen resets and GPU limits. It checks your mods and your PC and tells you what to try first.

Download and user guide: [Nexus Mods](https://www.nexusmods.com/cyberpunk2077/mods/36756), or the [releases](https://github.com/Arasael23/night-city-frame-lab/releases) here (the same zips).

Two editions from one source:
- **Frame Lab 2.5** records, reads and advises. It doesn't change Windows, your game settings or your mods; the one exception is moving background programs to the integrated graphics, with an exact Undo.
- **Frame Lab 2.5E (Experimental)**, built with `-tags experimental`, can also make changes for you: Optimise (test round, fixes, the same test again), ready-made and rule fixes for mods, the Workbench, Game options, Merge, lighter texture mods and Windows Experiments. Every change keeps what was there before and has an exact Undo. Mod files it changes are for the player's own PC and must never be uploaded or shared.

For Cyberpunk 2077 game version 2.5. Frame Lab's version numbers are its own, not game versions. Tested with an NVIDIA card; AMD and Intel cards are a beta.

## What is in this repository

| Folder | What it is |
|---|---|
| `framelab/` | The Windows app `NightCityFrameLab.exe` (Go, no cgo; UI is HTML/JS shown through WebView2). `internal/app/edition.go` decides what each edition can do. `vendor/` holds the exact third-party Go code it is built with. |
| `hitchrecorder/` | `HitchRecorder.dll`, the RED4ext plugin that times engine work and script calls during a session (C, with MinHook in `third_party/`). |
| `markers/` | The small mods Frame Lab puts into the game for a session: `cet/` (CET mod: loading screens, menus, where V is), `FrameLab.Markers*.reds`, and in 2.5E `options/` (the CET mod that sets game options). |
| `release/` | The READMEs (one per edition), licence files, the PawnIO module, the WolvenKit notes and `optimise/`: what Frame Lab knows from downloaded mods (Nexus ids, names, file paths and path hashes, TweakXL values, script facts; no file contents). |
| `build.sh` | Builds both editions, GPU check and the DLL, and with `package` both release zips. |

The release also contains Intel's PresentMon 2.6.0 and, in 2.5E, the WolvenKit console 9.1.0 (official binaries, not built here). See [BUILD.md](BUILD.md).

## Building

See [BUILD.md](BUILD.md). The build is reproducible: with the toolchain listed there, `./build.sh package` gives the same SHA-256 for the exes, the DLL and both zips as the downloads.

## Licence

From 1.1.0: all rights reserved, the source is here to read, not to reuse or re-upload (see [LICENSE](LICENSE)). Version 1.0.0 (tag `v1.0.0`) stays under MIT. Third-party parts keep their own licences, see [THIRD_PARTY.md](THIRD_PARTY.md).
