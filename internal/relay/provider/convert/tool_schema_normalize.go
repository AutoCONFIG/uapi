package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

type orderedJSONPair struct {
	key   string
	value orderedJSONValue
}

type orderedJSONValue struct {
	object []orderedJSONPair
	array  []orderedJSONValue
	scalar json.RawMessage
	kind   orderedJSONKind
}

type orderedJSONKind int

const (
	orderedJSONScalar orderedJSONKind = iota
	orderedJSONObject
	orderedJSONArray
)

func normalizeToolSchemaRaw(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := parseOrderedJSONValue(dec)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	if tok, err := dec.Token(); err != io.EOF || tok != nil {
		return append(json.RawMessage(nil), raw...)
	}
	normalized := normalizeToolSchemaValue(value, false)
	out, err := marshalOrderedJSONValue(normalized)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return out
}

func parseOrderedJSONValue(dec *json.Decoder) (orderedJSONValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return orderedJSONValue{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			var pairs []orderedJSONPair
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return orderedJSONValue{}, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return orderedJSONValue{}, fmt.Errorf("object key is %T", keyTok)
				}
				value, err := parseOrderedJSONValue(dec)
				if err != nil {
					return orderedJSONValue{}, err
				}
				pairs = append(pairs, orderedJSONPair{key: key, value: value})
			}
			if end, err := dec.Token(); err != nil || end != json.Delim('}') {
				return orderedJSONValue{}, fmt.Errorf("unterminated object")
			}
			return orderedJSONValue{kind: orderedJSONObject, object: pairs}, nil
		case '[':
			var values []orderedJSONValue
			for dec.More() {
				value, err := parseOrderedJSONValue(dec)
				if err != nil {
					return orderedJSONValue{}, err
				}
				values = append(values, value)
			}
			if end, err := dec.Token(); err != nil || end != json.Delim(']') {
				return orderedJSONValue{}, fmt.Errorf("unterminated array")
			}
			return orderedJSONValue{kind: orderedJSONArray, array: values}, nil
		default:
			return orderedJSONValue{}, fmt.Errorf("unexpected delimiter %q", t)
		}
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return orderedJSONValue{}, err
		}
		return orderedJSONValue{kind: orderedJSONScalar, scalar: raw}, nil
	}
}

func normalizeToolSchemaValue(value orderedJSONValue, preserveObjectKeyOrder bool) orderedJSONValue {
	switch value.kind {
	case orderedJSONObject:
		pairs := make([]orderedJSONPair, len(value.object))
		for i, pair := range value.object {
			pairs[i] = orderedJSONPair{
				key:   pair.key,
				value: normalizeToolSchemaValue(pair.value, pair.key == "properties"),
			}
		}
		if !preserveObjectKeyOrder {
			sort.SliceStable(pairs, func(i, j int) bool {
				ri, rj := toolSchemaKeyRank(pairs[i].key), toolSchemaKeyRank(pairs[j].key)
				if ri != rj {
					return ri < rj
				}
				return pairs[i].key < pairs[j].key
			})
		}
		return orderedJSONValue{kind: orderedJSONObject, object: pairs}
	case orderedJSONArray:
		values := make([]orderedJSONValue, len(value.array))
		for i := range value.array {
			values[i] = normalizeToolSchemaValue(value.array[i], false)
		}
		return orderedJSONValue{kind: orderedJSONArray, array: values}
	default:
		return value
	}
}

func toolSchemaKeyRank(key string) int {
	switch key {
	case "type":
		return 0
	case "description":
		return 1
	case "properties":
		return 2
	case "required":
		return 3
	default:
		return 100
	}
}

func marshalOrderedJSONValue(value orderedJSONValue) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeOrderedJSONValue(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeOrderedJSONValue(buf *bytes.Buffer, value orderedJSONValue) error {
	switch value.kind {
	case orderedJSONObject:
		buf.WriteByte('{')
		for i, pair := range value.object {
			if i > 0 {
				buf.WriteByte(',')
			}
			key, err := json.Marshal(pair.key)
			if err != nil {
				return err
			}
			buf.Write(key)
			buf.WriteByte(':')
			if err := writeOrderedJSONValue(buf, pair.value); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case orderedJSONArray:
		buf.WriteByte('[')
		for i, item := range value.array {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeOrderedJSONValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		if len(value.scalar) == 0 {
			buf.WriteString("null")
		} else {
			buf.Write(value.scalar)
		}
	}
	return nil
}
