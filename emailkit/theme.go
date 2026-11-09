package emailkit

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

type Theme struct {
	BrandName    string
	LogoURL      string
	Primary      string
	PrimaryHover string
	Text         string
	MutedText    string
	CardBG       string
	Border       string
	Background   string
	ButtonText   string
	Success      string
	Warning      string
	Error        string
	Info         string
}

type Overrides struct {
	BrandName    string
	LogoURL      string
	Primary      string
	PrimaryHover string
	Text         string
	MutedText    string
	CardBG       string
	Border       string
	Background   string
	Success      string
	Warning      string
	Error        string
	Info         string
}

const (
	DefaultPrimary      = "#8e44ad"
	DefaultPrimaryHover = "#732d91"
	DefaultText         = "#2c3e50"
	DefaultMutedText    = "#64748b"
	DefaultCardBG       = "#ffffff"
	DefaultBorder       = "#e2e8f0"
	DefaultBackground   = "#f4f6f8"
	DefaultSuccess      = "#10b981"
	DefaultWarning      = "#f59e0b"
	DefaultError        = "#ef4444"
)

var colorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|(?i:rgba?|hsla?)\(\s*[0-9.,%\s/-]+\))$`)

func SafeColor(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" && colorPattern.MatchString(v) {
		return v
	}
	return fallback
}

func Platform(brand string) Theme {
	return Theme{
		BrandName:    strings.TrimSpace(brand),
		Primary:      DefaultPrimary,
		PrimaryHover: DefaultPrimaryHover,
		Text:         DefaultText,
		MutedText:    DefaultMutedText,
		CardBG:       DefaultCardBG,
		Border:       DefaultBorder,
		Background:   DefaultBackground,
		ButtonText:   "#ffffff",
		Success:      DefaultSuccess,
		Warning:      DefaultWarning,
		Error:        DefaultError,
		Info:         DefaultPrimary,
	}
}

func Merchant(platformBrand string, o Overrides) Theme {
	t := Platform(platformBrand)
	if name := strings.TrimSpace(o.BrandName); name != "" {
		t.BrandName = name
	}
	t.LogoURL = strings.TrimSpace(o.LogoURL)
	primaryChanged := false
	if c := SafeColor(o.Primary, ""); c != "" {
		t.Primary = c
		primaryChanged = true
	}
	if c := SafeColor(o.PrimaryHover, ""); c != "" {
		t.PrimaryHover = c
	} else if primaryChanged {
		t.PrimaryHover = t.Primary
	}
	t.Text = SafeColor(o.Text, t.Text)
	t.MutedText = SafeColor(o.MutedText, t.MutedText)
	t.CardBG = SafeColor(o.CardBG, t.CardBG)
	t.Border = SafeColor(o.Border, t.Border)
	t.Background = SafeColor(o.Background, t.Background)
	t.Success = SafeColor(o.Success, t.Success)
	t.Warning = SafeColor(o.Warning, t.Warning)
	t.Error = SafeColor(o.Error, t.Error)
	if c := SafeColor(o.Info, ""); c != "" {
		t.Info = c
	} else {
		t.Info = t.Primary
	}
	t.ButtonText = ContrastText(t.Primary)
	return t
}

func ContrastText(bg string) string {
	r, g, b, ok := parseHex(bg)
	if !ok {
		return "#ffffff"
	}
	lum := 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
	if (lum+0.05)/0.05 >= 1.05/(lum+0.05) {
		return "#111827"
	}
	return "#ffffff"
}

func linear(c int) float64 {
	v := float64(c) / 255
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func parseHex(s string) (int, int, int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 && len(s) != 8 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s[:6], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff), true
}
