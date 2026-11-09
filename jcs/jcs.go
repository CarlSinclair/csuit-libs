package jcs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// JSON Canonicalization Scheme (RFC 8785) - the deterministic, byte-for-byte
// JSON serialization AP2 / UCP require before a payload is signed or its
// signature checked. Implemented here (rather than pulling a dependency)
// because the payloads that ever go through it are ones this service builds
// itself: UCP checkout objects made of ASCII field names, integer minor-unit
// amounts, short string enums, arrays and nested objects. Within that shape
// this is exact; the two edges it does not fully cover (keys outside the
// Basic Multilingual Plane needing UTF-16 code-unit ordering, and non-integer
// numbers needing the full ECMAScript Number->string algorithm) are things
// the signed objects never contain, and Marshal returns an error rather
// than emit a non-conformant byte if one ever appears.

// Marshal returns the RFC 8785 canonical form of v. v is first marshalled
// and re-parsed with json.Number so the caller can pass a struct, a map or an
// already-decoded value interchangeably.
func Marshal(v interface{}) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("jcs: pre-marshal: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var parsed interface{}
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("jcs: re-parse: %w", err)
	}
	var b bytes.Buffer
	if err := jcsWrite(&b, parsed); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func jcsWrite(b *bytes.Buffer, v interface{}) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		jcsWriteString(b, t)
	case json.Number:
		return jcsWriteNumber(b, string(t))
	case float64:
		return jcsWriteNumber(b, strconv.FormatFloat(t, 'g', -1, 64))
	case []interface{}:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := jcsWrite(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			for _, r := range k {
				if r > 0xFFFF {
					return fmt.Errorf("jcs: object key %q outside the BMP is not supported", k)
				}
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			jcsWriteString(b, k)
			b.WriteByte(':')
			if err := jcsWrite(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("jcs: unsupported value type %T", v)
	}
	return nil
}

// jcsWriteString applies the RFC 8785 string production: escape only ", \ and
// U+0000..U+001F (with the short forms where defined), and never the
// HTML-sensitive characters encoding/json escapes by default.
func jcsWriteString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// jcsWriteNumber emits the canonical form for a JSON number. Integers (the
// only kind the signed payloads use - minor-unit amounts, quantities) are
// emitted verbatim once normalised. A fractional or exponent form is only
// accepted when it round-trips exactly through a plain decimal within the
// ECMAScript-safe integer range; anything else is rejected rather than
// risk a non-conformant serialization.
func jcsWriteNumber(b *bytes.Buffer, num string) error {
	num = strings.TrimSpace(num)
	if num == "" {
		return fmt.Errorf("jcs: empty number")
	}
	if i, err := strconv.ParseInt(num, 10, 64); err == nil {
		b.WriteString(strconv.FormatInt(i, 10))
		return nil
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return fmt.Errorf("jcs: bad number %q: %w", num, err)
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Errorf("jcs: non-finite number %q", num)
	}
	if f == math.Trunc(f) && math.Abs(f) < 9.007199254740992e15 {
		b.WriteString(strconv.FormatInt(int64(f), 10))
		return nil
	}
	return fmt.Errorf("jcs: non-integer number %q is outside this canonicaliser's supported range", num)
}
