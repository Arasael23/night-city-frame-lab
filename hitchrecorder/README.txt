HitchRecorder 1.0.0
===================

What it does
  Records every frame's time plus the engine work around it: resource loads, world sector
  streaming, entity spawns, D3D12 shader/pipeline creation, GPU memory allocation, the
  game's VRAM use against its budget, and how much script time each REDscript and CET mod takes.
  It changes nothing in the game. Each hook calls the original function with the original
  arguments, hands its result back unchanged and only notes how long it took.

How to record
  Normally through Night City Frame Lab: press Start session there (game closed). Frame Lab puts
  this folder into the game for that one session (whole game run recorded, F11 marks the part to
  look at), and takes it out again when you quit the game.
  On its own: F11 in game starts recording, F11 again stops (CaptureMode=1). Scroll Lock drops a
  marker. CaptureMode=0 records the whole time the game runs.

Output (in "recordings" next to this file)
  HitchRecorder_<date>.log   readable: one block per minute + one SPIKE line per slow frame
  HitchRecorder_<date>.bin   everything, for Night City Frame Lab
  HitchRecorder_<date>.owners.txt   names of the REDscript and CET mods in the .bin

Settings
  HitchRecorder.ini. To switch off without removing: Enabled=0.
  If the game ever crashes with it, set Enabled=0 first and keep the crash report.

Needs
  RED4ext 1.30+ (game 2.31). On other game versions the ResourceLoader update hook and the
  REDscript timing stay off; the rest uses the same engine addresses ArchiveXL uses on 2.31 and 2.5.
  CET timing needs Cyber Engine Tweaks (1.37 checked); without it, or with a CET whose LuaJIT
  looks different, it stays off and the log says so.

Remove
  Delete red4ext\plugins\HitchRecorder.
