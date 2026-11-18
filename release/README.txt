Night City Frame Lab 2.5
========================

A stutter and performance recorder for Cyberpunk 2077 with mods. It records the game with every
layer on the same clock, explains each slow frame, checks your mods and your PC, and tells you
what to try first.

Frame Lab 2.5 reads and advises. It never changes Windows, your game settings or your mod files:
every fix comes as steps you do yourself. The one exception is the easy one on the VRAM page,
moving programs like browsers and Discord to the integrated graphics, with an exact Undo.
Frame Lab 2.5E (Experimental, a separate download) can also make fixes for you.

Nothing of Frame Lab is in the game while you play normally. It puts its recorders in for one
session and takes them out again when you quit the game.


Requirements
  - Windows 10 or 11, 64-bit. Microsoft Edge WebView2 Runtime (part of Windows 11).
  - Cyberpunk 2077 game version 2.5.
  - RED4ext, for the engine recorder (HitchRecorder). Without it you still get frame times,
    GPU and PC readings and the Windows events.
  - Cyber Engine Tweaks (recommended): loading screens, menus and where V is come from a small
    CET mod of Frame Lab's, and CET Lua time per mod needs CET.
  - NVIDIA cards fully; AMD and Intel cards are a beta (GPU readings from AMD's driver on Radeon
    cards, and from Windows on any card). If the GPU readings look wrong on your card, run
    components\GPU check.exe (changes nothing) and post the GPU check.txt it writes.


Install
  Unpack the folder anywhere (not into the game folder) and start NightCityFrameLab.exe.
  Windows asks for administrator rights: PresentMon (frame and GPU timing) and Windows' event
  tracing need them, and so does writing into a game folder under Program Files.

  On the first start Frame Lab looks for your game: Steam, GOG and Epic installs, the running game,
  the usual folders on every drive and copies next to them. Each folder is one "build", picked at
  the top of the window. Settings > Find game installs looks again; Add a build takes any other
  folder (the one with bin\x64 in it).

  Mod Organizer 2: give each build MO2 runs its MO2 folder in Settings (the one with
  ModOrganizer.ini). Frame Lab then reads the build the way the game sees it. Start the game
  through MO2 as usual.

  Updating from an older Frame Lab: copy Sessions, data and FrameLab.settings.json over from the
  old folder.


Start here
  One thing at a time, the easy ones first: what the newest recording of the build says to do,
  in the order to do it (crashes and hardware first, then the quick ones like graphics memory and
  closing programs, then Windows settings, then mods). Mark a step done and record again: the next
  report shows whether it helped.


Record a session
  1. Record: pick the build, press Start session (game closed).
  2. Start the game and play. Everything records by itself until you quit.
  3. F11 starts and ends a mark around a part that stutters: script time per mod (REDscript and
     CET) is measured inside the marked parts. F10 whenever you feel a stutter: the report shows
     what happened in the 2 seconds before. Both keys can be changed in Settings.
  4. Quit the game. Frame Lab collects everything, takes its parts out of the game and opens the
     report.

  The report: frame times with every slow frame coloured by its cause, graphics memory and the
  rest of the graphics card, CPU, disk, drivers and system RAM, the L-shape (frame time at every
  percentile), the list of spikes with what overlapped each one, script cost per mod, engine work,
  shader builds, load times, and what to try. Loading screens, menus and the main menu are shaded
  and left out of the numbers. A spike that came with opening or closing a menu says so.

  Causes it tells apart: shader and ray tracing builds, world streaming and file loading, entity
  spawns, REDscript and CET Lua mods, graphics memory full or spilled into system RAM, heavy
  streaming or slow disk reads, system RAM running out, a Windows driver holding the CPU, the
  game resetting its screen, a GPU-heavy frame, the GPU at its power or heat limit, a game menu.
  A session that ends in a crash says so first: a graphics card hang, the Advanced Crash Reporter
  report (when installed) and Windows' own log entries.

  Sessions keeps every recording; Compare puts up to six side by side, with what changed between
  them and whether a difference is bigger than the normal run-to-run noise. Every chart opens
  larger with zoom and pan. Export HTML report saves one as a single file.


