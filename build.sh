#!/bin/sh
# Builds Night City Frame Lab exactly as released on Nexus.
# Toolchain used for the release (Ubuntu 24.04):
#   Go 1.24.7 (linux/amd64, from go.dev/dl)
#   gcc-mingw-w64-x86-64-win32 13.2.0-6ubuntu1+26.1   (x86_64-w64-mingw32-gcc 13-win32)
#   binutils-mingw-w64-x86-64 2.41.90.20240122-1ubuntu1+11.4   (x86_64-w64-mingw32-windres)
#   zip
# Two editions from one source (framelab/internal/app/edition.go):
#   Frame Lab 2.5   records, reads and advises; never changes Windows, game settings or mod files
#   Frame Lab 2.5E  (Experimental) also Experiments and Optimise; built with -tags experimental
# Usage: ./build.sh            -> out/NightCityFrameLab.exe (2.5), out/E/NightCityFrameLab.exe (2.5E),
#                                 out/GPU check.exe and out/HitchRecorder.dll
#        ./build.sh package    -> also out/Night City Frame Lab <version>.zip and ...<version>E.zip
#                                 (needs PresentMon, and for 2.5E the WolvenKit console zip, see BUILD.md)
set -e
cd "$(dirname "$0")"
VERSION=2.5.0
# 2.5E: the E goes after the number (2.5.0E, 2.5.0E-test8)
VERSION_E=$(echo "$VERSION" | sed "s/^\([0-9.]*\)/\1E/")
PM_FILE=PresentMon-2.6.0-x64.exe
PM_SHA256=b2a706bc6ad475749e3b7e3409263aa1e6906d45bdcf993f6dbc0f660188f1af
WK_FILE=WolvenKit.Console-9.1.0.zip
WK_SHA256=7caf49cdf0f478b484b1a5a27e5aef261b879fabea7e72e33ca60eff3b834f4f

rm -rf out
mkdir -p out

# 1. Night City Frame Lab (Go, no cgo), both editions. Icon, manifest and version info come from resource.rc.
mkdir -p out/E
fl() { # fl <version> <description> <tags> <out>
  sed -e "s/\"FileVersion\", \"[^\"]*\"/\"FileVersion\", \"$1\"/" -e "s/\"ProductVersion\", \"[^\"]*\"/\"ProductVersion\", \"$1\"/" \
      -e "s/\"FileDescription\", \"[^\"]*\"/\"FileDescription\", \"$2\"/" framelab/cmd/framelab/resource.rc > framelab/cmd/framelab/edition.rc
  ( cd framelab/cmd/framelab && x86_64-w64-mingw32-windres -O coff -o rsrc_windows_amd64.syso edition.rc )
  ( cd framelab && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
      go build -mod=vendor -trimpath -buildvcs=false -tags "$3" -ldflags "-H windowsgui -s -w" \
      -o "../$4" ./cmd/framelab )
  rm -f framelab/cmd/framelab/rsrc_windows_amd64.syso framelab/cmd/framelab/edition.rc
}
fl "$VERSION" "Night City Frame Lab 2.5" "" out/NightCityFrameLab.exe
fl "$VERSION_E" "Night City Frame Lab 2.5E (Experimental)" experimental out/E/NightCityFrameLab.exe
( cd framelab && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
    go build -mod=vendor -trimpath -buildvcs=false -ldflags "-H windowsgui -s -w" \
    -o "../out/GPU check.exe" ./cmd/gpucheck )

# 2. HitchRecorder (RED4ext plugin, C + MinHook)
MH=hitchrecorder/third_party/minhook
x86_64-w64-mingw32-gcc -D_WIN32_WINNT=0x0A00 -DWINVER=0x0A00 -O2 -std=gnu11 -Wall -Wextra -Wno-unused-parameter -Wno-cast-function-type \
  -shared -o out/HitchRecorder.dll \
  hitchrecorder/src/HitchRecorder.c $MH/src/hook.c $MH/src/buffer.c $MH/src/trampoline.c $MH/src/hde/hde64.c \
  -I $MH/include \
  -static-libgcc -Wl,--no-insert-timestamp -Wl,--exclude-all-symbols -s

