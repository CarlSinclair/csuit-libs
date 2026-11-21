package postal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (r *Resolver) getJSON(ctx context.Context, url string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", r.UserAgent)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode: %w", err)
	}
	return resp.StatusCode, nil
}

func (r *Resolver) lookupBR(ctx context.Context, cep string) ([]Place, error) {
	var via struct {
		Erro       any    `json:"erro"`
		Logradouro string `json:"logradouro"`
		Bairro     string `json:"bairro"`
		Localidade string `json:"localidade"`
		UF         string `json:"uf"`
		Estado     string `json:"estado"`
	}
	status, viaErr := r.getJSON(ctx, "https://viacep.com.br/ws/"+cep+"/json/", &via)
	viaOK := viaErr == nil && status >= 200 && status < 300
	viaNotFound := viaErr == nil && status == http.StatusBadRequest
	if viaOK {
		erro := fmt.Sprint(via.Erro)
		if via.Erro != nil && (erro == "true" || erro == "1") {
			viaNotFound = true
		} else if via.Localidade != "" {
			return []Place{{Country: "BR", Postal: cep, Street: via.Logradouro, District: via.Bairro, City: via.Localidade, Region: via.Estado, RegionCode: via.UF, Source: "viacep"}}, nil
		}
	}

	var brasil struct {
		State        string `json:"state"`
		City         string `json:"city"`
		Neighborhood string `json:"neighborhood"`
		Street       string `json:"street"`
	}
	status2, baErr := r.getJSON(ctx, "https://brasilapi.com.br/api/cep/v1/"+cep, &brasil)
	if baErr == nil && status2 >= 200 && status2 < 300 && brasil.City != "" {
		return []Place{{Country: "BR", Postal: cep, Street: brasil.Street, District: brasil.Neighborhood, City: brasil.City, RegionCode: strings.ToUpper(brasil.State), Source: "brasilapi"}}, nil
	}
	baNotFound := baErr == nil && status2 == http.StatusNotFound
	if viaNotFound || baNotFound {
		return nil, ErrNotFound
	}
	return nil, fmt.Errorf("%w: viacep status=%d err=%v; brasilapi status=%d err=%v", ErrUpstream, status, viaErr, status2, baErr)
}
