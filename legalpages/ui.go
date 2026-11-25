package legalpages

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed editor.html
var editorHTML string

// AdminOptions wires the editor's POST handler to a product.
type AdminOptions struct {
	Store Store
	// Back is where every action returns to (the settings page showing the editor).
	Back string
	// Actor names who made the change, for the page's updated-by.
	Actor func(r *http.Request) string
	// Flash stores a one-shot message; key is one of the legal_msg_* or legal_err_* keys.
	Flash func(w http.ResponseWriter, r *http.Request, kind, key string)
	// Changed runs after a successful write (cache purge, sitemap).
	Changed func()
	// Audit records an action; optional.
	Audit func(r *http.Request, action string, pageID int)
}

func errKey(slug string) string {
	switch slug {
	case "slug_taken":
		return "legal_err_slug_taken"
	case "bad_slug":
		return "legal_err_bad_slug"
	case "same_as":
		return "legal_err_same_as"
	}
	return "legal_err_generic"
}

func newVariantInput(get func(string) string) (VariantInput, bool) {
	lang, _ := NormalizeLang(get("new_lang"))
	if lang == "" {
		return VariantInput{}, false
	}
	return VariantInput{
		Lang:    lang,
		Slug:    strings.ToLower(strings.TrimSpace(get("new_slug"))),
		Title:   strings.TrimSpace(get("new_title")),
		Body:    get("new_body"),
		Enabled: true,
	}, true
}

// Post handles the add, save and delete actions of the editor.
func Post(o AdminOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, o.Back, http.StatusSeeOther)
			return
		}
		back := func(kind, key string) {
			if o.Flash != nil {
				o.Flash(w, r, kind, key)
			}
			http.Redirect(w, r, o.Back, http.StatusSeeOther)
		}
		if err := r.ParseForm(); err != nil {
			logger.Warn("legalpages: form not parsed: %v", err)
			back("error", "legal_err_generic")
			return
		}
		actor := ""
		if o.Actor != nil {
			actor = o.Actor(r)
		}
		done := func(action string, pageID int, key string) {
			if o.Changed != nil {
				o.Changed()
			}
			if o.Audit != nil {
				o.Audit(r, action, pageID)
			}
			back("success", key)
		}
		store := o.Store
		switch strings.TrimSpace(r.FormValue("lp_action")) {
		case "add":
			v, ok := newVariantInput(r.FormValue)
			if !ok {
				logger.Warn("legalpages: add refused, language empty by %q", actor)
				back("error", "legal_err_generic")
				return
			}
			if r.FormValue("same_as_on") != "1" {
				id, err := store.AddPage([]VariantInput{v}, actor)
				if err != nil {
					logger.Error("legalpages: add lang=%s by %q failed: %v", v.Lang, actor, err)
					back("error", errKey(FlashSlug(err)))
					return
				}
				done("legal_page_added", id, "legal_msg_added")
				return
			}
			id, idErr := strconv.Atoi(r.FormValue("same_as"))
			if idErr != nil || id <= 0 {
				logger.Warn("legalpages: same-as refused, invalid same_as=%q lang=%s by %q", r.FormValue("same_as"), v.Lang, actor)
				back("error", "legal_err_same_as")
				return
			}
			pages, err := store.ListPages()
			if err != nil {
				logger.Error("legalpages: same-as list failed same_as=%d lang=%s by %q: %v", id, v.Lang, actor, err)
				back("error", "legal_err_generic")
				return
			}
			var page *Page
			for i := range pages {
				if pages[i].ID == id {
					page = &pages[i]
				}
			}
			if page == nil {
				logger.Warn("legalpages: same-as refused, page %d not found lang=%s by %q", id, v.Lang, actor)
				back("error", "legal_err_same_as")
				return
			}
			for _, pv := range page.Variants {
				if pv.Exists && SameLang(pv.Lang, v.Lang) {
					logger.Warn("legalpages: same-as refused, page %d already has lang=%s by %q", id, v.Lang, actor)
					back("error", "legal_err_same_as")
					return
				}
			}
			if err := store.SavePage(id, page.Enabled, page.ShowUpdated, []VariantInput{v}, actor); err != nil {
				logger.Error("legalpages: same-as save page=%d lang=%s by %q failed: %v", id, v.Lang, actor, err)
				back("error", errKey(FlashSlug(err)))
				return
			}
			done("legal_page_linked", id, "legal_msg_linked")
		case "save":
			existing, err := store.ListPages()
			if err != nil {
				logger.Error("legalpages: save list failed by %q: %v", actor, err)
				back("error", "legal_err_generic")
				return
			}
			langsByPage := map[int][]string{}
			for _, pg := range existing {
				langsByPage[pg.ID] = pg.Langs()
			}
			ids := ParseIDs(r.FormValue("page_ids"))
			for _, id := range ids {
				pe := r.FormValue(fmt.Sprintf("page_enabled_%d", id)) == "1"
				if err := store.SavePage(id, pe, true, FormVariantsFor(id, langsByPage[id], r.FormValue), actor); err != nil {
					logger.Error("legalpages: save page=%d by %q failed: %v", id, actor, err)
					back("error", errKey(FlashSlug(err)))
					return
				}
			}
			first := 0
			if len(ids) > 0 {
				first = ids[0]
			}
			done("legal_page_saved", first, "legal_msg_saved")
		case "delete":
			id, idErr := strconv.Atoi(r.FormValue("page_id"))
			if idErr != nil {
				logger.Warn("legalpages: delete refused, invalid page_id=%q by %q", r.FormValue("page_id"), actor)
				back("error", "legal_err_generic")
				return
			}
			if err := store.DeletePage(id); err != nil {
				logger.Error("legalpages: delete page=%d by %q failed: %v", id, actor, err)
				back("error", "legal_err_generic")
				return
			}
			done("legal_page_deleted", id, "legal_msg_deleted")
		default:
			http.Redirect(w, r, o.Back, http.StatusSeeOther)
		}
	}
}

