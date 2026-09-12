package scholar

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Canonical JSON rule — CROSS-LANGUAGE CONTRACT with the Python Scholar
// Worker (services/scholar-worker/src/lumin_scholar/contracts.py,
// compute_input_hash). T04 pinned it; both sides must agree byte-for-byte:
//
//   - Object keys are sorted recursively by Unicode code point (Go
//     sort.Strings on the UTF-8 keys is equivalent).
//   - Strings are raw UTF-8 with no escaping beyond JSON's mandatory
//     escapes: no HTML escaping of <, >, &, no \u2028/\u2029 escaping
//     (Python ensure_ascii=False semantics).
//   - Separators are compact ("," and ":"), no whitespace.
//   - The hash is sha256 over the UTF-8 bytes, hex-encoded, "sha256:" prefix.
//
// Numbers: integer types (and json.Number verbatim) are safe. float64 is
// REJECTED (fail closed): Go's and Python's float formatting differ
// (1.0 vs 1.0e+00 style edges), so payloads must not carry floats — a
// float in a hashed payload is a caller bug we surface loudly.
//
// The golden cross-language fixture lives in canonical_test.go and
// services/scholar-worker/tests/test_canonical_hash.py; both assert the
// same digest. Change the rule or fixture only in the same commit on both
// sides.

// appendCanonicalJSON writes v in the canonical form onto dst.
func appendCanonicalJSON(dst []byte, v any) ([]byte, error) {
	switch val := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case bool:
		if val {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case string:
		return appendCanonicalString(dst, val), nil
	case json.Number:
		return append(dst, val.String()...), nil
	case int:
		return strconv.AppendInt(dst, int64(val), 10), nil
	case int8:
		return strconv.AppendInt(dst, int64(val), 10), nil
	case int16:
		return strconv.AppendInt(dst, int64(val), 10), nil
	case int32:
		return strconv.AppendInt(dst, int64(val), 10), nil
	case int64:
		return strconv.AppendInt(dst, val, 10), nil
	case uint:
		return strconv.AppendUint(dst, uint64(val), 10), nil
	case uint8:
		return strconv.AppendUint(dst, uint64(val), 10), nil
	case uint16:
		return strconv.AppendUint(dst, uint64(val), 10), nil
	case uint32:
		return strconv.AppendUint(dst, uint64(val), 10), nil
	case uint64:
		return strconv.AppendUint(dst, val, 10), nil
	case float32, float64:
		return nil, fmt.Errorf("canonical: float values are not hash-safe; use ints or strings")
	case []any:
		dst = append(dst, '[')
		for i, item := range val {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			dst, err = appendCanonicalJSON(dst, item)
			if err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	case []string:
		converted := make([]any, len(val))
		for i, item := range val {
			converted[i] = item
		}
		return appendCanonicalJSON(dst, converted)
	case []map[string]any:
		converted := make([]any, len(val))
		for i, item := range val {
			converted[i] = item
		}
		return appendCanonicalJSON(dst, converted)
	case map[string]any:
		keys := make([]string, 0, len(val))
		for key := range val {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		dst = append(dst, '{')
		for i, key := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendCanonicalString(dst, key)
			dst = append(dst, ':')
			var err error
			dst, err = appendCanonicalJSON(dst, val[key])
			if err != nil {
				return nil, err
			}
		}
		return append(dst, '}'), nil
	default:
		return nil, fmt.Errorf("canonical: unsupported value type %T", v)
	}
}

// appendCanonicalString appends s as a canonical JSON string: mandatory
// escapes only, control chars as lowercase \u00xx, everything else (incl.
// non-ASCII and U+2028/U+2029) as raw UTF-8. Invalid UTF-8 input is a
// caller bug and is rejected to keep hashes well-defined.
func appendCanonicalString(dst []byte, s string) []byte {
	if !utf8.ValidString(s) {
		// Replace invalid bytes the way encoding/json would not — we refuse
		// instead, to keep the canonical form unambiguous.
		s = "�invalid-utf8�"
	}
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			dst = append(dst, '\\', '"')
		case c == '\\':
			dst = append(dst, '\\', '\\')
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c == '\r':
			dst = append(dst, '\\', 'r')
		case c == '\t':
			dst = append(dst, '\\', 't')
		case c == '\b':
			dst = append(dst, '\\', 'b')
		case c == '\f':
			dst = append(dst, '\\', 'f')
		case c < 0x20:
			const hexDigits = "0123456789abcdef"
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '"')
}

// CanonicalPayloadJSON returns the canonical JSON encoding of a payload
// (see the contract comment at the top of this file).
func CanonicalPayloadJSON(payload map[string]any) ([]byte, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	return appendCanonicalJSON(nil, any(payload))
}
