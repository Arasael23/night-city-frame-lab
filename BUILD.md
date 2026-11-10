# Building Night City Frame Lab 2.31.0

The release is cross-compiled on Linux. On Windows, use WSL with Ubuntu 24.04 and follow the same steps.

## 1. Toolchain (the exact versions used for the release)

Ubuntu 24.04:

```sh
sudo apt install gcc-mingw-w64-x86-64-win32 binutils-mingw-w64-x86-64 zip
#   gcc-mingw-w64-x86-64-win32   13.2.0-6ubuntu1+26.1        (x86_64-w64-mingw32-gcc 13-win32)
#   binutils-mingw-w64-x86-64    2.41.90.20240122-1ubuntu1+11.4
```

Go 1.24.7 for linux/amd64 from https://go.dev/dl/ (go1.24.7.linux-amd64.tar.gz), on the PATH.

No network access is needed during the build: all Go dependencies are in `framelab/vendor`.

## 2. Build

```sh
./build.sh
```

This produces `out/NightCityFrameLab.exe`, `out/GPU check.exe` and `out/HitchRecorder.dll` and prints their SHA-256:

| File | SHA-256 |
|---|---|
| NightCityFrameLab.exe | `953a83b1d2aab36ae317208b2b4c798be4c5d547ae35028bfcbfd39bb6d304ba` |
| GPU check.exe | `4d53a5f58e7fa66cb57de37aabc11560e9528bdc983c62604aa1adc3c6a55cbe` |
| HitchRecorder.dll | `d749031b9b53f67df017d60a62ad903950260fb94ba4ec75504391b44ad7dd1f` |

What `build.sh` runs:

```sh
# Frame Lab: icon, manifest and version info, then the exe
cd framelab/cmd/framelab && x86_64-w64-mingw32-windres -O coff -o rsrc_windows_amd64.syso resource.rc
cd framelab && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -mod=vendor -trimpath -buildvcs=false -ldflags "-H windowsgui -s -w" -o ../out/NightCityFrameLab.exe ./cmd/framelab

# HitchRecorder
x86_64-w64-mingw32-gcc -D_WIN32_WINNT=0x0A00 -DWINVER=0x0A00 -O2 -std=gnu11 -Wall -Wextra -Wno-unused-parameter -Wno-cast-function-type \
  -shared -o out/HitchRecorder.dll \
  hitchrecorder/src/HitchRecorder.c hitchrecorder/third_party/minhook/src/{hook.c,buffer.c,trampoline.c,hde/hde64.c} \
  -I hitchrecorder/third_party/minhook/include \
  -static-libgcc -Wl,--no-insert-timestamp -Wl,--exclude-all-symbols -s
```

## 3. Release zip

The download also contains Intel PresentMon 2.6.0, the official release binary from
https://github.com/GameTechDev/PresentMon/releases (version 2.6.0, file `PresentMon-2.6.0-x64.exe`,
SHA-256 `b2a706bc6ad475749e3b7e3409263aa1e6906d45bdcf993f6dbc0f660188f1af`). Put that file next to
`build.sh`.

It also contains one PawnIO module, `release/pawnio/AMDFamily17.bin` (in this repository), the unchanged
signed build from https://github.com/namazso/PawnIO.Modules/releases/tag/0.2.11 (`release_0_2_11.zip`,
SHA-256 `43608cb89bc84247fef1368a139013f7d043e17db6d6c8dfc9b46bf0905a81f4`; the module itself
`dae74615761b78bdf064dfb3e136252ddcc6fc727d88f14738d0e5800d427a91`, LGPL-2.1). The PawnIO driver is not
included: users install it themselves from pawnio.eu. Then:

```sh
./build.sh package
```

`out/Night City Frame Lab 2.31.0.zip` then has SHA-256
`1d991f7fb63ea0c6088a495635bb157f3737d6fec80a262999bcd63c6b4fb299` (file times are fixed so the zip is reproducible).

## Third-party code

`framelab/vendor` holds unmodified copies of the upstream files at the versions in `framelab/vendor/modules.txt`
(go-webview2 commit 56598839c808, go-winloader commit c1995be93bd1, golang.org/x/sys v0.30.0), only the
packages the app uses. `hitchrecorder/third_party/minhook` is MinHook at commit 8af6b4acae5a (only the
sources, header and licence). Details in THIRD_PARTY.md.
