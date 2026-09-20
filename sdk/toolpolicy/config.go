package toolpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ValidLabel bounds registration and attribution fields to printable,
// portable identifiers.
func ValidLabel(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-+", char)) {
			return false
		}
	}
	return true
}

// CanonicalConfig normalizes whitespace and object key order while preserving
// JSON number spelling. Config must be an object no larger than MaxConfigBytes
// and may not contain duplicate keys. The digest is not a secret-hiding
// mechanism: never put credentials or other secrets in config.
func CanonicalConfig(raw json.RawMessage) (json.RawMessage, string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > MaxConfigBytes {
		return nil, "", fmt.Errorf("tool policy config exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeConfigValue(decoder)
	if err != nil {
		return nil, "", err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, "", fmt.Errorf("tool policy config must be an object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, "", fmt.Errorf("tool policy config has trailing data")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

func decodeConfigValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := object[name]; exists {
				return nil, fmt.Errorf("duplicate tool policy config key %q", name)
			}
			value, err := decodeConfigValue(decoder)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeConfigValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := decoder.Token()
		return array, err
	default:
		return token, nil
	}
}
