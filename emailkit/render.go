package emailkit

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"path"
)

func ParseFS(th Theme, fsys fs.FS, file string, extra ...template.FuncMap) (*template.Template, error) {
	base := template.New(path.Base(file)).Funcs(Funcs(th))
	for _, fm := range extra {
		base = base.Funcs(fm)
	}
	t, err := base.ParseFS(fsys, file)
	if err != nil {
		return nil, fmt.Errorf("emailkit: parse %s: %w", file, err)
	}
	return t, nil
}

func Render(t *template.Template, data any, th Theme, p Page) (string, error) {
	var body bytes.Buffer
	if err := t.Execute(&body, data); err != nil {
		return "", fmt.Errorf("emailkit: execute %s: %w", t.Name(), err)
	}
	return string(Wrap(th, p, template.HTML(body.String()))), nil
}
