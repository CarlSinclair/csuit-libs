package searchkit

import (
	"slices"
	"testing"
)

func TestRemoveStopwords(t *testing.T) {
	cases := []struct {
		lang string
		in   []string
		want []string
	}{
		{"en", Words("how do I change my order"), []string{"change", "order"}},
		{"pt-BR", Words("como eu troco o meu pedido"), []string{"troco", "pedido"}},
		{"es", Words("como cambio mi pedido"), []string{"cambio", "pedido"}},
		{"fr", Words("comment changer ma commande"), []string{"changer", "commande"}},
		{"ru", Words("как изменить заказ"), []string{"изменить", "заказ"}},
		{"en", Words("how do you"), []string{"how", "do", "you"}},
		{"ja", Words("注文を変更"), Words("注文を変更")},
	}
	for _, c := range cases {
		if got := RemoveStopwords(c.in, c.lang); !slices.Equal(got, c.want) {
			t.Errorf("RemoveStopwords(%q, %q) = %q, want %q", c.in, c.lang, got, c.want)
		}
	}
}
