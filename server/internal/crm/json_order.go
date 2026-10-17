package crm

// JSON.parse gives each object its keys in an order of its own — array-index
// keys first, ascending, then the rest as written; a repeated key keeps its
// first place and its last value — and JSON.stringify writes them back in
// that order. A Go map has none. Where TS wrote a parsed request value back
// out (a view's filters, a custom field's options, an imported json cell) or
// walked an object's keys (the custom columns of an import), the order comes
// from the JSON text here.

import (
	"encoding/json"
	"math"
	"strings"

	"opencrm/internal/js"
)

// keyOrder is the key order of every object in a JSON value, as JSON.parse
// left it. A nil *keyOrder knows no order (sorted keys, in JS order, stand in).
type keyOrder struct {
	keys   []string             // this object's keys, in JS order
	fields map[string]*keyOrder // each key's (last) value
	items  []*keyOrder          // each element of an array
}

// readKeyOrder reads the key order of a JSON text: nil when it isn't JSON.
func readKeyOrder(text string) *keyOrder {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber() // a number past float64's range (1e400) is still JSON
	o, err := decodeKeyOrder(dec)
	if err != nil {
		return nil
	}
	return o
}

func decodeKeyOrder(dec *json.Decoder) (*keyOrder, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := &keyOrder{fields: map[string]*keyOrder{}}
		var seen []string
		for dec.More() {
			name, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := name.(string)
			v, err := decodeKeyOrder(dec)
			if err != nil {
				return nil, err
			}
			if _, again := o.fields[k]; !again {
				seen = append(seen, k)
			}
			o.fields[k] = v
		}
		if _, err := dec.Token(); err != nil { // the closing brace
			return nil, err
		}
		o.keys = jsKeyOrder(seen)
		return o, nil
	case json.Delim('['):
		o := &keyOrder{}
		for dec.More() {
			v, err := decodeKeyOrder(dec)
			if err != nil {
				return nil, err
			}
			o.items = append(o.items, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return o, nil
	}
	return nil, nil // a scalar has no keys
}

// field is the order inside the object's member k.
func (o *keyOrder) field(k string) *keyOrder {
	if o == nil {
		return nil
	}
	return o.fields[k]
}

// item is the order inside the array's element i.
func (o *keyOrder) item(i int) *keyOrder {
	if o == nil || i >= len(o.items) {
		return nil
	}
	return o.items[i]
}

// orderOf is m's keys as Object.keys listed them: in o's order, then any key
// o doesn't know, the whole in JS order.
func (o *keyOrder) orderOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	listed := map[string]bool{}
	if o != nil {
		for _, k := range o.keys {
			if _, ok := m[k]; ok {
				out = append(out, k)
				listed[k] = true
			}
		}
	}
	if len(listed) == len(m) {
		return out
	}
	for _, k := range sortedKeys(m) {
		if !listed[k] {
			out = append(out, k)
		}
	}
	return jsKeyOrder(out)
}

// jsStringify is JSON.stringify of a parsed JSON value (maps, []any, float64,
// string, bool, nil), each object's keys in o's order: numbers as JS writes
// them (NaN and ±Infinity as null), strings escaped as JSON.stringify does.
func jsStringify(v any, o *keyOrder) string {
	var b strings.Builder
	writeJSValue(&b, v, o)
	return b.String()
}

func writeJSValue(b *strings.Builder, v any, o *keyOrder) {
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(js.String(v))
		}
	case string:
		writeJSString(b, v)
	case []any:
		b.WriteByte('[')
		for i, x := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSValue(b, x, o.item(i))
		}
		b.WriteByte(']')
	case map[string]any:
		b.WriteByte('{')
		for i, k := range o.orderOf(v) {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSString(b, k)
			b.WriteByte(':')
			writeJSValue(b, v[k], o.field(k))
		}
		b.WriteByte('}')
	default:
		s, _ := stringify(v) // not a parsed JSON value; none reach here
		b.WriteString(s)
	}
}

// writeJSString is JSON.stringify's QuoteJSONString: the quote, the backslash
// and control characters escaped (\b \f \n \r \t, else \u00xx), nothing else.
func writeJSString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}
