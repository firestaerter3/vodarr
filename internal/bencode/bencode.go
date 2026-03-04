package bencode

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// Encode serialises v into bencode.
// Supported types: string, []byte, int, int64, map[string]interface{}, []interface{}.
func Encode(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encode(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case string:
		buf.WriteString(strconv.Itoa(len(val)))
		buf.WriteByte(':')
		buf.WriteString(val)
	case []byte:
		buf.WriteString(strconv.Itoa(len(val)))
		buf.WriteByte(':')
		buf.Write(val)
	case int:
		buf.WriteByte('i')
		buf.WriteString(strconv.Itoa(val))
		buf.WriteByte('e')
	case int64:
		buf.WriteByte('i')
		buf.WriteString(strconv.FormatInt(val, 10))
		buf.WriteByte('e')
	case map[string]interface{}:
		buf.WriteByte('d')
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := encode(buf, k); err != nil {
				return err
			}
			if err := encode(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	case []interface{}:
		buf.WriteByte('l')
		for _, item := range val {
			if err := encode(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	default:
		return fmt.Errorf("bencode: unsupported type %T", v)
	}
	return nil
}

// Decode deserialises bencode-encoded data.
// Returns string, int64, []interface{}, or map[string]interface{}.
func Decode(data []byte) (interface{}, error) {
	val, _, err := decodeValue(data, 0)
	return val, err
}

func decodeValue(data []byte, pos int) (interface{}, int, error) {
	if pos >= len(data) {
		return nil, pos, fmt.Errorf("bencode: unexpected end of data at position %d", pos)
	}
	switch {
	case data[pos] == 'i':
		end := bytes.IndexByte(data[pos+1:], 'e')
		if end < 0 {
			return nil, pos, fmt.Errorf("bencode: integer missing 'e'")
		}
		n, err := strconv.ParseInt(string(data[pos+1:pos+1+end]), 10, 64)
		if err != nil {
			return nil, pos, fmt.Errorf("bencode: invalid integer: %w", err)
		}
		return n, pos + 1 + end + 1, nil

	case data[pos] == 'l':
		pos++
		var list []interface{}
		for pos < len(data) && data[pos] != 'e' {
			val, newPos, err := decodeValue(data, pos)
			if err != nil {
				return nil, pos, err
			}
			list = append(list, val)
			pos = newPos
		}
		if pos >= len(data) {
			return nil, pos, fmt.Errorf("bencode: list missing 'e'")
		}
		return list, pos + 1, nil

	case data[pos] == 'd':
		pos++
		dict := make(map[string]interface{})
		for pos < len(data) && data[pos] != 'e' {
			keyRaw, newPos, err := decodeValue(data, pos)
			if err != nil {
				return nil, pos, err
			}
			key, ok := keyRaw.(string)
			if !ok {
				return nil, pos, fmt.Errorf("bencode: dict key must be string, got %T", keyRaw)
			}
			pos = newPos
			val, newPos, err := decodeValue(data, pos)
			if err != nil {
				return nil, pos, err
			}
			dict[key] = val
			pos = newPos
		}
		if pos >= len(data) {
			return nil, pos, fmt.Errorf("bencode: dict missing 'e'")
		}
		return dict, pos + 1, nil

	default:
		// String: <length>:<data>
		colon := bytes.IndexByte(data[pos:], ':')
		if colon < 0 {
			return nil, pos, fmt.Errorf("bencode: string missing ':'")
		}
		length, err := strconv.Atoi(string(data[pos : pos+colon]))
		if err != nil {
			return nil, pos, fmt.Errorf("bencode: invalid string length: %w", err)
		}
		start := pos + colon + 1
		end := start + length
		if end > len(data) {
			return nil, pos, fmt.Errorf("bencode: string length %d exceeds data", length)
		}
		return string(data[start:end]), end, nil
	}
}
