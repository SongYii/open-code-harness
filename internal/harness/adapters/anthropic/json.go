package anthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

// Standard JSON decoding silently repairs malformed Unicode and accepts
// duplicate keys. Neither is safe for content that must be replayed unchanged.
// Do not share an adapter implementation with a sibling adapter; the native
// protocol stays independently testable until a shared contract is justified.
func validReplayJSON(data []byte) bool {
	if !utf8.Valid(data) || !json.Valid(data) {
		return false
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		value, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if i+6 >= len(data) || string(data[i+1:i+3]) != `\u` {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return uniqueJSONValue(decoder, 0) && func() bool { _, err := decoder.Token(); return err == io.EOF }()
}

func uniqueJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return false
			}
			seen[name] = true
			if !uniqueJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case json.Delim('['):
		for decoder.More() {
			if !uniqueJSONValue(decoder, depth+1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		_, delimiter := token.(json.Delim)
		return !delimiter
	}
}

func object(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if !validReplayJSON(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return nil, errProtocol
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return nil, errProtocol
	}
	for name := range fields {
		found := false
		for _, candidate := range allowed {
			found = found || name == candidate
		}
		if !found {
			return nil, errProtocol
		}
	}
	return fields, nil
}

func stringField(fields map[string]json.RawMessage, name string) (string, error) {
	data := bytes.TrimSpace(fields[name])
	var value string
	if len(data) == 0 || data[0] != '"' || json.Unmarshal(data, &value) != nil {
		return "", errProtocol
	}
	return value, nil
}
