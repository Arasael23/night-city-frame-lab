package optimise

import (
	"fmt"
	"sort"
	"strings"

	"framelab/internal/conflicts"
)

// "Do this next" (2.5): the findings turned into a short list of instructions, most important first.
// Everything Frame Lab read stays in the findings; this is what to do about them.

// Action: one thing to do in the build.
type Action struct {
	Severity string `json:"severity"`         // high / medium / info, like the finding behind it
	Text     string `json:"text"`             // the instruction
	Why      string `json:"why,omitempty"`    // what Frame Lab found
	Button   string `json:"button,omitempty"` // label of its button, "" for none
	Act      string `json:"act,omitempty"`    // what the button does: "nexus" (open Links[0]), "updates", "slow", "conflicts", "settings"
	Links    []Link `json:"links,omitempty"`
}

// ConflictFindings: what the conflict check (archives, REDscript, TweakXL) found, as Optimise findings.
// Overlaps a compatibility patch makes on purpose, placeholder files of frameworks and identical
// copies are left out: they are how those mods are meant to work.
func ConflictFindings(c *conflicts.Report) []Finding {
	if c == nil {
		return nil
	}
	var out []Finding
	for i, s := range c.Scripts {
		if i == 4 && len(c.Scripts) > 5 {
			out = append(out, Finding{Severity: "medium", Kind: "script_clash",
				Title:  fmt.Sprintf("%d more game functions are replaced by more than one mod", len(c.Scripts)-4),
				Detail: "Mods > Conflicts and errors lists each of them with the mods involved."})
			break
		}
		out = append(out, Finding{Severity: "medium", Kind: "script_clash",
			Title:  list(s.Mods, 3) + " replace the same game function, " + s.Target + ": only one of them runs",
			Detail: "REDscript keeps one replacement and drops the others, so one of these mods doesn't do its job there. Look for a compatibility patch between them, or keep the one you need.",
			Mods:   s.Mods})
	}
	var tw []string
	for _, t := range c.Tweaks {
		if t.Patch || len(t.Values) < 2 {
			continue
		}
		tw = append(tw, fmt.Sprintf("%s (%s: %s, %s: %s)", t.Key, t.Values[0].Mod, t.Values[0].Value, t.Values[1].Mod, t.Values[1].Value))
	}
	if n := len(tw); n > 0 {
		out = append(out, Finding{Severity: "info", Kind: "tweak_clash",
			Title:  plural(n, "game value is", "game values are") + " set differently by more than one mod",
			Detail: "Only one of the values sticks: " + list(tw, 3) + ". Mods > Conflicts and errors shows which file sets what."})
	}
	type pr struct {
		text string
		lost int
	}
	var prs []pr
	for _, p := range c.Pairs {
		lost := p.Files - p.Same
		if p.Patch || p.Slot || lost <= 0 || p.Winner < 0 || p.Loser < 0 || p.Winner >= len(c.Archives) || p.Loser >= len(c.Archives) {
			continue
		}
		prs = append(prs, pr{fmt.Sprintf("%s wins over %s (%d file%s)", c.Archives[p.Winner].Label, c.Archives[p.Loser].Label, lost, sIf(lost != 1)), lost})
	}
	if n := len(prs); n > 0 {
		sort.SliceStable(prs, func(i, j int) bool { return prs[i].lost > prs[j].lost })
		var top []string
		for _, p := range prs {
			top = append(top, p.text)
		}
		out = append(out, Finding{Severity: "info", Kind: "archive_overlap",
			Title:  plural(n, "pair of mods replaces", "pairs of mods replace") + " the same game files, and the one that loads first wins",
			Detail: "Biggest: " + list(top, 3) + ". Often that is what you want (a patch or a newer texture on top); it matters when the loser's part is the one you installed the mod for."})
	}
	return out
}

func linkNames(ls []Link, n int) string {
	var s []string
	for _, l := range ls {
		s = append(s, l.Text)
	}
	return list(s, n)
}

