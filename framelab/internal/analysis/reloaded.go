package analysis

import (
	"fmt"
	"sort"
)

// Reloaded: a file the game loaded again in stutter after stutter. With VRAM near its budget that is
// the game dropping it to make room and fetching it back; otherwise usually an area being unloaded and
// loaded again as you move back and forth.
type Reloaded struct {
	Name     string `json:"name,omitempty"` // file name when the name list knows it
	Label    string `json:"label"`          // the game's hash for the file
	Mod      string `json:"mod,omitempty"`  // the mod it comes from (empty: the base game)
	Spikes   int    `json:"spikes"`         // stutters it was loaded in, 5 s or more apart
	VramFull int    `json:"vram_full"`      // of those, with VRAM at 90% of its budget or more
	hash     uint64
}

const (
	reloadMinSpikes = 3 // loaded in at least this many separate stutters
	reloadGapS      = 5 // stutters closer than this count once (one long load spanning two)
	reloadMax       = 20
)

// reloadedFiles also returns how many stutters had at least one of those files in them, and how many of
// those had VRAM at 90% of its budget or more.
func reloadedFiles(spikes []Spike, fileSpikes map[uint64][]int) (out []Reloaded, stutters, vramFull int) {
	for h, idx := range fileSpikes {
		if len(idx) < reloadMinSpikes {
			continue
		}
		sort.Slice(idx, func(i, j int) bool { return spikes[idx[i]].T < spikes[idx[j]].T })
		n, full, last := 0, 0, -1e9
		for _, i := range idx {
			sp := &spikes[i]
			if sp.T-last < reloadGapS {
				continue
			}
			last = sp.T
			n++
			if sp.BudgetMB > 0 && sp.VramMB >= 0.9*sp.BudgetMB {
				full++
			}
		}
		if n >= reloadMinSpikes {
			out = append(out, Reloaded{Label: fmt.Sprintf("#%016x", h), Spikes: n, VramFull: full, hash: h})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spikes != out[j].Spikes {
			return out[i].Spikes > out[j].Spikes
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > reloadMax {
		out = out[:reloadMax]
	}
	in := map[int]bool{}
	for _, f := range out {
		for _, i := range fileSpikes[f.hash] {
			in[i] = true
		}
	}
	for i := range in {
		stutters++
		if sp := &spikes[i]; sp.BudgetMB > 0 && sp.VramMB >= 0.9*sp.BudgetMB {
			vramFull++
		}
	}
	return out, stutters, vramFull
}
