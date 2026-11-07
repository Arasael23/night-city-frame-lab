Night City Frame Lab 2.0.1
==========================

A stutter and performance recorder for Cyberpunk 2077 with mods. One window records the game with
every layer on the same clock, then explains each slow frame:

  - shader and ray tracing pipeline builds
  - world streaming, file loading and entity spawns
  - REDscript mods and CET Lua mods (time per mod, in the parts you mark with F11)
  - VRAM full, or game memory spilled into system RAM
  - disk reads, system RAM running out, the page file
  - a Windows driver holding the CPU (interrupts / DPCs)
  - the game resetting its screen (full screen re-applied, screen buffers rebuilt)
  - a GPU-heavy frame (path tracing load), the GPU at its power or heat limit

Nothing of it is in the game while you play normally. Frame Lab puts its recorders in for one
session and takes them out again when you quit the game.


Requirements
  - Windows 10 or 11, 64-bit. Microsoft Edge WebView2 Runtime (already part of Windows 11).
  - RED4ext, for the engine recorder (HitchRecorder). Without it you still get frame times,
    GPU and PC readings and the Windows events.
  - Cyber Engine Tweaks (optional): loading-screen markers and CET Lua time per mod need it.
  - Made and tested on game version 2.31 with an NVIDIA card (players report 2.5 works too). On
    another game version the engine recorder leaves out the parts it can't find and says so in
    its log.
  - AMD and Intel cards (new in 1.1, beta): GPU readings come from AMD's driver (ADLX, part of
    AMD Software: Adrenalin Edition) on Radeon cards, and from Windows on any card. The Capture
    tab shows which readings your card gives. Why a card lowered its clocks (power limit, heat)
    is NVIDIA only. If the GPU readings look wrong on your card, run components\GPU check.exe
    (changes nothing) and post the GPU check.txt it writes next to itself in the Bugs tab.


Install
  Unpack the folder anywhere (not into the game folder) and start NightCityFrameLab.exe.
  Windows asks for administrator rights: PresentMon (frame and GPU timing) and Windows' event
  tracing need them, and so does writing into a game folder under Program Files.

  On the first start Frame Lab looks for your game: Steam, GOG and Epic installs, the running
  game, the usual folders on every drive and copies next to them ("Cyberpunk 2077 - Modded" and
  so on). Each folder is one "build". Settings > Find game installs looks again; Add a build takes
  any other folder (the one with bin\x64 in it).


Record a session
  1. Capture tab: pick the build, name the session, press Start session (game closed).
  2. Start the game and play. Everything records by itself until you quit.
  3. Press F11 where it stutters to start a mark, F11 again ends it. Script time per mod
     (REDscript and CET) is measured only inside the marked parts, so this is how you find out
     which mod costs what. Press F10 whenever you feel a stutter: the report shows what happened
     in the 2 seconds before each press (new in 2.0).
  4. Quit the game.
  5. Frame Lab collects everything, takes the recorders out again and opens the report.
  Loading screens and the main menu are shaded on the timeline and left out of the numbers.

  The report: frame times with every slow frame coloured by its cause, VRAM and the rest of the
  graphics card, GPU clock and temperature, disk and CPU, drivers and system RAM, frames shown
  per second, a list of the spikes with what overlapped each one, script cost per mod, engine work,
  shader builds, and "What to try": advice worked out from that session's own numbers.
  Compare puts two sessions side by side. Export HTML report saves one as a single file.

  2.0.1:
  - Overlays and recorders that hook into the game (the NVIDIA overlay and the like) no longer
    show the game's own graphics memory as theirs in "Everything else on the game's card".
  - When the game kept part of its graphics memory in system RAM the whole session, the VRAM
    item says so, instead of tying it to the freezes only.
  - The Capture tab and this README put F11 and F10 up front.

  New in 2.0:
  - Checks tab: read-only checks of the PC and each build (display and frame limiters, injected
    DLLs, DLSS files, PCIe link, RAM speed, game drive, MSI mode, mod frameworks and debug logging
    left on, old tweaks, memory integrity), with a fix and an Undo where one is safe.
  - A session that ends in a crash says so first: whether the graphics card hung, what the
    Advanced Crash Reporter report says (when that mod is installed) and Windows' own log entries
    (display driver reset, hardware errors, unexpected restart).
  - Stutter % (frames over 2.5x the average), how spread out the frame times are, input lag
    (PresentMon), CPU temperature, load times (game start to the menu, script loading, save
    loading, every loading screen).
  - CPU temperature: read from HWiNFO while it runs with "Shared Memory Support" on, or on AMD
    Ryzen through PawnIO, the signed driver LibreHardwareMonitor and Fan Control use. Frame Lab
    never installs a driver: Checks > CPU temperature > Get PawnIO opens pawnio.eu, and you run
    its installer yourself.
  - Compare: what changed between the two sessions (game, driver and Windows versions, mods added,
    removed or updated, game settings) and whether a difference is bigger than the normal
    run-to-run noise.
  - VRAM tab: what one mip level less on each mod's big textures would save (exact), and big
    meshes without levels of detail.