echo "SHA-256:"
sha256sum out/NightCityFrameLab.exe out/E/NightCityFrameLab.exe "out/GPU check.exe" out/HitchRecorder.dll

[ "$1" = "package" ] || exit 0

# 3. Release zip (same layout as the Nexus download)
if [ ! -f "$PM_FILE" ]; then
  echo "Put $PM_FILE (Intel PresentMon 2.6.0 release, see BUILD.md) next to build.sh to package." >&2
  exit 1
fi
echo "$PM_SHA256  $PM_FILE" | sha256sum -c -
if [ ! -f "$WK_FILE" ]; then
  echo "Put $WK_FILE (official WolvenKit release 9.1.0, see BUILD.md) next to build.sh to package." >&2
  exit 1
fi
echo "$WK_SHA256  $WK_FILE" | sha256sum -c -
pkg() { # pkg <edition: main|E> <zip version>
  D="out/pkg/Night City Frame Lab"
  C="$D/components"
  rm -rf out/pkg
  echo "dae74615761b78bdf064dfb3e136252ddcc6fc727d88f14738d0e5800d427a91  release/pawnio/AMDFamily17.bin" | sha256sum -c -
  mkdir -p "$C/HitchRecorder/red4ext/plugins/HitchRecorder" "$C/FrameLabMarkers" "$C/PresentMon" "$C/PawnIO" "$C/Optimise" "$C/licences"
  cp release/pawnio/AMDFamily17.bin "$C/PawnIO/"
  cp release/optimise/mods-index.json.gz "$C/Optimise/"  # Mods tab: Nexus mod names, file paths + sizes, requirements (no file contents)
  cp release/optimise/archives.bin.gz release/optimise/corpus.json.gz "$C/Optimise/"  # what the downloaded mods carry: path hashes, tweak values, script/Lua facts (no file contents)
  if [ "$1" = E ]; then cp out/E/NightCityFrameLab.exe "$D/"; else cp out/NightCityFrameLab.exe "$D/"; fi
  if [ "$1" = E ]; then cp release/README-E.txt "$D/README.txt"; else cp release/README.txt "$D/"; fi   # one README per edition
  cp "out/GPU check.exe" "$C/"
  cp out/HitchRecorder.dll hitchrecorder/HitchRecorder.ini hitchrecorder/README.txt "$C/HitchRecorder/red4ext/plugins/HitchRecorder/"
  cp $MH/LICENSE.txt "$C/HitchRecorder/red4ext/plugins/HitchRecorder/THIRD_PARTY_MinHook_LICENSE.txt"
  cp markers/FrameLab.Markers.reds markers/FrameLab.Markers.2.5.reds "$C/FrameLabMarkers/"
  mkdir -p "$C/FrameLabMarkers/cet" && cp markers/cet/init.lua "$C/FrameLabMarkers/cet/"   # game 2.5+: the markers as a CET mod
  if [ "$1" = E ]; then
    mkdir -p "$C/FrameLabOptions" && cp markers/options/init.lua "$C/FrameLabOptions/"     # kept game options (Optimise): a CET mod that sets them
    mkdir -p "$C/WolvenKit" && unzip -q "$WK_FILE" -d "$C/WolvenKit"                       # the WolvenKit console, unchanged (GPL-3.0)
    cp release/wolvenkit/LICENSE.txt release/wolvenkit/SOURCE.txt "$C/WolvenKit/"
  fi
  cp "$PM_FILE" "$C/PresentMon/"
  cp release/PresentMon-license.txt "$C/PresentMon/license.txt"
  cp release/THIRD_PARTY.txt "$C/"
  cp release/licences/* "$C/licences/"
  find out/pkg -exec touch -d "2026-11-10 00:00:00 UTC" {} +
  ( cd out/pkg && find "Night City Frame Lab" -type f | LC_ALL=C sort | TZ=UTC zip -X -D -q "../Night City Frame Lab $2.zip" -@ )
  rm -rf out/pkg
  sha256sum "out/Night City Frame Lab $2.zip"
}
pkg main "$VERSION"
pkg E "$VERSION_E"
