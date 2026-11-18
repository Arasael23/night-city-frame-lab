package optimise

// Optimise stage 2 (2.5E): edit recipes. Each one is a hand-made, tested change to one mod, kept as the
// exact edits to make (where, how many bytes go, what comes in) for one exact version of its files
// (SHA-256 of each file before and after). Frame Lab ships only the recipe: no mod files. A recipe only
// applies when every file it touches is byte for byte the file it was made for; anything else is left
// alone. recipes.json is made from the "our edit" zips by tools/mkrecipes.py.

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"framelab/internal/gamefs"
)

//go:embed recipes.json
var recipesJSON []byte

// recipes_rules.json: fixes made by the general rules (rules.go) in downloaded mods, each one read by
// hand and accepted, and checked to still load (tools: rulescan + mkrulerecipes). Not played in game
// one by one like the hand-made ones, which is why the tabs say so.
//
//go:embed recipes_rules.json
var ruleRecipesJSON []byte

// Recipe: one mod's fix.
type Recipe struct {
	ID    string       `json:"id"`
	Mod   string       `json:"mod"`
	Nexus int          `json:"nexus,omitempty"`
	Kind  string       `json:"kind"` // perf: costs less while playing; fix25: makes a part work on game 2.5
	What  string       `json:"what"`
	Files []RecipeFile `json:"files"`
	// Rule: made by a general rule and read by hand, not played in game (the hand-made ones were)
	Rule    bool   `json:"rule,omitempty"`
	Version string `json:"version,omitempty"` // the mod version the files are from
}

// RecipeFile: one file of a recipe. Before == "" means the recipe adds this file (Frame Lab's own code).
type RecipeFile struct {
	Path   string     `json:"path"` // relative to the game folder, forward slashes
	Before string     `json:"before,omitempty"`
	After  string     `json:"after"`
	Ops    []RecipeOp `json:"ops,omitempty"`
	Create string     `json:"create,omitempty"`
	Also   []string   `json:"also,omitempty"` // other hashes that count as the fix in place (the hand edit as first installed)
}

// RecipeOp: at byte At of the original, Del bytes (whose SHA-256 is DelSha) are replaced by Ins.
type RecipeOp struct {
	At     int    `json:"at"`
	Del    int    `json:"del"`
	DelSha string `json:"del_sha,omitempty"`
	Ins    string `json:"ins"`
}

var recipeList []Recipe

func init() {
	var doc struct {
		Recipes []Recipe `json:"recipes"`
	}
	if err := json.Unmarshal(recipesJSON, &doc); err == nil {
		recipeList = doc.Recipes
	}
	doc.Recipes = nil
	if err := json.Unmarshal(ruleRecipesJSON, &doc); err == nil {
		recipeList = append(recipeList, doc.Recipes...)
	}
}

// Recipes: every recipe Frame Lab knows.
func Recipes() []Recipe { return recipeList }

// RecipeByID: one recipe.
func RecipeByID(id string) (Recipe, bool) {
	for _, r := range recipeList {
		if r.ID == id {
			return r, true
		}
	}
	return Recipe{}, false
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ApplyOps makes the edits of one recipe file to its original. It refuses when the original isn't the
// file the recipe was made for, or when the result isn't exactly what the recipe promises.
func ApplyOps(orig []byte, f RecipeFile) ([]byte, error) {
	if f.Before == "" {
		if orig != nil {
			return nil, errors.New("the file is already there")
		}
		out := []byte(f.Create)
		if sum(out) != f.After {
			return nil, errors.New("the recipe is damaged")
		}
		return out, nil
	}
	if sum(orig) != f.Before {
		return nil, errors.New("this is another version of the file")
	}
	out := append([]byte(nil), orig...)
	ops := append([]RecipeOp(nil), f.Ops...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].At > ops[j].At }) // back to front: offsets stay right
	for _, o := range ops {
		if o.At < 0 || o.Del < 0 || o.At+o.Del > len(out) {
			return nil, errors.New("the recipe doesn't fit this file")
		}
		if o.Del > 0 && sum(out[o.At:o.At+o.Del]) != o.DelSha {
			return nil, errors.New("the recipe doesn't fit this file")
		}
		out = append(out[:o.At], append([]byte(o.Ins), out[o.At+o.Del:]...)...)
	}
	if sum(out) != f.After {
		return nil, errors.New("the result isn't what the recipe promises")
	}
	return out, nil
}

