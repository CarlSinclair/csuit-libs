package secheaders

import "encoding/json"

func jsonUnmarshal(s string, v interface{}) error { return json.Unmarshal([]byte(s), v) }

func jsonMarshalSorted(m map[string][]string) string {
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}