What it puts into the game folder, for one session only
  - red4ext\plugins\HitchRecorder (engine recorder, also times REDscript and CET mods). It
    switches itself off after one game run, so even after a crash the next start is clean.
  - r6\scripts\NightCityFrameLab\FrameLab.Markers.reds (writes "loading screen on/off" and
    "in game" lines to CET's gamelog.log; needs CET).
  Both come out again when you quit the game, or with Cancel and remove. If the game or PC
  crashes mid-session, Frame Lab offers to take them out the next time it starts.
  Nothing else in the game folder is changed. CET itself is not touched.


What runs outside the game during a session
  - PresentMon (bundled): real frame times, GPU time per frame, frame generation.
  - The graphics driver: NVIDIA's NVML, or AMD's ADLX on Radeon cards: GPU clock, power,
    temperature, VRAM (NVIDIA also says why it lowered its clocks).
  - Windows' GPU readings, for any card: GPU usage and VRAM (Task Manager's numbers), and
    temperature and clock where the driver reports them.
  - Windows performance counters: game CPU, busiest core, disk reads, free RAM, graphics memory
    of every program.
  - Windows event tracing (kernel): how long each driver held the CPU, memory read back from the
    page file, disk use per program. Settings > Windows events switches it off.


Script time per mod, how it is counted
  REDscript: every script function the game runs is timed and charged to the mod whose .reds
  file it came from. Time is counted once: a mod's wrapper gets its own part, the game code it
  calls counts as "game scripts".
  CET: every call CET makes into a mod's Lua (onUpdate, onDraw, Observe, Override, hotkeys) is
  timed and charged to that mod, including the game script code it calls. Lua run from CET's
  console or by CET itself shows as "CET itself".
  Settings > Script time per mod switches both off.


Mods tab
  Every mod with a recorded cost in the open session: script time (REDscript and CET), files
  the game loaded from it, and in how many spikes it took part. Sortable and filterable.


