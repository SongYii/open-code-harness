package acpweb

import "testing"

func TestGenerateTokenIsRandomAndCorrectLength(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if a == b {
		t.Fatal("two successive tokens must not be equal")
	}
	if len(a) != tokenBytes*2 { // hex-encoded
		t.Fatalf("got token length %d, want %d", len(a), tokenBytes*2)
	}
}

func TestValidateTokenExactMatchOnly(t *testing.T) {
	cases := []struct {
		name       string
		want, got  string
		wantResult bool
	}{
		{"exact match", "abc123", "abc123", true},
		{"different value", "abc123", "abc124", false},
		{"different length", "abc123", "abc1234", false},
		{"empty got", "abc123", "", false},
		{"both empty", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidateToken(c.want, c.got); got != c.wantResult {
				t.Fatalf("ValidateToken(%q, %q) = %v, want %v", c.want, c.got, got, c.wantResult)
			}
		})
	}
}

func TestCheckOrigin(t *testing.T) {
	const self = "http://127.0.0.1:54321"
	cases := []struct {
		name       string
		origin     string
		wantResult bool
	}{
		{"matching origin", self, true},
		{"absent origin", "", true},
		{"different port", "http://127.0.0.1:9999", false},
		{"different scheme", "https://127.0.0.1:54321", false},
		{"hostile origin", "http://evil.example", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CheckOrigin(self, c.origin); got != c.wantResult {
				t.Fatalf("CheckOrigin(%q, %q) = %v, want %v", self, c.origin, got, c.wantResult)
			}
		})
	}
}

func TestUpgradeAllowedRequiresBothChecksIndependently(t *testing.T) {
	const self = "http://127.0.0.1:54321"
	const wantToken = "correct-token"

	cases := []struct {
		name        string
		origin      string
		token       string
		wantAllowed bool
	}{
		{"matching origin and token", self, wantToken, true},
		{"absent origin, matching token", "", wantToken, true},
		{"mismatched origin, matching token", "http://evil.example", wantToken, false},
		{"matching origin, wrong token", self, "wrong", false},
		{"matching origin, missing token", self, "", false},
		{"absent origin, wrong token", "", "wrong", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UpgradeAllowed(self, c.origin, wantToken, c.token)
			if got != c.wantAllowed {
				t.Fatalf("UpgradeAllowed(origin=%q, token=%q) = %v, want %v", c.origin, c.token, got, c.wantAllowed)
			}
		})
	}
}
