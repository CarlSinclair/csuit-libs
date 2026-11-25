package auditlog

import (
	"bytes"
	"html/template"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const pageHTML = `<link rel="stylesheet" href="/static/shared/auditlog.css">
<div class="card">
<form method="get" action="{{.V.Base}}" class="al-filters" data-auditlog-filter>
<div class="form-group"><label for="al-from">{{t "auditlog.from" "From"}}</label><input id="al-from" type="date" name="date_from" value="{{.V.DateFrom}}"></div>
<div class="form-group"><label for="al-to">{{t "auditlog.to" "To"}}</label><input id="al-to" type="date" name="date_to" value="{{.V.DateTo}}"></div>
<div class="form-group"><label for="al-user">{{t "auditlog.user" "User"}}</label><select id="al-user" name="user_id"><option value="">{{t "auditlog.all_users" "All users"}}</option>{{range .V.Users}}<option value="{{.Value}}"{{if eq .Value $.V.FilterUser}} selected{{end}}>{{.Label}}</option>{{end}}</select></div>
<div class="form-group"><label for="al-action">{{t "auditlog.action" "Action"}}</label><select id="al-action" name="action"><option value="">{{t "auditlog.all_actions" "All actions"}}</option>{{range .V.Actions}}<option value="{{.Value}}"{{if eq .Value $.V.Filter.Action}} selected{{end}}>{{.Label}}</option>{{end}}</select></div>
{{if .V.Statuses}}<div class="form-group"><label for="al-status">{{t "auditlog.status" "Status"}}</label><select id="al-status" name="status"><option value="">{{t "auditlog.all_statuses" "All statuses"}}</option>{{range .V.Statuses}}<option value="{{.Value}}"{{if eq .Value $.V.Filter.Status}} selected{{end}}>{{.Label}}</option>{{end}}</select></div>{{end}}
<div class="form-group"><label for="al-q">{{t "auditlog.search" "Search"}}</label><input id="al-q" type="text" name="q" value="{{.V.Filter.Q}}" maxlength="100"></div>
{{if .V.Applied}}<a class="btn btn-secondary" href="{{.V.Base}}">{{t "auditlog.clear" "Clear"}}</a>{{end}}
</form>
</div>
{{if .V.Rows}}<div class="card"><div class="al-head"><span class="opacity-70">{{.V.Total}} {{t "auditlog.entries" "entries"}}</span><a class="btn btn-sm btn-secondary" href="{{.V.CSV}}">{{t "auditlog.export" "Export CSV"}}</a></div>
<div class="table-container"><table class="table"><thead><tr><th>{{t "auditlog.when" "When"}}</th><th>{{t "auditlog.user" "User"}}</th><th>{{t "auditlog.action" "Action"}}</th>{{if .V.Statuses}}<th>{{t "auditlog.status" "Status"}}</th>{{end}}<th>{{t "auditlog.location" "Location"}}</th><th></th></tr></thead>
<tbody>{{range .V.Rows}}<tr><td><time data-local-time datetime="{{.ISO}}">{{.Fallback}}</time></td><td>{{if .UserLink}}<a href="{{.UserLink}}">{{.User}}</a>{{else}}{{.User}}{{end}}</td><td>{{if .Link}}<a href="{{.Link}}">{{.Action}}</a>{{else}}{{.Action}}{{end}}</td>{{if $.V.Statuses}}<td><span class="al-status al-status-{{.Status}}">{{.Status}}</span></td>{{end}}<td>{{.Location}}</td>
<td><a class="btn btn-sm btn-icon-only btn-secondary" href="#modal-audit-{{.ID}}" title="{{t "auditlog.details" "Details"}}" aria-label="{{t "auditlog.details" "Details"}}"><svg class="icon"><use href="{{icon "search"}}"></use></svg></a></td></tr>{{end}}</tbody></table></div></div>
{{range .V.Rows}}<div id="modal-audit-{{.ID}}" class="modal"><a href="#" class="modal-close-overlay"></a><div class="modal-content"><a href="#" class="close-x">&times;</a>
<div class="modal-header"><h2>{{.Action}}</h2></div>
<dl class="al-details"><dt>{{t "auditlog.when" "When"}}</dt><dd><time data-local-time datetime="{{.ISO}}">{{.Fallback}}</time></dd><dt>{{t "auditlog.user" "User"}}</dt><dd>{{.User}}</dd>{{if .Location}}<dt>{{t "auditlog.location" "Location"}}</dt><dd>{{.Location}}</dd>{{end}}{{if .Agent}}<dt>{{t "auditlog.device" "Device"}}</dt><dd>{{.Agent}}</dd>{{end}}
{{range .Lines}}<dt>{{.Label}}</dt><dd>{{if .Link}}<a href="{{.Link}}">{{.Value}}</a>{{else}}{{.Value}}{{end}}</dd>{{end}}</dl>
</div></div>{{end}}
<div class="al-pager">{{if .V.Prev}}<a class="btn btn-sm btn-secondary" href="{{.V.Prev}}">{{t "auditlog.prev" "Previous"}}</a>{{end}}{{if .V.Next}}<a class="btn btn-sm btn-secondary" href="{{.V.Next}}">{{t "auditlog.next" "Next"}}</a>{{end}}</div>
{{else}}<p class="opacity-70">{{t "auditlog.empty" "No activity recorded yet."}}</p>{{end}}
<script src="/static/shared/localtime.js" defer></script>
<script src="/static/shared/auditlog.js" defer></script>`

// Render returns the whole audit page body (filters, table, details modals,
// paging) to embed inside a product's own layout.
func Render(v View, o PageOptions) template.HTML {
	tpl, err := template.New("auditlog").Funcs(template.FuncMap{
		"t": func(key, fallback string) string { return o.tr(key, fallback) },
		"icon": func(name string) string {
			if o.Icon != nil {
				return o.Icon(name)
			}
			return "/assets/img/icons.svg#fa-" + name
		},
	}).Parse(pageHTML)
	if err != nil {
		logger.Error("auditlog: page template is invalid: %v", err)
		return ""
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, map[string]any{"V": v}); err != nil {
		logger.Error("auditlog: page render failed: %v", err)
		return ""
	}
	return template.HTML(buf.String())
}
