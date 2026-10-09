package instrumenter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// jsonObject is an insertion-ordered JSON object. Rewriting package.json through
// it keeps the original key order, so Extent only touches the entries it owns.
type jsonObject struct {
	keys   []string
	values map[string]any
}

func newJSONObject() *jsonObject {
	return &jsonObject{values: map[string]any{}}
}

func (o *jsonObject) get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	value, ok := o.values[key]
	return value, ok
}

// object returns the nested object stored under key, or nil when the key is
// missing or does not hold an object.
func (o *jsonObject) object(key string) *jsonObject {
	value, _ := o.get(key)
	nested, _ := value.(*jsonObject)
	return nested
}

func (o *jsonObject) set(key string, value any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// parseJSONObject decodes a JSON document whose top level must be an object.
// Numbers are kept as their literal text so they are written back unchanged.
func parseJSONObject(data []byte) (*jsonObject, error) {
	if !json.Valid(data) {
		return nil, errors.New("package.json is not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := decodeOrderedJSON(dec)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*jsonObject)
	if !ok {
		return nil, errors.New("package.json must contain a JSON object")
	}
	return obj, nil
}

func decodeOrderedJSON(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		obj := newJSONObject()
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid JSON object key")
			}
			value, err := decodeOrderedJSON(dec)
			if err != nil {
				return nil, err
			}
			obj.set(key, value)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		items := []any{}
		for dec.More() {
			value, err := decodeOrderedJSON(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return items, nil
	}
	return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
}

// jsonFormat describes the layout of an existing JSON document.
type jsonFormat struct {
	indent  string
	newline string
}

// detectJSONFormat reuses the indentation and line endings of an existing
// document. Minified input (no indented lines) is pretty-printed with two spaces.
func detectJSONFormat(data []byte) jsonFormat {
	format := jsonFormat{indent: "  ", newline: "\n"}
	if bytes.Contains(data, []byte("\r\n")) {
		format.newline = "\r\n"
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		leading := line[:len(line)-len(trimmed)]
		if leading == "" || strings.TrimSpace(trimmed) == "" {
			continue
		}
		// The first indented line of a document is a top-level member, so its
		// leading whitespace is exactly one indentation unit.
		format.indent = leading
		break
	}
	return format
}

// marshalPackageJSON serializes pkg using the layout of the original document.
// Callers only use it when they changed pkg, so untouched files are not rewritten.
func marshalPackageJSON(pkg *jsonObject, original []byte) ([]byte, error) {
	format := detectJSONFormat(original)
	var b bytes.Buffer
	if err := writeOrderedJSON(&b, pkg, format, 0); err != nil {
		return nil, err
	}
	b.WriteString(format.newline)
	return b.Bytes(), nil
}

func writeOrderedJSON(b *bytes.Buffer, value any, format jsonFormat, depth int) error {
	switch v := value.(type) {
	case *jsonObject:
		if len(v.keys) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{" + format.newline)
		for i, key := range v.keys {
			b.WriteString(strings.Repeat(format.indent, depth+1))
			b.WriteString(encodeJSONString(key) + ": ")
			if err := writeOrderedJSON(b, v.values[key], format, depth+1); err != nil {
				return err
			}
			if i < len(v.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteString(format.newline)
		}
		b.WriteString(strings.Repeat(format.indent, depth) + "}")
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[" + format.newline)
		for i, item := range v {
			b.WriteString(strings.Repeat(format.indent, depth+1))
			if err := writeOrderedJSON(b, item, format, depth+1); err != nil {
				return err
			}
			if i < len(v)-1 {
				b.WriteByte(',')
			}
			b.WriteString(format.newline)
		}
		b.WriteString(strings.Repeat(format.indent, depth) + "]")
	case string:
		b.WriteString(encodeJSONString(v))
	case json.Number:
		b.WriteString(v.String())
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case nil:
		b.WriteString("null")
	default:
		return fmt.Errorf("unsupported JSON value %T", value)
	}
	return nil
}

func encodeJSONString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
