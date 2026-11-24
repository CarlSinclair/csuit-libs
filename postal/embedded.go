package postal

import (
	"embed"
	"io/fs"
)

//go:embed data/*.txt.gz
var dataFiles embed.FS

// Countries lists every country GeoNames publishes a postal file for (Brazil is served live and never read from these files); each file ships inside
// the package. The monthly refresh workflow regenerates exactly these files.
var Countries = []string{"AD", "AE", "AI", "AL", "AM", "AR", "AS", "AT", "AU", "AX", "AZ", "BD", "BE", "BG", "BM", "BY", "CA", "CC", "CH", "CL", "CN", "CO", "CR", "CX", "CY", "CZ", "DE", "DK", "DO", "DZ", "EC", "EE", "ES", "FI", "FK", "FM", "FO", "FR", "GB", "GF", "GG", "GI", "GL", "GP", "GS", "GT", "GU", "HK", "HM", "HN", "HR", "HT", "HU", "ID", "IE", "IM", "IN", "IO", "IS", "IT", "JE", "JP", "KE", "KR", "LI", "LK", "LT", "LU", "LV", "MA", "MC", "MD", "MH", "MK", "MN", "MO", "MP", "MQ", "MT", "MW", "MX", "MY", "NC", "NF", "NL", "NO", "NR", "NU", "NZ", "PA", "PE", "PF", "PH", "PK", "PL", "PM", "PN", "PR", "PT", "PW", "PY", "RE", "RO", "RS", "RU", "SE", "SG", "SI", "SJ", "SK", "SM", "TC", "TH", "TR", "UA", "US", "UY", "VA", "VI", "WF", "WS", "YT", "ZA"}

// Embedded returns the embedded GeoNames files, one <CC>.txt.gz per country.
func Embedded() fs.FS {
	sub, err := fs.Sub(dataFiles, "data")
	if err != nil {
		return nil
	}
	return sub
}

// NewEmbedded builds a Resolver backed by the embedded GeoNames files.
func NewEmbedded(userAgent string) *Resolver {
	return New(Embedded(), userAgent)
}
