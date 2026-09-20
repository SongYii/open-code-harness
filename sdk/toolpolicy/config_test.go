package toolpolicy

import (
	"strings"
	"testing"
)

func TestCanonicalConfigPreservesOneIdentity(t *testing.T) {
	first, firstDigest, err := CanonicalConfig([]byte(`{ "z": [1,{"b":2,"a":1}], "a": true }`))
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := CanonicalConfig([]byte(`{"a":true,"z":[1,{"a":1,"b":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != `{"a":true,"z":[1,{"a":1,"b":2}]}` {
		t.Fatalf("canonical config = %s", first)
	}
	if string(first) != string(second) || firstDigest != secondDigest {
		t.Fatalf("identity differs: (%s,%s) != (%s,%s)", first, firstDigest, second, secondDigest)
	}
	if len(firstDigest) != 64 || firstDigest != strings.ToLower(firstDigest) {
		t.Fatalf("digest = %q", firstDigest)
	}
}

func TestCanonicalConfigPreservesNumberSpelling(t *testing.T) {
	canonical, digest, err := CanonicalConfig([]byte(`{"n":1.00}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{"n":1.00}` {
		t.Fatalf("canonical config = %s, want number spelling preserved", canonical)
	}
	integer, integerDigest, err := CanonicalConfig([]byte(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(integer) == string(canonical) || integerDigest == digest {
		t.Fatalf("distinct number spellings collapsed: (%s,%s) and (%s,%s)", canonical, digest, integer, integerDigest)
	}
}

func TestCanonicalConfigDefaultsEmptyInput(t *testing.T) {
	canonical, digest, err := CanonicalConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{}` || digest != "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a" {
		t.Fatalf("empty identity = (%s,%s)", canonical, digest)
	}
}

func TestCanonicalConfigRejectsInvalidDocuments(t *testing.T) {
	tests := [][]byte{
		[]byte(`null`),
		[]byte(`[]`),
		[]byte(`{"a":1,"a":2}`),
		[]byte(`{"a":{"b":1,"b":2}}`),
		[]byte(`{} {}`),
		[]byte(`{"a":NaN}`),
		append([]byte(`{"x":"`), append(make([]byte, MaxConfigBytes), []byte(`"}`)...)...),
	}
	for _, raw := range tests {
		if _, _, err := CanonicalConfig(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestValidLabel(t *testing.T) {
	for _, value := range []string{"policy.v1", "deny_tools+1", strings.Repeat("a", 128)} {
		if !ValidLabel(value) {
			t.Fatalf("rejected %q", value)
		}
	}
	for _, value := range []string{"", "has space", "slash/name", "é", strings.Repeat("a", 129)} {
		if ValidLabel(value) {
			t.Fatalf("accepted %q", value)
		}
	}
}
