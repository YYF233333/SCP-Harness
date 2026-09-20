package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Strict rejects duplicate keys before Go's decoder can silently overwrite them.
// Every declared JSON field is required, including empty arrays and objects.
func Strict(data []byte, dst any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := value(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	// JSON Schema integer includes 1.0 and 1e0. Normalize exactly integral
	// numeric spellings without a floating-point round trip or large powers.
	d = json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var tree any
	if err := d.Decode(&tree); err != nil {
		return err
	}
	tree = normalize(tree)
	canonical, err := json.Marshal(tree)
	if err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	return required(canonical, reflect.TypeOf(dst).Elem())
}
func value(d *json.Decoder, depth int) error {
	if depth > 128 {
		return fmt.Errorf("JSON nesting exceeds 128")
	}
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || keys[s] {
				return fmt.Errorf("duplicate/invalid JSON key %v", k)
			}
			keys[s] = true
			if e = value(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = value(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("invalid delimiter")
	}
	_, e = d.Token()
	return e
}

var rawType = reflect.TypeOf(json.RawMessage{})

func required(data []byte, t reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("required value is null")
	}
	if t == rawType {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		allowed := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			k := f.Tag.Get("json")
			if k == "-" || !f.IsExported() {
				continue
			}
			if k == "" {
				k = f.Name
			}
			k = strings.SplitN(k, ",", 2)[0]
			allowed[k] = true
			v, ok := obj[k]
			if !ok {
				return fmt.Errorf("missing field %s", k)
			}
			if err := required(v, f.Type); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
		for k := range obj {
			if !allowed[k] {
				return fmt.Errorf("unknown field %s", k)
			}
		}
	case reflect.Slice, reflect.Array:
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		for _, v := range arr {
			if err := required(v, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		if t.Elem() == rawType {
			return nil
		}
		for _, v := range obj {
			if err := required(v, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Integer recognizes the mathematical integer value of a JSON number. Values
// outside int64 remain in their original spelling; bounded runtime fields then
// fail normal decoding, while opaque role-card numbers can be retained.
func integer(s string) (string, bool) {
	negative := strings.HasPrefix(s, "-")
	unsigned := strings.TrimPrefix(s, "-")
	parts := strings.FieldsFunc(unsigned, func(r rune) bool { return r == 'e' || r == 'E' })
	mantissa := parts[0]
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return "0", true
	}
	exponent := 0
	if len(parts) > 1 {
		v, e := strconv.Atoi(parts[1])
		if e != nil {
			if strings.HasPrefix(parts[1], "-") {
				return "", false
			}
			return s, true
		}
		exponent = v
	}
	if exponent > 10000 {
		return s, true
	}
	if exponent < -10000 {
		return "", false
	}
	shift := exponent - fraction
	if shift < 0 {
		n := -shift
		if n >= len(digits) || strings.Trim(digits[len(digits)-n:], "0") != "" {
			return "", false
		}
		digits = digits[:len(digits)-n]
	} else if len(digits)+shift > 19 {
		return s, true
	} else {
		digits += strings.Repeat("0", shift)
	}
	if negative {
		digits = "-" + digits
	}
	return digits, true
}
func normalize(v any) any {
	switch v := v.(type) {
	case json.Number:
		if s, ok := integer(string(v)); ok {
			if _, e := strconv.ParseInt(s, 10, 64); e == nil {
				return json.Number(s)
			}
		}
		return v
	case []any:
		for i := range v {
			v[i] = normalize(v[i])
		}
		return v
	case map[string]any:
		for k, x := range v {
			v[k] = normalize(x)
		}
		return v
	}
	return v
}