// SlugCheck answers the editor's inline "is this slug free" question.
func SlugCheck(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("slug")))
		pid := 0
		if raw := strings.TrimSpace(r.URL.Query().Get("page_id")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				logger.Warn("legalpages: slug check with invalid page_id %q: %v", raw, err)
				http.Error(w, "invalid page_id", http.StatusBadRequest)
				return
			}
			pid = n
		}
		avail := false
		if slug != "" && SlugRe.MatchString(slug) {
			inUse, err := store.SlugInUse(slug, pid)
			if err != nil {
				logger.Error("legalpages: slug check for %q failed: %v", slug, err)
			}
			avail = err == nil && !inUse
		}
		writeJSONBody(w, map[string]any{"available": avail})
	}
}

// LangResolve previews how a typed language name will be stored.
func LangResolve(viewerLang func(r *http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code, resolved := NormalizeLang(r.URL.Query().Get("q"))
		label := ""
		if code != "" {
			label = LanguageLabel(code, viewerLang(r))
		}
		writeJSONBody(w, map[string]any{"code": code, "label": label, "resolved": resolved})
	}
}

func writeJSONBody(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Warn("legalpages: JSON reply not delivered: %v", err)
	}
}

// EditorOptions shapes the editor card.
type EditorOptions struct {
	Store Store
	// T returns the translation of a key, or "" when unknown.
	T func(key string) string
	// IconHref returns the sprite href of an icon name ("plus").
	IconHref func(name string) string
	// Lang is the viewer's language, for language names.
	Lang                          string
	PostURL, CheckURL, ResolveURL string
	CSRFName, CSRFValue           string
}

type editorView struct {
	Pages                         []Page
	LangSuggestions               []string
	PostURL, CheckURL, ResolveURL string
	CSRFName, CSRFValue           string
}

// Editor renders the legal pages card.
func Editor(o EditorOptions) template.HTML {
	pages, err := o.Store.ListPages()
	if err != nil {
		logger.Error("legalpages: editor could not list pages: %v", err)
	}
	t := func(key string) string {
		if o.T != nil {
			if v := o.T(key); v != "" && v != key {
				return v
			}
		}
		return key
	}
	icon := func(name string) string {
		if o.IconHref == nil {
			return "#fa-" + name
		}
		return o.IconHref(name)
	}
	tpl, err := template.New("legal-editor").Funcs(template.FuncMap{
		"t":        t,
		"icon":     icon,
		"langName": func(code string) string { return LanguageLabel(code, o.Lang) },
	}).Parse(editorHTML)
	if err != nil {
		logger.Error("legalpages: editor template invalid: %v", err)
		return ""
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, editorView{Pages: pages, LangSuggestions: LanguageSuggestions(o.Lang), PostURL: o.PostURL, CheckURL: o.CheckURL, ResolveURL: o.ResolveURL, CSRFName: o.CSRFName, CSRFValue: o.CSRFValue}); err != nil {
		logger.Error("legalpages: editor render failed: %v", err)
		return ""
	}
	return template.HTML(buf.String())
}

// PageView is what a public legal page template renders.
type PageView struct {
	Title       string
	BodyHTML    template.HTML
	Lang        string
	UpdatedAt   string
	ShowUpdated bool
	Alternates  []Alt
	JSONLD      template.JS
}

// Prepare resolves language preference and builds the view for a public page.
// It returns false when it already redirected to another language version.
func Prepare(w http.ResponseWriter, r *http.Request, res Resolved, resolve func(string) (Resolved, bool), activeLang string) (PageView, bool) {
	if pl := r.URL.Query().Get("page_lang"); pl != "" {
		for _, a := range res.Alternates {
			if a.Lang == pl && a.Slug != "" {
				http.Redirect(w, r, "/"+url.PathEscape(a.Slug)+"?pick=1", http.StatusSeeOther)
				return PageView{}, false
			}
		}
	}
	if r.URL.Query().Get("pick") != "1" && resolve != nil {
		res = PreferLang(res, activeLang, resolve)
	}
	proto := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		proto = "http"
	}
	doc := map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": res.Title, "url": proto + "://" + r.Host + r.URL.Path}
	if res.Lang != "" {
		doc["inLanguage"] = res.Lang
	}
	ld, err := json.Marshal(doc)
	if err != nil {
		logger.Error("legalpages: JSON-LD for page %d failed: %v", res.PageID, err)
	}
	return PageView{Title: res.Title, BodyHTML: SafeHTML(res.Body), Lang: res.Lang, UpdatedAt: res.UpdatedAt, ShowUpdated: res.ShowUpdated, Alternates: res.Alternates, JSONLD: template.JS(ld)}, true
}
