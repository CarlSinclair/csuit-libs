package apikit

import (
	"net/http"
	"testing"
)

func TestAdminRoutesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range AdminRoutes {
		k := r.Method + " " + r.Path
		if seen[k] {
			t.Errorf("duplicate route %s", k)
		}
		seen[k] = true
		if r.Pick == nil || r.Pick(&Admin{}) == nil {
			t.Errorf("route %s has no handler", k)
		}
	}
	for _, want := range []string{http.MethodGet + " /overview", http.MethodPost + " /keys", http.MethodPost + " /keys/revoke", http.MethodGet + " /deliveries"} {
		if !seen[want] {
			t.Errorf("missing route %s", want)
		}
	}
}