// RecipeState: a recipe against one build.
type RecipeState struct {
	Recipe
	State string   `json:"state"`          // ready (every file is the one it was made for), done (already in place), other (another version), partly (some files)
	Note  string   `json:"note,omitempty"` // what is different, in words
	Real  []string `json:"-"`              // where each file is (MO2: inside the mod's folder)
}

// MatchRecipes: the recipes that concern a build, i.e. whose mod is installed. game is the game folder.
func MatchRecipes(game string) []RecipeState {
	var out []RecipeState
	for _, r := range recipeList {
		st := RecipeState{Recipe: r}
		ready, done, present, other := 0, 0, 0, 0
		for _, f := range r.Files {
			p := filepath.Join(game, filepath.FromSlash(f.Path))
			real := gamefs.Real(p)
			st.Real = append(st.Real, real)
			b, err := os.ReadFile(real)
			if err != nil {
				if f.Before == "" { // ours to add
					ready++
				}
				continue
			}
			present++
			h := sum(b)
			switch {
			case h == f.After || contains(f.Also, h):
				done++
			case f.Before != "" && h == f.Before:
				ready++
			default:
				other++
			}
		}
		if present == 0 {
			continue // the mod isn't in this build
		}
		switch {
		case done == len(r.Files):
			st.State = "done"
		case ready == len(r.Files):
			st.State = "ready"
		case other > 0:
			st.State, st.Note = "other", "another version of the mod: the recipe is for one exact version, so it stays out"
		default:
			st.State, st.Note = "partly", fmt.Sprintf("%d of %d files match: the mod looks partly installed or partly changed", ready+done, len(r.Files))
		}
		out = append(out, st)
	}
	// one line per mod: recipes are for exact versions, so a mod with recipes for several versions only
	// shows the one that fits (or, when none does, one "another version" note)
	fits := map[string]bool{}
	key := func(r RecipeState) string {
		if r.Nexus > 0 {
			return strconv.Itoa(r.Nexus)
		}
		return strings.ToLower(r.Mod)
	}
	for _, r := range out {
		if r.State == "ready" || r.State == "done" {
			fits[key(r)] = true
		}
	}
	kept := out[:0]
	noted := map[string]bool{}
	for _, r := range out {
		k := key(r)
		if r.State != "ready" && r.State != "done" {
			if fits[k] || noted[k] {
				continue
			}
			noted[k] = true
		}
		kept = append(kept, r)
	}
	out = kept
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Mod) < strings.ToLower(out[j].Mod) })
	return out
}

// UseRecipes swaps the recipe list (tests) and returns a function that puts the real one back.
func UseRecipes(list []Recipe) (restore func()) {
	old := recipeList
	recipeList = list
	return func() { recipeList = old }
}

// MakeRecipeFile builds a recipe file from an original and the edited text with one replace op per
// changed run of bytes (tests and tools; the shipped recipes come from tools/mkrecipes.py).
func MakeRecipeFile(path string, orig, edited []byte) RecipeFile {
	f := RecipeFile{Path: path, After: sum(edited)}
	if orig == nil {
		f.Create = string(edited)
		return f
	}
	f.Before = sum(orig)
	p := 0
	for p < len(orig) && p < len(edited) && orig[p] == edited[p] {
		p++
	}
	s := 0
	for s < len(orig)-p && s < len(edited)-p && orig[len(orig)-1-s] == edited[len(edited)-1-s] {
		s++
	}
	if p == len(orig) && p == len(edited) {
		return f
	}
	del := orig[p : len(orig)-s]
	op := RecipeOp{At: p, Del: len(del), Ins: string(edited[p : len(edited)-s])}
	if len(del) > 0 {
		op.DelSha = sum(del)
	}
	f.Ops = []RecipeOp{op}
	return f
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
