package app

import (
	"errors"
	"fmt"
)

// In-game keys of a session (2.5): "I felt a stutter" (Frame Lab watches it) and "mark a part"
// (HitchRecorder's capture key). Both can be changed in Settings because F10 / F11 are taken in
// some setups (overlays, other mods). Only keys that nobody types or plays with are offered: Frame
// Lab and HitchRecorder see a key press anywhere, not just in the game.

const (
	DefaultFeltKey = 0x79 // F10
	DefaultMarkKey = 0x7A // F11
)

// HotKey is one key the Settings list offers.
type HotKey struct {
	VK   int    `json:"vk"`
	Name string `json:"name"`
}

// HotKeys: function keys, the navigation block and the number pad.
var HotKeys = func() []HotKey {
	var k []HotKey
	for i := 1; i <= 24; i++ {
		k = append(k, HotKey{0x6F + i, fmt.Sprintf("F%d", i)})
	}
	k = append(k,
		HotKey{0x13, "Pause"}, HotKey{0x91, "Scroll Lock"},
		HotKey{0x2D, "Insert"}, HotKey{0x24, "Home"}, HotKey{0x23, "End"},
		HotKey{0x21, "Page Up"}, HotKey{0x22, "Page Down"})
	for i := 0; i <= 9; i++ {
		k = append(k, HotKey{0x60 + i, fmt.Sprintf("Num %d", i)})
	}
	k = append(k, HotKey{0x6A, "Num *"}, HotKey{0x6B, "Num +"}, HotKey{0x6D, "Num -"}, HotKey{0x6E, "Num ."}, HotKey{0x6F, "Num /"})
	return k
}()

// KeyName: "F10", "Num 5", ... for a Windows key code ("key 123" for one not in the list).
func KeyName(vk int) string {
	for _, k := range HotKeys {
		if k.VK == vk {
			return k.Name
		}
	}
	return fmt.Sprintf("key %d", vk)
}

func validKey(vk int) bool {
	for _, k := range HotKeys {
		if k.VK == vk {
			return true
		}
	}
	return false
}

// Keys: the two session keys from the settings (defaults for unset or unknown values).
func (c Config) Keys() (felt, mark int) {
	felt, mark = c.FeltKey, c.MarkKey
	if !validKey(felt) {
		felt = DefaultFeltKey
	}
	if !validKey(mark) {
		mark = DefaultMarkKey
	}
	return felt, mark
}

// checkKeys: a key Settings can't use, or the same key twice, is refused.
func checkKeys(c *Config) error {
	if c.FeltKey != 0 && !validKey(c.FeltKey) {
		return fmt.Errorf("key code %d can't be used for the stutter key", c.FeltKey)
	}
	if c.MarkKey != 0 && !validKey(c.MarkKey) {
		return fmt.Errorf("key code %d can't be used for the mark key", c.MarkKey)
	}
	f, m := c.Keys()
	if f == m {
		return errors.New("the stutter key and the mark key have to be different keys")
	}
	return nil
}
