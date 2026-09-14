package contextpolicy

import "testing"

func TestCanonicalConfig(t *testing.T) {
	a, ad, err := CanonicalConfig([]byte(`{ "z": [1, {"b":2,"a":1}], "a": true }`))
	if err != nil {
		t.Fatal(err)
	}
	b, bd, err := CanonicalConfig([]byte(`{"a":true,"z":[1,{"a":1,"b":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) || ad != bd {
		t.Fatal("key order/whitespace changed identity")
	}
	for _, raw := range []string{`null`, `[]`, `{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{} {}`, `{"a":NaN}`} {
		if _, _, err := CanonicalConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
