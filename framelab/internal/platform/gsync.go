package platform

// G-SYNC frame doubling (2.5.3). Below a certain frame rate the NVIDIA driver shows each frame twice
// (LFC, low framerate compensation) so the screen stays inside its variable refresh range. On high
// refresh screens the driver starts doing that around 50-65 fps, long before the screen's real VRR
// minimum (usually 48 Hz), so a game running near 60 fps keeps switching between 1x and 2x: brightness
// flicker on OLED screens, stutter on others.
//
// The switch is the driver's hidden value vrrFrameSplitBehavior in the graphics card's driver key
// (HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}\000N). It was
// found by u/PykeFeed on Reddit; Orbmu2k's NVIDIA G-SYNC Tweaks documents how the driver uses it. What
// Frame Lab uses of that, as facts (no code from either):
//   - missing value = the driver default 0x80000055
//   - low byte: how much the driver prefers the multiplier it is on (85 = 15% bonus)
//   - bit 16: use the lowest multiplier that fits (the fix: doubling only below the VRR minimum)
//   - bit 17: no preference for the current multiplier
//   - the driver reads it once when the card starts: restart the display driver (or Windows) after a change
// Frame Lab only ever sets or clears bit 16 and keeps every other bit as it was.

import "math"

const (
	SplitValueName        = "vrrFrameSplitBehavior"
	SplitDefault   uint32 = 0x80000055
	SplitLowestFit uint32 = 0x00010000
	SplitNoSticky  uint32 = 0x00020000
	// DisplayClassKey: the display adapters' class key under HKLM
	DisplayClassKey = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
)

// NvAdapter: an NVIDIA graphics card Windows has running, with its G-SYNC frame doubling value.
type NvAdapter struct {
	Name      string `json:"name"`
	Instance  string `json:"instance"`   // device instance ID (PCI\VEN_10DE&...)
	DriverKey string `json:"driver_key"` // {4d36e968-...}\000N under the class key
	Split     uint32 `json:"split"`      // the value the driver uses (SplitDefault when not set)
	SplitSet  bool   `json:"split_set"`  // false: not in the registry, the driver default applies
}

// SplitFixed: the driver uses the lowest multiplier that fits (doubling only below the VRR minimum).
func SplitFixed(v uint32) bool { return v&SplitLowestFit != 0 }

// SplitCustom: changed by something else (another tool), apart from the lowest-fit bit.
func SplitCustom(v uint32) bool { return v&^SplitLowestFit != SplitDefault }

// SplitPoints: the frame rates (frames reaching the screen, frame generation included) where the driver
// starts showing each frame twice (below engage) and stops again (above release), for a screen with
// this highest refresh rate and VRR minimum. Both 0 when the range is too narrow for doubling at all.
func SplitPoints(v uint32, maxHz, minHz float64) (engage, release float64) {
	if maxHz <= 0 || minHz <= 0 || minHz >= maxHz {
		return 0, 0
	}
	minFT, maxFT := 1000/maxHz, 1000/minHz // shortest and longest time a refresh may take (ms)
	if maxFT < 2*minFT {
		return 0, 0 // a doubled frame would not fit: no doubling inside this range
	}
	if SplitFixed(v) {
		return minHz, minHz
	}
	// the driver picks the multiplier whose frame time lands closest to the middle of the range,
	// with the current one's distance scaled by s (it prefers to stay)
	mid := (minFT + maxFT) / 2
	s := float64(v&0xFF) / 100
	if v&SplitNoSticky != 0 {
		s = 1
	}
	eT := mid * (1 + s) / (s + 0.5) // on 1x: doubling starts when the frame time passes this
	rT := mid * (1 + s) / (1 + s/2) // on 2x: back to 1x when the frame time drops under this
	eT = math.Min(eT, maxFT)        // 1x stops fitting at the VRR minimum in any case
	rT = math.Min(rT, maxFT)
	return round1(1000 / eT), round1(1000 / rT)
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

// VRRMinFromEDID: the lowest refresh rate the screen says it can run variable refresh at: the HDMI 2.1
// VRRmin (HDMI Forum data block), else the range limits of the base block. 0 when it says neither.
func VRRMinFromEDID(b []byte) int {
	if len(b) < 128 {
		return 0
	}
	// CTA-861 extension blocks: HDMI Forum VSDB (OUI C4-5D-D8) or SCDB (extended tag 0x79)
	for blk := 1; (blk+1)*128 <= len(b); blk++ {
		x := b[blk*128 : (blk+1)*128]
		if x[0] != 0x02 {
			continue
		}
		end := int(x[2])
		if end < 4 || end > 127 {
			continue
		}
		for i := 4; i < end; {
			tag, n := x[i]>>5, int(x[i]&0x1F)
			if i+1+n > end {
				break
			}
			p := x[i+1 : i+1+n]
			var hf []byte // the HDMI Forum fields, starting at their version byte
			switch {
			case tag == 3 && n >= 3 && p[0] == 0xD8 && p[1] == 0x5D && p[2] == 0xC4:
				hf = p[3:]
			case tag == 7 && n >= 4 && p[0] == 0x79:
				hf = p[3:]
			}
			// version, max TMDS rate, SCDC flags, FRL rate, ALLM/VRR flags, then VRRmin (bits 5:0)
			if len(hf) >= 6 {
				if m := int(hf[5] & 0x3F); m > 0 {
					return m
				}
			}
			i += 1 + n
		}
	}
	// base block: display range limits descriptor (tag 0xFD)
	for d := 54; d+18 <= 126; d += 18 {
		x := b[d : d+18]
		if x[0] == 0 && x[1] == 0 && x[3] == 0xFD {
			m := int(x[5])
			if x[4]&0x03 == 0x03 {
				m += 255
			}
			if m >= 20 && m <= 120 {
				return m
			}
		}
	}
	return 0
}