Conflicts tab (no game needed)
  Pick a build, press Check build (a few seconds). It reads the build's files and lists:
  - Archives: two mods that replace the same game file. The archive that loads first is used (ASCII
    order of the file name in archive\pc\mod, or modlist.txt if you have one; REDmod folders after),
    the other one's copy is ignored. Shows which files, and how many of them the game actually loaded
    in your last session of that build. Compatibility patches and merges (by name), framework
    placeholder files meant to be replaced, and byte-identical copies are listed apart as harmless.
    To make a mod win, rename its .archive so it sorts earlier (a leading # or 0), or remove the other.
  - REDscript: a game function that two mods replace (only one replacement runs).
  - TweakXL: the same game value set to different things by two mods (simple "key: value" lines).
  - Logs: errors and warnings in the last log of RED4ext and each plugin (ArchiveXL, TweakXL,
    Codeware, ...), REDscript and every CET mod. A crash that a plugin logged shows up here too.


VRAM tab (no game needed), two parts:
  - Mods: Scan build adds up the textures and meshes each mod sends to the graphics card and
    compares every mod file with the game file it replaces (new in 1.2), so you see what a mod
    really adds on top of the game. A mod that swaps game meshes for ones of the same size holds
    a lot of data but adds almost nothing. Also shows upscaled textures (3.5 times the game's file
    or more), copies another mod overrides (never used), files identical to the game's own,
    textures without mip levels (always loaded at full size), archives that are cut off, what
    changed since the last scan, and with a session of that build how much the game really
    loaded. Click a mod for its biggest and most upscaled files and its Nexus page.
  - Programs on the graphics card: every program that holds graphics memory right now, on which
    card, and the graphics setting Windows has for it. Programs that don't need the main card
    (browsers, Discord, launchers) can be moved to the processor's integrated graphics, one by one
    or with Move all suggested. It is the same per-program setting as Windows Settings > System >
    Display > Graphics > Power saving and applies the next time that program starts. Undo puts
    back exactly what was there. The game, recorders (OBS, NVIDIA's apps) and Windows' own parts
    are never moved. Needs integrated graphics switched on; the tab says how if it isn't.


Experiments tab
  Windows settings people change to make games run better, each one switched on by itself and
  undone exactly (what was there before is kept in data\experiments.json). Every session notes
  which were on, so two sessions in Compare show whether a setting helped on your PC. The risky
  ones are marked and need a second click. Leave them off unless a session points at them.
  New in 2.0: Game Mode off, quiet session (Search, Update, SysMain paused), 3D V-Cache cores,
  0.5 ms timer, CPU idle off, PCIe link power off, MSI mode, memory compression off, crowd
  density, SMT, hybrid P-cores, NVIDIA power mode and DLSS latest model, DLSS indicator, a fixed
  page file on the fastest drive, GPU interrupt core, newer DLSS files with an exact backup, and
  Undo everything.
  New in 1.2: multiplane overlay (MPO) off, Game Bar background recording off, keep the game on
  the performance cores (Intel CPUs with two kinds of cores, beta), and two that are shown only:
  hardware-accelerated GPU scheduling, and the NVIDIA driver profile for the game (frame cap,
  V-sync, low latency, power mode and more), read from the driver and never changed.


Folders
  Sessions\      one folder per session (raw data, markers, analysis.json, exported HTML report)
  components\    HitchRecorder, FrameLabMarkers, PresentMon, GPU check, the PawnIO module, licences
  data\          the file-name list, scan results, experiments and graphics settings changed, framelab.log
  FrameLab.settings.json   your settings (created when you save in the Settings tab)

  File names: Frame Lab can show the game's file names instead of numbers. It builds that list
  once from WolvenKit if WolvenKit is on the PC (Settings > WolvenKit folder).
  Mod library (optional, Settings): a folder with your downloaded mod zips. Frame Lab then shows
  which download each file in the game came from.


Uninstall
  Turn off any Experiments you switched on and Undo any program you moved on the VRAM tab, then
  delete the folder. Nothing is left in the game folder after a session.


If something goes wrong
  data\framelab.log has every step. HitchRecorder's own log is copied into each session (hitch.log).
  Wrong or missing GPU readings: run components\GPU check.exe and post GPU check.txt in the Bugs tab.


Credits
  PresentMon by Intel (MIT). uPlot by Leon Sorokin (MIT). go-webview2 by John Chadwick (MIT).
  Inter font by The Inter Project Authors, IBM Plex Mono font by IBM (both SIL OFL 1.1).
  golang.org/x/sys (BSD-3). HitchRecorder uses MinHook by Tsuda Kageyu (BSD-2).
  PawnIO module AMDFamily17 by namazso and contributors (LGPL-2.1, unchanged signed build).
  Mapping script functions to their .reds files at load is an idea from jekky's redscript-dap (MIT);
  the CET part reads LuaJIT's data layout (LuaJIT and Cyber Engine Tweaks are MIT). Game function
  hashes come from ArchiveXL and RED4ext.SDK. Licences in components\licences and
  components\THIRD_PARTY.txt.


Licence
  Night City Frame Lab 1.1.0 and later: all rights reserved. The source is on GitHub to read and
  check, not to reuse, modify or re-upload anywhere without permission. Version 1.0.0 stays MIT.
