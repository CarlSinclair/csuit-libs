package currency

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const erAPILatestURL = "https://open.er-api.com/v6/latest/"

var erAPIClient = &http.Client{Timeout: 10 * time.Second}

func FetchERAPI(base string) (map[string]float64, error) {
	resp, err := erAPIClient.Get(erAPILatestURL + url.PathEscape(base))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open.er-api.com returned %d", resp.StatusCode)
	}
	var payload struct {
		Result string             `json:"result"`
		Base   string             `json:"base_code"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("open.er-api.com decode: %w", err)
	}
	if payload.Result != "success" || len(payload.Rates) == 0 {
		return nil, fmt.Errorf("open.er-api.com result=%q rates=%d", payload.Result, len(payload.Rates))
	}
	if !strings.EqualFold(payload.Base, base) {
		return nil, fmt.Errorf("open.er-api.com returned base %q for %q", payload.Base, base)
	}
	return payload.Rates, nil
}
