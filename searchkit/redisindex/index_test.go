package redisindex

import "testing"

func testIndex() *Index {
	return &Index{
		Name:   "t",
		Prefix: "t:",
		Fields: []Field{
			{Name: "q", Kind: Text, Weight: 2},
			{Name: "a", Kind: Text},
			{Name: "lang", Kind: Tag},
			{Name: "pos", Kind: Numeric, Sortable: true},
		},
	}
}

func TestBuildAnd(t *testing.T) {
	q, opts, ok := testIndex().build(Query{Text: "Troca de Pedido", Tags: map[string][]string{"lang": {"pt-BR"}}})
	if !ok {
		t.Fatal("expected a query")
	}
	want := `@q|a:(troca* de* pedido*) @lang:{pt\-BR}`
	if q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
	if opts.Limit != 10 || !opts.NoContent || !opts.WithScores {
		t.Errorf("unexpected options %+v", opts)
	}
}

func TestBuildAnyShortWords(t *testing.T) {
	q, _, ok := testIndex().build(Query{Text: "how do I change my order", Any: true})
	if !ok {
		t.Fatal("expected a query")
	}
	want := `@q|a:(how* | do | change* | my | order*)`
	if q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestBuildFilterOnly(t *testing.T) {
	q, opts, ok := testIndex().build(Query{
		Tags:   map[string][]string{"lang": {"pt", "en"}},
		Ranges: map[string][2]float64{"pos": {1, 5}},
		SortBy: "pos",
		Limit:  3,
	})
	if !ok {
		t.Fatal("expected a query")
	}
	want := `@lang:{pt | en} @pos:[1 5]`
	if q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
	if len(opts.SortBy) != 1 || !opts.SortBy[0].Asc || opts.Limit != 3 {
		t.Errorf("unexpected options %+v", opts)
	}
}

func TestBuildUnusableText(t *testing.T) {
	if _, _, ok := testIndex().build(Query{Text: "a"}); ok {
		t.Error("a query with no usable words must not search")
	}
}

func TestBuildUnknownFieldsIgnored(t *testing.T) {
	q, _, ok := testIndex().build(Query{Text: "pedido", Fields: []string{"nope", "q"}, Tags: map[string][]string{"ghost": {"x"}}})
	if !ok {
		t.Fatal("expected a query")
	}
	if want := `@q:(pedido*)`; q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestBuildCJKSingleCharacterIsExact(t *testing.T) {
	q, _, ok := testIndex().build(Query{Text: "猫"})
	if !ok {
		t.Fatal("expected a query")
	}
	if want := `@q|a:(猫)`; q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestEscapeTag(t *testing.T) {
	if got := escapeTag("pt-BR.x y"); got != `pt\-BR\.x\ y` {
		t.Errorf("escapeTag = %q", got)
	}
}
