package notifykit

import (
	"bytes"
	"html/template"
	"net/http"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// PrefsOptions shapes the channel preferences card.
type PrefsOptions struct {
	T func(key string) string
	// Action is where the form posts ("/account/notifications").
	Action string
	// Channels are the channels this product and this person can use, in
	// order; a channel that is missing (Telegram not linked) is not shown.
	Channels []string
	Events   []EventDef
	// Values holds the person's stored choices ("category/channel").
	Values map[string]bool
	// Hints maps a channel to a short note shown under its column (for
	// example "link Telegram first").
	Hints               map[string]string
	CSRFName, CSRFValue string
}

func (o PrefsOptions) tr(key, fallback string) string {
	if o.T == nil {
		return fallback
	}
	v := o.T(key)
	if v == "" || v == key {
		return fallback
	}
	return v
}

type prefRow struct {
	Category string
	Label    string
	Cells    []prefCell
}

type prefCell struct {
	Key     string
	Checked bool
	Locked  bool
}

// Matrix builds the rows of the preferences card: one per category that has
// at least one event, with one switch per channel.
func (o PrefsOptions) Matrix() []prefRow {
	var rows []prefRow
	idx := map[string]int{}
	for _, ev := range o.Events {
		i, ok := idx[ev.Category]
		if !ok {
			idx[ev.Category] = len(rows)
			rows = append(rows, prefRow{Category: ev.Category, Label: o.tr("notifykit.cat."+ev.Category, ev.Category), Cells: make([]prefCell, len(o.Channels))})
			i = len(rows) - 1
		}
		for c, ch := range o.Channels {
			cell := &rows[i].Cells[c]
			cell.Key = ev.Category + "/" + ch
			if contains(ev.Locked, ch) {
				cell.Locked, cell.Checked = true, true
				continue
			}
			if cell.Locked {
				continue
			}
			if v, set := o.Values[cell.Key]; set {
				cell.Checked = v
			} else if contains(ev.Defaults, ch) {
				cell.Checked = true
			}
		}
	}
	return rows
}

const prefsHTML = `<form method="post" action="{{.O.Action}}" class="stack al-prefs">{{if .O.CSRFName}}<input type="hidden" name="{{.O.CSRFName}}" value="{{.O.CSRFValue}}">{{end}}
<div class="table-container"><table class="table nk-prefs"><thead><tr><th>{{t "notifykit.event" "Notification"}}</th>{{range .O.Channels}}<th>{{ch .}}</th>{{end}}</tr></thead>
<tbody>{{range .Rows}}<tr><td>{{.Label}}</td>{{range .Cells}}<td><div class="tswitch"><input type="checkbox" class="tswitch-input" id="np-{{.Key}}" name="pref" value="{{.Key}}"{{if .Checked}} checked{{end}}{{if .Locked}} disabled{{end}}><label for="np-{{.Key}}" class="tswitch-label"><div class="ball"></div></label></div></td>{{end}}</tr>{{end}}</tbody></table></div>
{{range .O.Channels}}{{with index $.O.Hints .}}<p class="opacity-70 text-sm">{{.}}</p>{{end}}{{end}}
<button type="submit" class="btn btn-primary">{{t "notifykit.save" "Save"}}</button></form>`

// PrefsPanel renders the events-by-channels preferences card.
func PrefsPanel(o PrefsOptions) template.HTML {
	tpl, err := template.New("prefs").Funcs(template.FuncMap{
		"t":  func(key, fallback string) string { return o.tr(key, fallback) },
		"ch": func(c any) string { return o.tr("notifykit.chan."+c.(string), c.(string)) },
	}).Parse(`{{define "x"}}` + prefsHTML + `{{end}}`)
	if err != nil {
		logger.Error("notifykit: preferences template is invalid: %v", err)
		return ""
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "x", map[string]any{"O": o, "Rows": o.Matrix()}); err != nil {
		logger.Error("notifykit: preferences render failed: %v", err)
		return ""
	}
	return template.HTML(buf.String())
}

// ParsePrefs reads the submitted switches. Every category/channel pair the
// panel showed gets an explicit value, so unchecking sticks.
func ParsePrefs(r *http.Request, events []EventDef, channels []string) map[string]bool {
	if err := r.ParseForm(); err != nil {
		logger.Warn("notifykit: preferences form not parsed: %v", err)
	}
	on := map[string]bool{}
	for _, v := range r.Form["pref"] {
		on[v] = true
	}
	out := map[string]bool{}
	for _, ev := range events {
		for _, ch := range channels {
			if contains(ev.Locked, ch) {
				continue
			}
			key := ev.Category + "/" + ch
			out[key] = on[key]
		}
	}
	return out
}
