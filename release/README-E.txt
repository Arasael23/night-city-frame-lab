Night City Frame Lab 2.5E (Experimental)
========================================

A stutter and performance recorder for Cyberpunk 2077 with mods. It records the game with every
layer on the same clock, explains each slow frame, checks your mods and your PC, and tells you
what to try first.

Frame Lab 2.5E is everything Frame Lab 2.5 does, plus the parts that change things for you:
Optimise (measure the build, fix mods, measure again), Game options with sliders, merging two
mods' text files, lighter texture mods put in for you, Windows Experiments and PC check fixes.
Every change is made on this PC only, keeps what was there before and has an exact Undo.
It is experimental: read what each change does before you press its button, change one thing at
a time, and record again after it. If you only want to record and read, use Frame Lab 2.5.

Mod files Frame Lab changes are for your own PC. Never upload or share them: the original mod
authors' permissions still apply.

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
  old folder. data\ holds the undo information of every change, so always take it along.

  Antivirus: 2.5E is an unsigned program that asks for administrator rights, runs PowerShell to
  change Windows settings and can add a Defender exclusion for the game folder (one of the
  Experiments, only when you switch it on). Some antivirus programs flag that kind of program
  without anything bad in it. The full source is on GitHub.


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
       versions of a mod that are on Nexus. Download the lighter file from the mod's Nexus page
       with your browser; Frame Lab sees it in your Downloads folder, checks it is the same mod
       with lighter textures, and swaps it in (the old archives are kept, Undo puts them back).
    3. The game's texture quality one step lower.


PC checks (no game needed)
  The PC and each build: display and frame limiters, injected DLLs, DLSS files, PCIe link, RAM
  speed, the game's drive, memory integrity, mod frameworks and debug logging left on, old tweaks.
  Each one says what it found; where a fix is safe, Fix makes it and Undo takes it back.
  CPU temperature: from HWiNFO while it runs with "Shared Memory Support" on, or on AMD Ryzen
  through PawnIO (pawnio.eu, a signed driver you install yourself; Frame Lab never installs one).


Optimise (2.5E)
  Measure, change one thing, measure again:
  1. Start the test round and record three kinds of play (a walk, a drive, a fight; about 8
     minutes in all, in one session or several). A line at the top of the screen in the game shows
     how far each one is. Script time per mod is measured for the whole run.
  2. The conflict check and the analysis of the build.
  3. Fix. What the analysis found, most important first, and what Frame Lab can change:
     - ready-made fixes for exact versions of popular mods: the ones made and played by hand,
       and ones made by a general rule and read by hand (the tab says which)
     - fixes by rule on your own files: a CET mod's onUpdate (or one call in it) runs a few times a
       second instead of every frame; a dialog helper many CET mods copy only works while its hub
       is on screen; a REDscript loop becomes one ArrayContains
     - game options, one at a time, on trial (see Game options)
     Every fix keeps the original file and has an exact Undo.
  4. Test again: the same kinds of play, and Frame Lab shows each one before and after.
  Workbench: open any mod (scripts, CET Lua, TweakXL, .xl, or a whole .archive, unpacked into
  readable JSON with the WolvenKit console) and edit it by hand, with checks while you type and
  advice on what to do and not. Install goes through the same Undo.
  Archives need Microsoft's .NET 10 runtime for the WolvenKit console.


Game options (2.5E)
  The game's own options, the ones mods like Ultra+ set through Cyber Engine Tweaks, each with a
  slider (down and up) or On / Off: sharper text and textures far away (street signs), how far out
  road markings and posters are drawn, how far out the world is loaded, streaming, shadows, fog.
  Frame Lab's small CET mod (NightCityFrameLabOptions) sets them; while you play it picks up a
  change within a couple of seconds, so you can watch it happen. Reset gives the game its own value
  back. Nothing is written to the game's files or your in-game settings, and taking the mod out
  (on the page) puts everything back. If another mod keeps setting the same option, the page says
  so. To measure what an option costs in frames, put it on trial in Optimise.


Merge two mods (2.5E, Conflicts)
  Two mods that replace the same text files (.json: subtitles, on-screen texts and other lists)
  can both be kept: Merge both into one unpacks both with the WolvenKit console, puts the entries
  of both into one new archive that loads before both (where both change the same entry, the
  winning mod's stays) and installs it with Undo. Neither mod is changed. Scenes and quest files
  can't be merged by a program: Open both in the Workbench shows them side by side. Let ... win
  changes which archive loads first (archive\pc\mod\modlist.txt), and a TweakXL clash gets your
  pick as a small patch file.


Experiments (2.5E)
  Windows settings people change to make games run better, each switched on by itself and undone
  exactly (what was there before is kept in data\experiments.json). Every session notes which
  were on, so Compare shows whether one helped on your PC. The risky ones are marked and need a
  second click. Among them: Game Mode, a quiet session (Search, Update and SysMain paused while
  you record), high performance power plan, MSI mode, hardware-accelerated GPU scheduling, Game
  Bar background recording, the game at high priority, timer resolution, CPU idle, PCIe link
  power, memory compression, crowd density, SMT and hybrid cores, NVIDIA power mode and DLSS
  model, a fixed page file, a Defender exclusion for the game folder, newer DLSS files with an
  exact backup, and Undo everything. Leave them off unless a session points at them.


What it puts into the game folder, for one session only
  - red4ext\plugins\HitchRecorder (engine recorder, also times REDscript and CET mods). It
    switches itself off after one game run, so even after a crash the next start is clean.
  - bin\x64\plugins\cyber_engine_tweaks\mods\NightCityFrameLab (a small CET mod: loading screens,
    menus, where V is every 5 seconds).
  Both come out again when you quit the game. If the game or PC crashes mid-session, Frame Lab
  offers to take them out the next time it starts.
  Changes that stay (2.5E, only when you make them): the fixes and merges you apply (listed with
  Undo in Optimise), lighter texture archives, archive\pc\mod\modlist.txt for a load-order pick,
  and bin\x64\plugins\cyber_engine_tweaks\mods\NightCityFrameLabOptions while you use Game
  options or keep an option after a trial.


What runs outside the game during a session
  - PresentMon (bundled, Intel): frame times, GPU time per frame, frame generation.
  - The graphics driver (NVIDIA NVML, AMD ADLX) and Windows' own GPU readings.
  - Windows performance counters and Windows event tracing (drivers holding the CPU, memory read
    back from the page file, disk use per program). Settings > Windows events switches it off.


Folders
  Sessions\      one folder per session (raw data, markers, analysis, exported HTML report)
  components\    HitchRecorder, the markers mod, the options mod, PresentMon, WolvenKit console,
                 the mod data, GPU check, PawnIO module, licences
  data\          scan results, the undo information of every change (keep it), framelab.log
  FrameLab.settings.json   your settings


Uninstall
  Settings > Changes on this PC > Put everything back (Experiments, PC check fixes, programs moved
  to the integrated graphics), Undo the mod fixes in Optimise, take the options mod out on the Game
  options page, then delete the folder.


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
