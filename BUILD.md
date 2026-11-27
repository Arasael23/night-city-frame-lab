# Building Night City Frame Lab 2.5.3 and 2.5.3E

The release is cross-compiled on Linux. On Windows, use WSL with Ubuntu 24.04 and follow the same steps.

## 1. Toolchain (the exact versions used for the release)

Ubuntu 24.04:

```sh
sudo apt install gcc-mingw-w64-x86-64-win32 binutils-mingw-w64-x86-64 zip unzip
#   gcc-mingw-w64-x86-64-win32   13.2.0-6ubuntu1+26.1        (x86_64-w64-mingw32-gcc 13-win32)
#   binutils-mingw-w64-x86-64    2.41.90.20240122-1ubuntu1+11.4
```

Go 1.24.7 for linux/amd64 from https://go.dev/dl/ (go1.24.7.linux-amd64.tar.gz), on the PATH.

No network access is needed during the build: all Go dependencies are in `framelab/vendor`.

## 2. Build

```sh
./build.sh
```

This produces both editions from the same source, `out/NightCityFrameLab.exe` (Frame Lab 2.5) and
`out/E/NightCityFrameLab.exe` (Frame Lab 2.5E, built with `-tags experimental`), plus `out/GPU check.exe`
and `out/HitchRecorder.dll`, and prints their SHA-256:

| File | SHA-256 |
|---|---|
| NightCityFrameLab.exe (2.5) | `c22d2cffc366b22ed4e58937cabefecc03c0cacb56cb3c1a8116559bcf2e1f85` |
| E/NightCityFrameLab.exe (2.5E) | `2ead15a4a30a9b2477a6d8a0bd8685e35c23c7f0c920838e8dd9e7e373111189` |
| GPU check.exe | `6608b5a3c26e8587d33148822612037f00277c2f786f0cd834b9c79349346d72` |
| HitchRecorder.dll | `68a50bc937df5b91da07aa0537aa08cc6170e457a1ce018ccaf93e14c29b653d` |

What `build.sh` runs:

```sh
# Frame Lab, once per edition: icon, manifest and version info (resource.rc with the edition's
# version and description), then the exe; 2.5E adds -tags experimental
cd framelab/cmd/framelab && x86_64-w64-mingw32-windres -O coff -o rsrc_windows_amd64.syso edition.rc
cd framelab && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -mod=vendor -trimpath -buildvcs=false -tags "" -ldflags "-H windowsgui -s -w" -o ../out/NightCityFrameLab.exe ./cmd/framelab
cd framelab && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -mod=vendor -trimpath -buildvcs=false -tags experimental -ldflags "-H windowsgui -s -w" -o ../out/E/NightCityFrameLab.exe ./cmd/framelab

# HitchRecorder
x86_64-w64-mingw32-gcc -D_WIN32_WINNT=0x0A00 -DWINVER=0x0A00 -O2 -std=gnu11 -Wall -Wextra -Wno-unused-parameter -Wno-cast-function-type \
  -shared -o out/HitchRecorder.dll \
  hitchrecorder/src/HitchRecorder.c hitchrecorder/third_party/minhook/src/{hook.c,buffer.c,trampoline.c,hde/hde64.c} \
  -I hitchrecorder/third_party/minhook/include \
  -static-libgcc -Wl,--no-insert-timestamp -Wl,--exclude-all-symbols -s
```

## 3. Release zip

Both downloads contain Intel PresentMon 2.6.0, the official release binary from
https://github.com/GameTechDev/PresentMon/releases (version 2.6.0, file `PresentMon-2.6.0-x64.exe`,
SHA-256 `b2a706bc6ad475749e3b7e3409263aa1e6906d45bdcf993f6dbc0f660188f1af`). Put that file next to
`build.sh`.

The 2.5E download also contains the WolvenKit console 9.1.0, the official release from
https://github.com/WolvenKit/WolvenKit/releases/tag/9.1.0 (file `WolvenKit.Console-9.1.0.zip`,
SHA-256 `7caf49cdf0f478b484b1a5a27e5aef261b879fabea7e72e33ca60eff3b834f4f`), unpacked and unchanged
into `components\WolvenKit`, with the GPL-3.0 text and a note on where its source is
(`release/wolvenkit/`). Put the zip next to `build.sh` before `./build.sh package`; it is not built
here. Frame Lab reads the game's file names from it and starts its console as a separate program.

It also contains one PawnIO module, `release/pawnio/AMDFamily17.bin` (in this repository), the unchanged
signed build from https://github.com/namazso/PawnIO.Modules/releases/tag/0.2.11 (`release_0_2_11.zip`,
SHA-256 `43608cb89bc84247fef1368a139013f7d043e17db6d6c8dfc9b46bf0905a81f4`; the module itself
`dae74615761b78bdf064dfb3e136252ddcc6fc727d88f14738d0e5800d427a91`, LGPL-2.1). The PawnIO driver is not
included: users install it themselves from pawnio.eu. The mod data in `release/optimise/` (Nexus ids,
names, file paths and path hashes, TweakXL values and script facts of downloaded mods; no file contents)
goes into `components\Optimise`. Then:

```sh
./build.sh package
```

This gives the two downloads (file times are fixed so the zips are reproducible):

| Zip | SHA-256 |
|---|---|
| `out/Night City Frame Lab 2.5.3.zip` (Frame Lab 2.5) | `62f58c25ec6b0be5a870fb320a6866404a7f4363c96484be5ca90950c21bc22a` |
| `out/Night City Frame Lab 2.5.3E.zip` (Frame Lab 2.5E) | `09982a6b3a7be8d63d524a24010f2ced9db2ea64676da61d375acd5df12f6779` |

Each zip has its edition's exe and README (`release/README.txt` or `release/README-E.txt`); only 2.5E has
`components\FrameLabOptions` and `components\WolvenKit`.

## Third-party code

`framelab/vendor` holds unmodified copies of the upstream files at the versions in `framelab/vendor/modules.txt`
(go-webview2 commit 56598839c808, go-winloader commit c1995be93bd1, golang.org/x/sys v0.30.0), only the
packages the app uses. `hitchrecorder/third_party/minhook` is MinHook at commit 8af6b4acae5a (only the
sources, header and licence). Details in THIRD_PARTY.md.
