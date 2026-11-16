// Package discountpresets is the shared implementation of named, reusable
// discounts that staff pick at sale time next to the free-form fixed amount
// and percentage options. A preset is a label plus either an amount off (in
// minor currency units) or a percentage off (whole percent).
package discountpresets

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	KindAmount     = "amount"
	KindPercentage = "percentage"

	MaxLabelLen = 80
)

var (
	ErrLabelRequired = errors.New("discountpresets: label required")
	ErrBadKind       = errors.New("discountpresets: kind must be amount or percentage")
	ErrBadValue      = errors.New("discountpresets: value out of range")
	ErrNotFound      = errors.New("discountpresets: preset not found")
	ErrBadTable      = errors.New("discountpresets: invalid table name")
)

var tableRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

type Preset struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Value     int64  `json:"value"`
	Active    bool   `json:"active"`
	SortOrder int    `json:"sort_order"`
}

// Validate normalises the label and checks kind and value. An amount must be
// positive; a percentage must be 1-100.
func (p *Preset) Validate() error {
	p.Label = strings.TrimSpace(p.Label)
	if p.Label == "" || len([]rune(p.Label)) > MaxLabelLen {
		return ErrLabelRequired
	}
	switch p.Kind {
	case KindAmount:
		if p.Value <= 0 {
			return ErrBadValue
		}
	case KindPercentage:
		if p.Value < 1 || p.Value > 100 {
			return ErrBadValue
		}
	default:
		return ErrBadKind
	}
	return nil
}

// Apply returns priceCents after taking the discount off, never below zero.
// An unknown kind returns the price unchanged.
func Apply(priceCents int64, kind string, value int64) int64 {
	if priceCents <= 0 || value <= 0 {
		return priceCents
	}
	switch kind {
	case KindAmount:
		if value >= priceCents {
			return 0
		}
		return priceCents - value
	case KindPercentage:
		if value > 100 {
			value = 100
		}
		return priceCents - (priceCents*value)/100
	}
	return priceCents
}

// Apply is the preset's own discount applied to a price.
func (p Preset) Apply(priceCents int64) int64 {
	return Apply(priceCents, p.Kind, p.Value)
}

type Store struct {
	DB    *sql.DB
	Table string
	MySQL bool
}

func (s Store) tbl() (string, error) {
	if !tableRe.MatchString(s.Table) {
		return "", ErrBadTable
	}
	if s.MySQL {
		return "`" + s.Table + "`", nil
	}
	return `"` + s.Table + `"`, nil
}

// List returns presets ordered for display. activeOnly hides disabled ones
// (the sale-time picker); the management screen passes false.
func (s Store) List(activeOnly bool) ([]Preset, error) {
	t, err := s.tbl()
	if err != nil {
		return nil, err
	}
	q := `SELECT id, label, kind, value, active, sort_order FROM ` + t
	if activeOnly {
		q += ` WHERE active = 1`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", s.Table, err)
	}
	defer rows.Close()
	out := []Preset{}
	for rows.Next() {
		var p Preset
		var active int
		if err := rows.Scan(&p.ID, &p.Label, &p.Kind, &p.Value, &active, &p.SortOrder); err != nil {
			return nil, fmt.Errorf("scan %s: %w", s.Table, err)
		}
		p.Active = active != 0
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s: %w", s.Table, err)
	}
	return out, nil
}

// Get returns one preset, or ErrNotFound.
func (s Store) Get(id int64) (Preset, error) {
	t, err := s.tbl()
	if err != nil {
		return Preset{}, err
	}
	var p Preset
	var active int
	err = s.DB.QueryRow(`SELECT id, label, kind, value, active, sort_order FROM `+t+` WHERE id = ?`, id).
		Scan(&p.ID, &p.Label, &p.Kind, &p.Value, &active, &p.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return Preset{}, ErrNotFound
	}
	if err != nil {
		return Preset{}, fmt.Errorf("get %s id=%d: %w", s.Table, id, err)
	}
	p.Active = active != 0
	return p, nil
}

// Save inserts (ID == 0) or updates a preset after validating it and returns
// the stored ID.
func (s Store) Save(p Preset) (int64, error) {
	t, err := s.tbl()
	if err != nil {
		return 0, err
	}
	if err := p.Validate(); err != nil {
		return 0, err
	}
	active := 0
	if p.Active {
		active = 1
	}
	if p.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO `+t+` (label, kind, value, active, sort_order) VALUES (?, ?, ?, ?, ?)`,
			p.Label, p.Kind, p.Value, active, p.SortOrder)
		if err != nil {
			return 0, fmt.Errorf("insert %s label=%q: %w", s.Table, p.Label, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("insert %s label=%q last id: %w", s.Table, p.Label, err)
		}
		return id, nil
	}
	res, err := s.DB.Exec(`UPDATE `+t+` SET label = ?, kind = ?, value = ?, active = ?, sort_order = ? WHERE id = ?`,
		p.Label, p.Kind, p.Value, active, p.SortOrder, p.ID)
	if err != nil {
		return 0, fmt.Errorf("update %s id=%d: %w", s.Table, p.ID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		if _, gerr := s.Get(p.ID); gerr != nil {
			return 0, gerr
		}
	}
	return p.ID, nil
}

// Delete removes a preset. Sales already made keep the amount they were
// charged; presets are only a convenience at sale time.
func (s Store) Delete(id int64) error {
	t, err := s.tbl()
	if err != nil {
		return err
	}
	res, err := s.DB.Exec(`DELETE FROM `+t+` WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete %s id=%d: %w", s.Table, id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}
