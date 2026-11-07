#!/bin/sh
# Builds Night City Frame Lab exactly as released on Nexus.
# Toolchain used for the release (Ubuntu 24.04):
#   Go 1.24.7 (linux/amd64, from go.dev/dl)
#   gcc-mingw-w64-x86-64-win32 13.2.0-6ubuntu1+26.1   (x86_64-w64-mingw32-gcc 13-win32)
#   binutils-mingw-w64-x86-64 2.41.90.20240122-1ubuntu1+11.4   (x86_64-w64-mingw32-windres)
#   zip
# Usage: ./build.sh            -> out/NightCityFrameLab.exe, out/GPU check.exe and out/HitchRecorder.dll
#        ./build.sh package    -> also out/Night City Frame Lab <version>.zip (needs PresentMon, see BUILD.md)
set -e
cd "$(dirname "$0")"
VERSION=2.0.0
PM_FILE=PresentMon-2.6.0-x64.exe
PM_SHA256=b2a706bc6ad475749e3b7e3409263aa1e6906d45bdcf993f6dbc0f660188f1af

rm -rf out
mkdir -p out

# 1. Night City Frame Lab (Go, no cgo). Icon, manifest and version info come from resource.rc.
( cd framelab/cmd/framelab && x86_64-w64-mingw32-windres -O coff -o rsrc_windows_amd64.syso resource.rc )
( cd framelab && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
    go build -mod=vendor -trimpath -buildvcs=false -ldflags "-H windowsgui -s -w" \
    -o ../out/NightCityFrameLab.exe ./cmd/framelab )
rm -f framelab/cmd/framelab/rsrc_windows_amd64.syso
( cd framelab && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
    go build -mod=vendor -trimpath -buildvcs=false -ldflags "-H windowsgui -s -w" \
    -o "../out/GPU check.exe" ./cmd/gpucheck )

# 2. HitchRecorder (RED4ext plugin, C + MinHook)
MH=hitchrecorder/third_party/minhook
x86_64-w64-mingw32-gcc -O2 -std=gnu11 -Wall -Wextra -Wno-unused-parameter -Wno-cast-function-type \
  -shared -o out/HitchRecorder.dll \
  hitchrecorder/src/HitchRecorder.c $MH/src/hook.c $MH/src/buffer.c $MH/src/trampoline.c $MH/src/hde/hde64.c \
  -I $MH/include \
  -static-libgcc -Wl,--no-insert-timestamp -Wl,--exclude-all-symbols -s

echo "SHA-256:"
sha256sum out/NightCityFrameLab.exe "out/GPU check.exe" out/HitchRecorder.dll

[ "$1" = "package" ] || exit 0

# 3. Release zip (same layout as the Nexus download)
if [ ! -f "$PM_FILE" ]; then
  echo "Put $PM_FILE (Intel PresentMon 2.6.0 release, see BUILD.md) next to build.sh to package." >&2
  exit 1
fi
echo "$PM_SHA256  $PM_FILE" | sha256sum -c -
D="out/pkg/Night City Frame Lab"
C="$D/components"
echo "dae74615761b78bdf064dfb3e136252ddcc6fc727d88f14738d0e5800d427a91  release/pawnio/AMDFamily17.bin" | sha256sum -c -
mkdir -p "$C/HitchRecorder/red4ext/plugins/HitchRecorder" "$C/FrameLabMarkers" "$C/PresentMon" "$C/PawnIO" "$C/licences"
cp release/pawnio/AMDFamily17.bin "$C/PawnIO/"
cp out/NightCityFrameLab.exe release/README.txt "$D/"
cp "out/GPU check.exe" "$C/"
cp out/HitchRecorder.dll hitchrecorder/HitchRecorder.ini hitchrecorder/README.txt "$C/HitchRecorder/red4ext/plugins/HitchRecorder/"
cp $MH/LICENSE.txt "$C/HitchRecorder/red4ext/plugins/HitchRecorder/THIRD_PARTY_MinHook_LICENSE.txt"
cp markers/FrameLab.Markers.reds "$C/FrameLabMarkers/"
cp "$PM_FILE" "$C/PresentMon/"
cp release/PresentMon-license.txt "$C/PresentMon/license.txt"
cp release/THIRD_PARTY.txt "$C/"
cp release/licences/* "$C/licences/"
find out/pkg -exec touch -d "2026-11-07 00:00:00 UTC" {} +
( cd out/pkg && find "Night City Frame Lab" -type f | LC_ALL=C sort | TZ=UTC zip -X -D -q "../Night City Frame Lab $VERSION.zip" -@ )
rm -rf out/pkg
sha256sum "out/Night City Frame Lab $VERSION.zip"
