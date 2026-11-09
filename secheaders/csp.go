package secheaders

import "strings"

type CSP struct {
	order []string
	m     map[string][]string
}

func NewCSP() *CSP {
	return &CSP{m: map[string][]string{}}
}

func ParseCSP(s string) *CSP {
	c := NewCSP()
	for _, part := range strings.Split(s, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		c.Set(fields[0], fields[1:]...)
	}
	return c
}

func (c *CSP) Clone() *CSP {
	out := NewCSP()
	for _, d := range c.order {
		out.order = append(out.order, d)
		out.m[d] = append([]string(nil), c.m[d]...)
	}
	return out
}

func (c *CSP) Has(directive string) bool {
	_, ok := c.m[strings.ToLower(directive)]
	return ok
}

func (c *CSP) Sources(directive string) []string {
	return append([]string(nil), c.m[strings.ToLower(directive)]...)
}

func (c *CSP) Set(directive string, sources ...string) *CSP {
	directive = strings.ToLower(strings.TrimSpace(directive))
	if directive == "" {
		return c
	}
	if _, ok := c.m[directive]; !ok {
		c.order = append(c.order, directive)
	}
	c.m[directive] = dedupe(sources)
	return c
}

func (c *CSP) Add(directive string, sources ...string) *CSP {
	directive = strings.ToLower(strings.TrimSpace(directive))
	if directive == "" || len(sources) == 0 {
		return c
	}
	if _, ok := c.m[directive]; !ok {
		c.order = append(c.order, directive)
		c.m[directive] = nil
	}
	c.m[directive] = dedupe(append(c.m[directive], sources...))
	return c
}

func (c *CSP) Remove(directive string) *CSP {
	directive = strings.ToLower(strings.TrimSpace(directive))
	if _, ok := c.m[directive]; !ok {
		return c
	}
	delete(c.m, directive)
	for i, d := range c.order {
		if d == directive {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	return c
}

func (c *CSP) String() string {
	parts := make([]string, 0, len(c.order))
	for _, d := range c.order {
		if len(c.m[d]) == 0 {
			parts = append(parts, d)
			continue
		}
		parts = append(parts, d+" "+strings.Join(c.m[d], " "))
	}
	return strings.Join(parts, "; ")
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
