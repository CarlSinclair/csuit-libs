package accountkit

import (
	"bytes"
	"html/template"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// PanelOptions shapes the shared danger-zone card for products whose pages use
// the standard card, button and modal classes.
type PanelOptions struct {
	// T returns the translation of a key, or the key or "" when unknown.
	T func(key string) string
	// Icon returns the sprite href of an icon name ("trash").
	Icon func(name string) string
	// Prefix is where the handlers are mounted ("/account").
	Prefix string
	// Subject says what the person types when they have no password
	// ("email address" wording comes from locale key accountkit.confirm_label).
	Subject Subject
	// NeedsCode shows a 2FA code field on the delete and deactivate forms.
	NeedsCode bool
	// HasDeactivate shows the step-away row.
	HasDeactivate bool
	// CSRF is a hidden token name/value pair, when the product uses one.
	CSRFName, CSRFValue string
}

func (o PanelOptions) tr(key, fallback string) string {
	if o.T == nil {
		return fallback
	}
	v := o.T(key)
	if v == "" || v == key {
		return fallback
	}
	return v
}

const panelHTML = `<section class="card card-danger" id="danger-zone">
<h2>{{t "accountkit.title" "Danger zone"}}</h2>
<div class="flex flex-between security-row"><div class="security-row-label"><svg class="icon"><use href="{{icon "download"}}"></use></svg><span>{{t "accountkit.export" "Download my data"}}<br><small class="opacity-70">{{t "accountkit.export_hint" "A JSON file with everything we hold about you."}}</small></span></div><a href="{{.P}}/export" class="btn btn-sm btn-icon-only btn-secondary" title="{{t "accountkit.export" "Download my data"}}" aria-label="{{t "accountkit.export" "Download my data"}}"><svg class="icon"><use href="{{icon "download"}}"></use></svg></a></div>
<hr class="mv-20">
<div class="flex flex-between security-row"><div class="security-row-label"><svg class="icon"><use href="{{icon "logout"}}"></use></svg><span>{{t "accountkit.sessions" "Sign out everywhere"}}<br><small class="opacity-70">{{t "accountkit.sessions_hint" "Ends every other session on every device."}}</small></span></div>
<form method="post" action="{{.P}}/logout-all" class="inline-form" data-confirm="{{t "accountkit.sessions_confirm" "Sign out of all other devices?"}}">{{if .O.CSRFName}}<input type="hidden" name="{{.O.CSRFName}}" value="{{.O.CSRFValue}}">{{end}}<button type="submit" class="btn btn-sm btn-icon-only btn-secondary" title="{{t "accountkit.sessions" "Sign out everywhere"}}" aria-label="{{t "accountkit.sessions" "Sign out everywhere"}}"><svg class="icon"><use href="{{icon "logout"}}"></use></svg></button></form></div>
{{if .O.HasDeactivate}}<hr class="mv-20">
<div class="flex flex-between security-row"><div class="security-row-label"><svg class="icon"><use href="{{icon "pause"}}"></use></svg><span>{{t "accountkit.deactivate" "Deactivate my account"}}<br><small class="opacity-70">{{t "accountkit.deactivate_hint" "Reversible. Signing in again reactivates it."}}</small></span></div><a href="#modal-deactivate-account" class="btn btn-sm btn-icon-only btn-secondary" title="{{t "accountkit.deactivate" "Deactivate my account"}}" aria-label="{{t "accountkit.deactivate" "Deactivate my account"}}"><svg class="icon"><use href="{{icon "pause"}}"></use></svg></a></div>{{end}}
<hr class="mv-20">
<div class="flex flex-between security-row"><div class="security-row-label"><svg class="icon"><use href="{{icon "trash"}}"></use></svg><span>{{t "accountkit.delete" "Delete my account"}}<br><small class="opacity-70">{{t "accountkit.delete_hint" "Permanent. This cannot be undone."}}</small></span></div><a href="#modal-delete-account" class="btn btn-sm btn-icon-only btn-danger" title="{{t "accountkit.delete" "Delete my account"}}" aria-label="{{t "accountkit.delete" "Delete my account"}}"><svg class="icon"><use href="{{icon "trash"}}"></use></svg></a></div>
</section>
{{if .O.HasDeactivate}}<div id="modal-deactivate-account" class="modal"><a href="#" class="modal-close-overlay"></a><div class="modal-content"><a href="#" class="close-x">&times;</a>
<div class="modal-header"><h2>{{t "accountkit.deactivate" "Deactivate my account"}}</h2></div>
<p>{{t "accountkit.deactivate_warning" "You will be signed out everywhere. Sign in again to come back."}}</p>
<form method="post" action="{{.P}}/deactivate" class="stack">{{template "proof" (dict "O" .O "ID" "dea" "T" .T)}}
<div class="modal-footer"><a href="#" class="btn btn-secondary">{{t "accountkit.cancel" "Cancel"}}</a><button type="submit" class="btn btn-danger">{{t "accountkit.deactivate" "Deactivate my account"}}</button></div></form>
</div></div>{{end}}
<div id="modal-delete-account" class="modal"><a href="#" class="modal-close-overlay"></a><div class="modal-content"><a href="#" class="close-x">&times;</a>
<div class="modal-header"><h2>{{t "accountkit.delete" "Delete my account"}}</h2></div>
<p>{{t "accountkit.delete_warning" "Your account and access are removed for good."}}</p>
<form method="post" action="{{.P}}/delete" class="stack">{{template "proof" (dict "O" .O "ID" "del" "T" .T)}}
<div class="modal-footer"><a href="#" class="btn btn-secondary">{{t "accountkit.cancel" "Cancel"}}</a><button type="submit" class="btn btn-danger">{{t "accountkit.delete" "Delete my account"}}</button></div></form>
</div></div>`

const proofHTML = `{{define "proof"}}{{if .O.CSRFName}}<input type="hidden" name="{{.O.CSRFName}}" value="{{.O.CSRFValue}}">{{end}}{{if .O.Subject.HasPassword}}<div class="form-group"><label for="{{.ID}}-pw">{{call .T "accountkit.password" "Current password"}}</label><input id="{{.ID}}-pw" name="password" type="password" autocomplete="current-password" required></div>{{else}}<div class="form-group"><label for="{{.ID}}-confirm">{{call .T "accountkit.confirm_label" "Type your email address to confirm"}}</label><input id="{{.ID}}-confirm" name="confirm" type="text" autocomplete="off" placeholder="{{.O.Subject.Label}}" required></div>{{end}}{{if .O.NeedsCode}}<div class="form-group"><label for="{{.ID}}-code">{{call .T "accountkit.code" "Authentication code"}}</label><input id="{{.ID}}-code" name="totp_code" type="text" autocomplete="one-time-code"></div>{{end}}{{end}}`

// Panel renders the danger-zone card and its two modals. The returned HTML
// belongs on the account page, on its security (or account) tab.
func Panel(o PanelOptions) template.HTML {
	if o.Prefix == "" {
		o.Prefix = "/account"
	}
	t := func(key, fallback string) string { return o.tr(key, fallback) }
	tpl, err := template.New("accountkit").Funcs(template.FuncMap{
		"t": t,
		"icon": func(name string) string {
			if o.Icon != nil {
				return o.Icon(name)
			}
			return "/assets/img/icons.svg#fa-" + name
		},
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i].(string)] = kv[i+1]
			}
			return m
		},
	}).Parse(proofHTML + panelHTML)
	if err != nil {
		logger.Error("accountkit: panel template is invalid: %v", err)
		return ""
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "accountkit", map[string]any{"O": o, "P": o.Prefix, "T": t}); err != nil {
		logger.Error("accountkit: panel render failed: %v", err)
		return ""
	}
	return template.HTML(buf.String())
}