// SetActions works out the "Do this next" list and the verdict line from the report's findings.
// liveOn: the Nexus update check is switched on in Settings.
func SetActions(r *Report, liveOn bool) {
	sort.SliceStable(r.Findings, func(i, j int) bool { return sevRank(r.Findings[i].Severity) < sevRank(r.Findings[j].Severity) })
	var out []Action
	add := func(f Finding, text, button, act string) {
		out = append(out, Action{Severity: f.Severity, Text: text, Why: f.Title, Button: button, Act: act, Links: f.Links})
	}
	install := func(f Finding, fallback string) {
		if len(f.Links) > 0 {
			add(f, "Install "+linkNames(f.Links, 2)+".", "Open its Nexus page", "nexus")
			return
		}
		add(f, fallback, "", "")
	}
	clash := 0
	for _, f := range r.Findings {
		switch f.Kind {
		case "framework_outdated":
			if len(f.Links) > 0 {
				add(f, "Update "+linkNames(f.Links, 2)+".", "Open its Nexus page", "nexus")
			} else {
				add(f, "Update RED4ext to a version made for this game version.", "", "")
			}
		case "framework_missing":
			if strings.HasPrefix(f.Title, "REDmod") {
				add(f, "Install the free REDmod DLC from your launcher (GOG, Steam or Epic).", "", "")
			} else {
				install(f, "Install the framework this item names.")
			}
		case "module_missing":
			install(f, "Install the mod that has that REDscript module, or remove the mod that needs it.")
		case "requirement_missing":
			install(f, "Install the requirement this item names.")
		case "installed_twice":
			add(f, "Remove the older copy of "+strings.TrimSpace(strings.SplitN(f.Title, " is installed twice", 2)[0])+".", "", "")
		case "wrong_folder":
			add(f, "Install "+strings.Join(f.Mods, ", ")+" again so its folders land directly in the game folder.", "", "")
		case "still_packed":
			add(f, "Unpack "+strings.Join(f.Mods, ", ")+" and delete the packed copy.", "", "")
		case "obsolete":
			add(f, "Remove cybercmd: the game hasn't needed it since RED4ext 1.13.", "", "")
		case "plugin_outdated":
			add(f, "Update "+strings.Join(f.Mods, ", ")+" to a version made for game "+r.GameVersion+".", "", "")
		case "runtime_missing":
			add(f, "Install or repair the Microsoft Visual C++ 2015-2022 Redistributable (x64).", "", "")
		case "scripts_not_compiled":
			add(f, "Fix the script error named in r6\\logs\\redscript_rCURRENT.log: until the scripts compile, no REDscript mod runs.", "", "")
		case "script_clash":
			clash++
			if clash == 1 {
				add(f, "Decide which mod should win where two replace the same game function, or find a patch between them.", "Open Conflicts", "conflicts")
			}
		case "tweak_clash":
			a := Action{Severity: "info", Text: "Check which mod should win where two set the same game value differently.", Why: f.Title, Button: "Open Conflicts", Act: "conflicts"}
			out = append(out, a)
		case "updates":
			a := Action{Severity: "medium", Text: f.Title + ": update them from their Nexus pages.", Why: f.Detail, Button: "Show them", Act: "updates", Links: f.Links}
			out = append(out, a)
		}
	}
	if clash > 1 {
		for i := range out {
			if out[i].Act == "conflicts" {
				out[i].Why = fmt.Sprintf("%d game functions are replaced by more than one mod, so only one replacement runs in each", clash)
			}
		}
	}
	// CET mods that work every frame: one action for all of them
	var slow []string
	for _, m := range r.Mods {
		for _, h := range m.Lua {
			if h.Kind == "candidate" {
				slow = append(slow, m.Name)
				break
			}
		}
	}
	if n := len(slow); n > 0 {
		out = append(out, Action{Severity: "medium", Text: "Slow down " + plural(n, "CET mod", "CET mods") + " that work" + sIf(n == 1) + " every frame: " + list(slow, 4) + ".",
			Why: "Their onUpdate code runs on every frame although a few times a second would do. Each change keeps the original file and has an Undo.", Button: "Slow down all", Act: "slow"})
	}
	switch {
	case r.Live != nil && r.Live.Error != "":
		out = append(out, Action{Severity: "info", Text: "Press Check build again to repeat the Nexus update check.", Why: r.Live.Error})
	case !liveOn && r.Recognised > 0:
		out = append(out, Action{Severity: "info", Text: "Switch on the Nexus update check to see which of your mods are out of date.",
			Why: "It is off. When on, only the Nexus ids of the mods Frame Lab recognised are sent.", Button: "Open Settings", Act: "settings"})
	}
	sort.SliceStable(out, func(i, j int) bool { return sevRank(out[i].Severity) < sevRank(out[j].Severity) })
	r.Actions = out

	high, todo := 0, 0
	for _, a := range out {
		if a.Severity == "high" {
			high++
		}
		if a.Severity != "info" {
			todo++
		}
	}
	switch {
	case r.Files == 0:
		r.Verdict = "No mod files in this build."
	case todo == 0:
		r.Verdict = fmt.Sprintf("Nothing needs doing: %d mods read, every framework they need is there and nothing is broken.", len(r.Mods))
	default:
		r.Verdict = plural(todo, "thing", "things") + " to do in this build."
		if high > 0 {
			r.Verdict += " " + plural(high, "of them stops", "of them stop") + " mods from working: start there."
		}
	}
}
