package emailkit

import (
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
)

type Page struct {
	Lang      string
	Title     string
	Rights    string
	Footer    template.HTML
	BrandURL  string
	PoweredBy template.HTML
	Year      int
}

func esc(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case template.HTML:
		return string(x)
	case string:
		return template.HTMLEscapeString(x)
	default:
		return template.HTMLEscapeString(fmt.Sprint(x))
	}
}

func safeURL(raw any) string {
	s := strings.TrimSpace(fmt.Sprint(raw))
	if s == "" {
		return "#"
	}
	u, err := url.Parse(s)
	if err != nil {
		return "#"
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel":
		return template.HTMLEscapeString(s)
	}
	return "#"
}

func Link(href, text string) template.HTML {
	u := safeURL(href)
	if strings.TrimSpace(href) == "" || u == "#" {
		return template.HTML(esc(text))
	}
	return template.HTML(`<a href="` + u + `" style="color:inherit;text-decoration:underline;">` + esc(text) + `</a>`)
}

func Wrap(th Theme, p Page, body template.HTML) template.HTML {
	year := p.Year
	if year == 0 {
		year = time.Now().Year()
	}
	lang := strings.TrimSpace(p.Lang)
	if lang == "" {
		lang = "en"
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="` + template.HTMLEscapeString(lang) + `"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><meta name="color-scheme" content="light"><title>` + esc(p.Title) + `</title></head>`)
	b.WriteString(`<body style="margin:0;padding:0;background:` + th.Background + `;font-family:'Segoe UI',Tahoma,Geneva,Verdana,sans-serif;color:` + th.Text + `;line-height:1.6;">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:` + th.Background + `;"><tr><td align="center" style="padding:24px 16px;">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:600px;background:` + th.CardBG + `;border:1px solid ` + th.Border + `;border-radius:12px;">`)
	b.WriteString(`<tr><td style="padding:28px 32px 8px;">`)
	if th.LogoURL != "" {
		b.WriteString(`<img src="` + safeURL(th.LogoURL) + `" alt="` + esc(th.BrandName) + `" style="display:block;max-height:48px;max-width:200px;border:0;">`)
	} else {
		b.WriteString(`<div style="font-size:22px;font-weight:700;color:` + th.Primary + `;">` + esc(th.BrandName) + `</div>`)
	}
	b.WriteString(`</td></tr>`)
	b.WriteString(`<tr><td style="padding:8px 32px 24px;font-size:15px;color:` + th.Text + `;">`)
	b.WriteString(string(body))
	b.WriteString(`</td></tr>`)
	b.WriteString(`<tr><td style="padding:18px 32px;border-top:1px solid ` + th.Border + `;font-size:12px;color:` + th.MutedText + `;">`)
	brand := esc(th.BrandName)
	if u := safeURL(p.BrandURL); strings.TrimSpace(p.BrandURL) != "" && u != "#" {
		brand = `<a href="` + u + `" style="color:` + th.MutedText + `;text-decoration:underline;">` + brand + `</a>`
	}
	b.WriteString(`<div>&copy; ` + fmt.Sprint(year) + ` ` + brand)
	if r := strings.TrimSpace(p.Rights); r != "" {
		b.WriteString(`. ` + esc(r))
	}
	b.WriteString(`</div>`)
	if string(p.Footer) != "" {
		b.WriteString(`<div style="margin-top:6px;">` + string(p.Footer) + `</div>`)
	}
	if string(p.PoweredBy) != "" {
		b.WriteString(`<div style="margin-top:6px;">` + string(p.PoweredBy) + `</div>`)
	}
	b.WriteString(`</td></tr></table></td></tr></table></body></html>`)
	return template.HTML(b.String())
}

func Funcs(th Theme) template.FuncMap {
	return template.FuncMap{
		"th": func() Theme { return th },
		"heading": func(v any) template.HTML {
			return template.HTML(`<h1 style="margin:0 0 14px;font-size:20px;line-height:1.3;color:` + th.Text + `;">` + esc(v) + `</h1>`)
		},
		"para": func(v any) template.HTML {
			return template.HTML(`<p style="margin:0 0 16px;font-size:15px;line-height:1.6;color:` + th.Text + `;">` + esc(v) + `</p>`)
		},
		"note": func(v any) template.HTML {
			return template.HTML(`<p style="margin:0 0 16px;font-size:13px;line-height:1.5;color:` + th.MutedText + `;">` + esc(v) + `</p>`)
		},
		"button": func(href, label any) template.HTML {
			return template.HTML(`<div style="margin:22px 0;"><a href="` + safeURL(href) + `" style="display:inline-block;padding:13px 26px;font-size:15px;font-weight:600;color:` + th.ButtonText + `;background:` + th.Primary + `;border-radius:8px;text-decoration:none;">` + esc(label) + `</a></div>`)
		},
		"linkline": func(href any) template.HTML {
			u := safeURL(href)
			return template.HTML(`<p style="margin:0 0 16px;font-size:13px;word-break:break-all;"><a href="` + u + `" style="color:` + th.Primary + `;">` + u + `</a></p>`)
		},
		"alert": func(kind string, v any) template.HTML {
			c := th.Info
			switch kind {
			case "success":
				c = th.Success
			case "warning":
				c = th.Warning
			case "error":
				c = th.Error
			}
			return template.HTML(`<div style="margin:0 0 16px;padding:12px 16px;border:1px solid ` + th.Border + `;border-left:4px solid ` + c + `;border-radius:8px;font-size:14px;color:` + th.Text + `;">` + esc(v) + `</div>`)
		},
		"code": func(v any) template.HTML {
			return template.HTML(`<div style="margin:0 0 16px;padding:14px 16px;border:1px solid ` + th.Border + `;border-radius:8px;text-align:center;font-family:Consolas,Menlo,monospace;font-size:22px;letter-spacing:4px;font-weight:700;color:` + th.Text + `;">` + esc(v) + `</div>`)
		},
		"kv": func(label, value any) template.HTML {
			return template.HTML(`<tr><td style="padding:8px 0;border-bottom:1px solid ` + th.Border + `;font-size:14px;color:` + th.MutedText + `;">` + esc(label) + `</td><td align="right" style="padding:8px 0;border-bottom:1px solid ` + th.Border + `;font-size:14px;font-weight:600;color:` + th.Text + `;">` + esc(value) + `</td></tr>`)
		},
		"kvmono": func(label, value any) template.HTML {
			return template.HTML(`<tr><td style="padding:8px 0;border-bottom:1px solid ` + th.Border + `;font-size:14px;color:` + th.MutedText + `;">` + esc(label) + `</td><td align="right" style="padding:8px 0;border-bottom:1px solid ` + th.Border + `;font-family:Consolas,Menlo,monospace;font-size:14px;font-weight:600;color:` + th.Text + `;">` + esc(value) + `</td></tr>`)
		},
		"kvtable": func(rows ...template.HTML) template.HTML {
			var b strings.Builder
			b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:0 0 16px;border-collapse:collapse;">`)
			for _, r := range rows {
				b.WriteString(string(r))
			}
			b.WriteString(`</table>`)
			return template.HTML(b.String())
		},
	}
}