Mods
  - Check your mods (no game needed): reads the build's mod files and finds missing
    requirements, mods installed twice or in the wrong place, plugins that failed to load, mods
    with a newer version on Nexus (Settings > Mod updates, off until you switch it on), and what
    Frame Lab knows from about 6,200 downloaded mods.
  - Conflicts and errors: two mods that replace the same game file (which one wins, and whether
    the game used it), game functions two mods replace, TweakXL values set differently (with what
    other mods set them to), and the errors and warnings in the logs of RED4ext, every plugin,
    REDscript and every CET mod. Compatibility patches, framework placeholder files and identical
    copies are listed apart as harmless.
  - Cost while playing: every mod with a recorded cost in the open session: script time, files
    the game loaded from it, and how many spikes it was part of.
  - Before you install: search a Nexus mod and see, before installing it, which of your mods
    carry the same game files and which would win, values it sets differently, game functions it
    replaces that your build replaces too, requirements you don't have, code that runs every
    frame, and whether it changes your save.


VRAM page (no game needed)
  The game only gets a set share of the graphics card's memory (Frame Lab shows it as the budget
  in every recording). When that runs out, the game moves textures in and out in the middle of a
  frame, and that is a stutter. Even a few hundred MB helps. Easiest first:
    1. Programs on the graphics card: move the ones that don't need it (browsers, Discord,
       launchers) to the integrated graphics, one by one or all suggested ones at once. It is the
       same per-program setting as Windows Settings > System > Display > Graphics, applies the
       next time that program starts, and Undo puts back exactly what was there.
    2. Lighter texture mods: what each mod sends to the graphics card, what it adds on top of the
       game's own files, upscaled textures, copies another mod overrides, and the lighter (2K)
       versions of a mod that are on Nexus.
    3. The game's texture quality one step lower.


PC checks (no game needed)
  The PC and each build: display and frame limiters, injected DLLs, DLSS files, PCIe link, RAM
  speed, the game's drive, memory integrity, mod frameworks and debug logging left on, old tweaks.
  Each one says what it found and the steps to fix it yourself.
  CPU temperature: from HWiNFO while it runs with "Shared Memory Support" on, or on AMD Ryzen
  through PawnIO (pawnio.eu, a signed driver you install yourself; Frame Lab never installs one).


What it puts into the game folder, for one session only
  - red4ext\plugins\HitchRecorder (engine recorder, also times REDscript and CET mods). It
    switches itself off after one game run, so even after a crash the next start is clean.
  - bin\x64\plugins\cyber_engine_tweaks\mods\NightCityFrameLab (a small CET mod: loading screens,
    menus, where V is every 5 seconds).
  Both come out again when you quit the game. If the game or PC crashes mid-session, Frame Lab
  offers to take them out the next time it starts. Nothing else in the game folder is changed.


What runs outside the game during a session
  - PresentMon (bundled, Intel): frame times, GPU time per frame, frame generation.
  - The graphics driver (NVIDIA NVML, AMD ADLX) and Windows' own GPU readings.
  - Windows performance counters and Windows event tracing (drivers holding the CPU, memory read
    back from the page file, disk use per program). Settings > Windows events switches it off.


Folders
  Sessions\      one folder per session (raw data, markers, analysis, exported HTML report)
  components\    HitchRecorder, the markers mod, PresentMon, the mod data, GPU check, PawnIO
                 module, licences
  data\          scan results, settings changes, framelab.log
  FrameLab.settings.json   your settings


Uninstall
  Undo any program you moved on the VRAM page, then delete the folder. Nothing is left in the game
  folder after a session.


If something goes wrong
  data\framelab.log has every step. HitchRecorder's own log is copied into each session
  (hitch.log). Post both in the Bugs tab on Nexus.


Credits
  PresentMon by Intel (MIT). uPlot by Leon Sorokin (MIT). go-webview2 by John Chadwick (MIT).
  Saira, JetBrains Mono and Jura fonts (SIL OFL 1.1).
  golang.org/x/sys (BSD-3). HitchRecorder uses MinHook by Tsuda Kageyu (BSD-2).
  PawnIO module AMDFamily17 by namazso and contributors (LGPL-2.1, unchanged signed build).
  Mapping script functions to their .reds files at load is an idea from jekky's redscript-dap
  (MIT); the CET part reads LuaJIT's data layout (LuaJIT and Cyber Engine Tweaks are MIT). Game
  function hashes come from ArchiveXL and RED4ext.SDK. Licences in components\licences and
  components\THIRD_PARTY.txt.


Licence
  Night City Frame Lab 1.1.0 and later: all rights reserved. The source is on GitHub to read and
  check, not to reuse, modify or re-upload anywhere without permission. Version 1.0.0 stays MIT.
