package optimise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LiveState: the optional update check against Nexus Mods (only when the player switched it on in
// Settings). It sends the Nexus ids of the recognised mods to Nexus' public API, nothing else.
type LiveState struct {
	Checked time.Time `json:"checked"`
	Asked   int       `json:"asked"`
	Updates int       `json:"updates"`
	Error   string    `json:"error,omitempty"`
}

const nexusAPI = "https://api.nexusmods.com/v2/graphql"

var reVerNum = regexp.MustCompile(`\d+(?:\.\d+)*`)

func normVer(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v"))
	return strings.TrimSuffix(strings.TrimSuffix(v, ".0"), ".0")
}

// newer: Nexus shows a later version than the installed one (only when both are plain numbers)
func newer(installed, latest string) bool {
	a, b := reVerNum.FindString(installed), reVerNum.FindString(latest)
	if a == "" || b == "" || normVer(installed) == normVer(latest) {
		return false
	}
	return verLess(a, b)
}

// CheckLive asks Nexus for the current version of every recognised mod and marks the ones with a newer one.
func CheckLive(r *Report, client *http.Client) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	st := &LiveState{Checked: time.Now()}
	r.Live = st
	idx := map[int][]int{} // nexus id -> mods in r.Mods
	for i, m := range r.Mods {
		if m.Nexus != 0 && m.How != "folder name" {
			idx[m.Nexus] = append(idx[m.Nexus], i)
		}
	}
	var ids []int
	for id := range idx {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	st.Asked = len(ids)
	for i := 0; i < len(ids); i += 20 {
		ch := ids[i:min(i+20, len(ids))]
		var parts []string
		for _, id := range ch {
			parts = append(parts, fmt.Sprintf(`{gameDomain:"cyberpunk2077",modId:%d}`, id))
		}
		q := `query{ legacyModsByDomain(ids:[` + strings.Join(parts, ",") + `]){ nodes{ modId name version updatedAt status } } }`
		body, _ := json.Marshal(map[string]string{"query": q})
		req, _ := http.NewRequest(http.MethodPost, nexusAPI, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Application-Name", "Night City Frame Lab")
		resp, err := client.Do(req)
		if err != nil {
			st.Error = "Nexus couldn't be reached: " + err.Error()
			return
		}
		var out struct {
			Data struct {
				LegacyModsByDomain struct {
					Nodes []struct {
						ModID   int    `json:"modId"`
						Name    string `json:"name"`
						Version string `json:"version"`
						Status  string `json:"status"`
					} `json:"nodes"`
				} `json:"legacyModsByDomain"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if err != nil {
			st.Error = "Nexus sent an answer Frame Lab couldn't read (" + resp.Status + ")"
			return
		}
		if len(out.Errors) > 0 && len(out.Data.LegacyModsByDomain.Nodes) == 0 {
			st.Error = "Nexus: " + out.Errors[0].Message
			return
		}
		for _, n := range out.Data.LegacyModsByDomain.Nodes {
			// a mod can be here as several entries (a main file and add-ons from the player's library, each
			// with its own version): what is installed is the highest of them
			inst := ""
			for _, mi := range idx[n.ModID] {
				m := &r.Mods[mi]
				m.Latest = n.Version
				if m.Version != "" && !m.Approx && (inst == "" || verLess(inst, m.Version)) {
					inst = m.Version
				}
			}
			if inst != "" && newer(inst, n.Version) {
				for _, mi := range idx[n.ModID] {
					if m := &r.Mods[mi]; m.Version == inst {
						m.Update = true
					}
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	var names []string
	var links []Link
	for _, m := range r.Mods {
		if m.Update {
			st.Updates++
			names = append(names, m.Name+" "+m.Version+" → "+m.Latest)
			links = append(links, link(m.Name, m.Nexus))
		}
	}
	if st.Updates > 0 {
		r.Findings = append(r.Findings, Finding{Severity: "info", Kind: "updates", Title: strconv.Itoa(st.Updates) + " mod" + sIf(st.Updates != 1) + " ha" + map[bool]string{true: "ve", false: "s"}[st.Updates != 1] + " a newer version on Nexus",
			Detail: list(names, 12) + ". Read each mod's changelog first: some updates are for another game version, or for a file of the mod you don't use.", Links: links})
	}
}
