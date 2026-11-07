package platform

import (
	"regexp"
	"strings"
	"time"
)

type SysEvent struct {
	Time   time.Time `json:"time"`
	ID     string    `json:"id"`
	Source string    `json:"source"`
	Level  string    `json:"level"`
	Text   string    `json:"text"`
}

// parseEvents reads wevtutil's /f:text output (blocks starting with "Event[n]:").
func parseEvents(s string) []SysEvent {
	var out []SysEvent
	var cur *SysEvent
	inDesc := false
	for _, raw := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " ")
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Event[") {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &SysEvent{}
			inDesc = false
			continue
		}
		if cur == nil {
			continue
		}
		if inDesc {
			if t != "" && len(cur.Text) < 400 {
				if cur.Text != "" {
					cur.Text += " "
				}
				cur.Text += t
			}
			continue
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Source":
			cur.Source = v
		case "Date":
			if tt, err := time.ParseInLocation("2006-01-02T15:04:05.000", v, time.Local); err == nil {
				cur.Time = tt
			} else if tt, err := time.Parse(time.RFC3339Nano, v); err == nil {
				cur.Time = tt
			}
		case "Event ID":
			cur.ID = v
		case "Level":
			cur.Level = v
		case "Description":
			inDesc = true
			if v != "" {
				cur.Text = v
			}
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

var (
	reEvBlock = regexp.MustCompile(`(?s)<Event[ >].*?</Event>`)
	reEvID    = regexp.MustCompile(`<EventID[^>]*>(\d+)</EventID>`)
	reEvTime  = regexp.MustCompile(`<TimeCreated SystemTime=['"]([^'"]+)['"]`)
	reEvProv  = regexp.MustCompile(`<Provider Name=['"]([^'"]+)['"]`)
	reEvLevel = regexp.MustCompile(`<Level>(\d+)</Level>`)
	reEvMsg   = regexp.MustCompile(`(?s)<Message>(.*?)</Message>`)
	reEvTag   = regexp.MustCompile(`<[^>]+>`)
)

// parseEventsXML reads wevtutil's /f:RenderedXml output: the same on every Windows language.
func parseEventsXML(s string) []SysEvent {
	var out []SysEvent
	for _, b := range reEvBlock.FindAllString(s, -1) {
		var e SysEvent
		if m := reEvID.FindStringSubmatch(b); m != nil {
			e.ID = m[1]
		}
		if m := reEvTime.FindStringSubmatch(b); m != nil {
			if t, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
				e.Time = t.Local()
			}
		}
		if m := reEvProv.FindStringSubmatch(b); m != nil {
			e.Source = strings.TrimPrefix(m[1], "Microsoft-Windows-")
		}
		if m := reEvLevel.FindStringSubmatch(b); m != nil {
			e.Level = map[string]string{"1": "Critical", "2": "Error", "3": "Warning", "4": "Information", "0": "Information"}[m[1]]
		}
		if m := reEvMsg.FindStringSubmatch(b); m != nil {
			t := strings.Join(strings.Fields(xmlText(reEvTag.ReplaceAllString(m[1], " "))), " ")
			if len(t) > 400 {
				t = t[:400]
			}
			e.Text = t
		}
		if e.ID != "" {
			out = append(out, e)
		}
	}
	return out
}

func xmlText(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&").Replace(s)
}
