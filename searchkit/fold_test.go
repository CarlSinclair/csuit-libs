package searchkit

import (
	"slices"
	"testing"
)

func TestFold(t *testing.T) {
	cases := []struct{ in, want string }{
		{"João", "joao"},
		{"João", "joao"},
		{"CONCEIÇÃO", "conceicao"},
		{"Amélie", "amelie"},
		{"El Niño", "el nino"},
		{"Straße", "strasse"},
		{"Œuvre", "oeuvre"},
		{"Łódź", "lodz"},
		{"Ёлка", "елка"},
		{"Йод", "йод"},
		{"Київ їжак", "київ їжак"},
		{"уда́рение", "ударение"},
		{"Ελλάδα", "ελλαδα"},
		{"ΟΔΟΣ", "οδοσ"},
		{"οδός", "οδοσ"},
		{"مُحَمَّد", "محمد"},
		{"أحمد", "احمد"},
		{"مدرسة", "مدرسه"},
		{"ＡＢＣ１２３", "abc123"},
		{"がっこう", "がっこう"},
		{"a​b", "a b"},
	}
	for _, c := range cases {
		if got := Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"João da Silva", []string{"joao", "da", "silva"}},
		{"a b cd", []string{"cd"}},
		{"Black-Friday 2026", []string{"black", "friday", "2026"}},
		{"猫", []string{"猫"}},
		{"猫咪", []string{"猫咪"}},
		{"東京タワー", []string{"東京", "京タ", "タワ", "ワー"}},
		{"iPhone15は最高", []string{"iphone15", "は最", "最高"}},
		{"Чёрный кот", []string{"черный", "кот"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := Words(c.in); !slices.Equal(got, c.want) {
			t.Errorf("Words(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIndexWordsUnsegmented(t *testing.T) {
	got := IndexWords("猫咪")
	for _, w := range []string{"猫", "咪", "猫咪"} {
		if !slices.Contains(got, w) {
			t.Errorf("IndexWords(猫咪) = %q, missing %q", got, w)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		query  string
		fields []string
		want   bool
	}{
		{"amelie", []string{"AMÉLIE Poulain"}, true},
		{"joao", []string{"João"}, true},
		{"sil", []string{"João da Silva"}, true},
		{"ilva", []string{"João da Silva"}, false},
		{"joao silva", []string{"João", "Silva"}, true},
		{"joao costa", []string{"João", "Silva"}, false},
		{"кот", []string{"Чёрный кот"}, true},
		{"москва", []string{"Москва"}, true},
		{"猫", []string{"黒猫"}, true},
		{"東京", []string{"東京タワー"}, true},
		{"احمد", []string{"أحمد علي"}, true},
		{"", []string{"anything"}, false},
		{"x", []string{"x"}, false},
	}
	for _, c := range cases {
		if got := Match(c.query, c.fields...); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.query, c.fields, got, c.want)
		}
	}
}
