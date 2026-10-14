package web

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
)

// JSON.stringify writes NaN and ±Infinity as null; encoding/json refuses them
// and has no hook to do otherwise. So when a value holds one, it is encoded
// from a copy in which each is a sentinel number, and that number is then
// rewritten as null in the output. The sentinel is random and checked against
// every number in the value, so a real number is never mistaken for it.

var (
	marshalerType     = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// isNonFinite reports whether err is encoding/json refusing a NaN or ±Inf.
func isNonFinite(err error) bool {
	var unsupported *json.UnsupportedValueError
	if !errors.As(err, &unsupported) {
		return false
	}
	k := unsupported.Value.Kind()
	return k == reflect.Float64 || k == reflect.Float32
}

func marshalNonFinite(v any) ([]byte, error) {
	for attempt := 0; attempt < 8; attempt++ {
		s := newSentinels()
		cp := s.replace(reflect.ValueOf(v), 0)
		if s.err != nil {
			return nil, s.err
		}
		if s.clash {
			continue
		}
		b, err := marshal(cp.Interface())
		if err != nil {
			return nil, err
		}
		return s.null(b), nil
	}
	return nil, errors.New("web: no sentinel for NaN/Infinity that the value doesn't already contain")
}

type sentinels struct {
	f64, f32 float64 // f32 holds a float32 value
	tokens   [][]byte
	clash    bool
	err      error
}

func newSentinels() *sentinels {
	s := &sentinels{
		f64: -(1 + rand.Float64()) * 1e-300,
		f32: float64(-(1 + rand.Float32()) * 1e-30),
	}
	t64, _ := json.Marshal(s.f64)
	t32, _ := json.Marshal(float32(s.f32))
	s.tokens = [][]byte{t64, t32}
	return s
}

// replace returns a copy of v, of the same type, with every NaN and ±Inf
// swapped for a sentinel. Values that encode themselves (MarshalJSON,
// MarshalText) are left alone, as are unexported fields, which JSON skips.
func (s *sentinels) replace(v reflect.Value, depth int) reflect.Value {
	if !v.IsValid() || s.err != nil {
		return v
	}
	if depth > 1000 {
		s.err = errors.New("json: value too deep or cyclic")
		return v
	}
	t := v.Type()
	if encodesItself(t) {
		return v
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			s.check(f, t.Bits())
			return v
		}
		out := reflect.New(t).Elem()
		if t.Kind() == reflect.Float32 {
			out.SetFloat(s.f32)
		} else {
			out.SetFloat(s.f64)
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(t).Elem()
		out.Set(s.replace(v.Elem(), depth+1))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(t.Elem())
		out.Elem().Set(s.replace(v.Elem(), depth+1))
		return out
	case reflect.Struct:
		out := reflect.New(t).Elem()
		out.Set(v)
		for i := 0; i < t.NumField(); i++ {
			if f := out.Field(i); f.CanSet() {
				f.Set(s.replace(v.Field(i), depth+1))
			}
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(t, v.Len())
		for it := v.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), s.replace(it.Value(), depth+1))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(t, v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(s.replace(v.Index(i), depth+1))
		}
		return out
	case reflect.Array:
		out := reflect.New(t).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(s.replace(v.Index(i), depth+1))
		}
		return out
	}
	return v
}

func encodesItself(t reflect.Type) bool {
	pt := reflect.PointerTo(t)
	return t.Implements(marshalerType) || t.Implements(textMarshalerType) ||
		pt.Implements(marshalerType) || pt.Implements(textMarshalerType)
}

// check flags a real number that would be written exactly like a sentinel.
func (s *sentinels) check(f float64, bits int) {
	var b []byte
	if bits == 32 {
		b, _ = json.Marshal(float32(f))
	} else {
		b, _ = json.Marshal(f)
	}
	for _, tok := range s.tokens {
		if bytes.Equal(b, tok) {
			s.clash = true
		}
	}
}

// null rewrites every sentinel number in compact JSON b as null, leaving the
// contents of strings alone.
func (s *sentinels) null(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		default:
			if n := s.tokenAt(b, i); n > 0 {
				out = append(out, "null"...)
				i += n - 1
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// tokenAt is the length of the sentinel number that starts at b[i] as a whole
// JSON value, or 0.
func (s *sentinels) tokenAt(b []byte, i int) int {
	if i > 0 && !bytes.ContainsRune([]byte(":,["), rune(b[i-1])) {
		return 0
	}
	for _, tok := range s.tokens {
		end := i + len(tok)
		if bytes.HasPrefix(b[i:], tok) && (end == len(b) || bytes.ContainsRune([]byte(",]}"), rune(b[end]))) {
			return len(tok)
		}
	}
	return 0
}
