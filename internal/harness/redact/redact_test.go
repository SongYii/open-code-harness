package redact

import (
	"strings"
	"testing"
)

func TestTextPassesThroughContentWithNoSecretShape(t *testing.T) {
	input := "hello, this is an ordinary file with no secrets in it at all."
	if got := Text(input); got != input {
		t.Fatalf("Text(%q) = %q, want unchanged", input, got)
	}
}

func TestTextRedactsAuthorizationHeader(t *testing.T) {
	input := "Authorization: Basic dXNlcjpwYXNz"
	got := Text(input)
	if got == input {
		t.Fatalf("Text(%q) left the header unredacted", input)
	}
	if got != "Authorization: [redacted]" {
		t.Fatalf("Text(%q) = %q, want %q", input, got, "Authorization: [redacted]")
	}
}

func TestTextRedactsBearerToken(t *testing.T) {
	input := "sent header Bearer abc.def-GHI_123 to the server"
	want := "sent header [redacted] to the server"
	if got := Text(input); got != want {
		t.Fatalf("Text(%q) = %q, want %q", input, got, want)
	}
}

func TestTextRedactsProviderStyleSecretKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"plain sk-", "key is sk-abcdEFGH12345"},
		{"sk-ant-", "key is sk-ant-abcdEFGH12345"},
		{"sk-proj-", "key is sk-proj-abcdEFGH12345"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Text(test.input)
			if got == test.input {
				t.Fatalf("Text(%q) left the key unredacted", test.input)
			}
			if got != "key is [redacted]" {
				t.Fatalf("Text(%q) = %q, want %q", test.input, got, "key is [redacted]")
			}
		})
	}
}

func TestTextRedactsQueryStringKeyPreservingParameterName(t *testing.T) {
	input := "GET https://api.example.com/v1/models?key=AIzaSyD-abcdefgh12345 HTTP/1.1"
	want := "GET https://api.example.com/v1/models?key=[redacted] HTTP/1.1"
	if got := Text(input); got != want {
		t.Fatalf("Text(%q) = %q, want %q", input, got, want)
	}
}

func TestTextRedactsQueryStringKeyAfterAmpersand(t *testing.T) {
	input := "https://api.example.com/v1?model=gpt&key=abcdef123456&extra=1"
	want := "https://api.example.com/v1?model=gpt&key=[redacted]&extra=1"
	if got := Text(input); got != want {
		t.Fatalf("Text(%q) = %q, want %q", input, got, want)
	}
}

func TestTextRedactsGenericKeyValueAssignment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"env style key", "API_KEY=sup3rSecretValue123", "API_KEY=[redacted]"},
		{"password colon", "password: hunter2", "password: [redacted]"},
		{"secret assignment", "SECRET=letmein", "SECRET=[redacted]"},
		{"token assignment", "token=abc123xyz", "token=[redacted]"},
		{"credential assignment", "credential=abc123xyz", "credential=[redacted]"},
		{"does not match inside a longer word", "monkey=business", "monkey=business"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Text(test.input); got != test.want {
				t.Fatalf("Text(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// awsKeyLike and githubTokenLike build credential-shaped test fixtures by
// concatenation, not as a single string literal, so this source file never
// contains a contiguous substring a secret scanner (including GitHub's own
// push-protection scanner, which flagged an earlier version of this file)
// would flag as a real credential.
func awsKeyLike(prefix string) string { return prefix + "ABCDEFGHIJKLMNOP" }
func githubTokenLike(prefix string) string {
	return prefix + "1234567890abcdefghijklmnopqrstuvwx" + "yz12"
}

func TestTextRedactsAWSAccessKeyID(t *testing.T) {
	tests := []string{
		"aws_access_key_id " + awsKeyLike("AKIA") + " in the config",
		"aws_access_key_id " + awsKeyLike("ASIA") + " in the config",
	}
	for _, input := range tests {
		got := Text(input)
		if got == input {
			t.Fatalf("Text(%q) left the AWS key unredacted", input)
		}
	}
}

func TestTextRedactsGitHubTokens(t *testing.T) {
	tests := []string{
		githubTokenLike("ghp_"),
		githubTokenLike("gho_"),
		"github_pat_" + "1234567890abcdefghijklmnopqrstuvwx",
	}
	for _, input := range tests {
		got := Text(input)
		if got == input {
			t.Fatalf("Text(%q) left the GitHub token unredacted", input)
		}
	}
}

func TestTextRedactsPEMPrivateKeyBlockAsOneMatch(t *testing.T) {
	input := "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD91\nassorted base64 lines here\n-----END RSA PRIVATE KEY-----\nafter"
	got := Text(input)
	want := "before\n[redacted]\nafter"
	if got != want {
		t.Fatalf("Text(pem) = %q, want %q", got, want)
	}
}

func TestTextRedactsTwoDistinctSecretsInOneString(t *testing.T) {
	awsKey := awsKeyLike("AKIA")
	input := "leaked a Bearer abc123secrettoken, and separately leaked " + awsKey + " too"
	got := Text(input)
	if got == input {
		t.Fatal("Text() left both secrets unredacted")
	}
	// Both shapes must be gone from the output, and the unrelated
	// surrounding prose must survive.
	for _, leaked := range []string{"abc123secrettoken", awsKey} {
		if strings.Contains(got, leaked) {
			t.Fatalf("Text(%q) = %q, still leaks %q", input, got, leaked)
		}
	}
	for _, keep := range []string{"leaked a", "and separately leaked", "too"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("Text(%q) = %q, want to keep %q", input, got, keep)
		}
	}
}

func TestTextRedactsASecretEmbeddedInSurroundingFileContent(t *testing.T) {
	input := "# .env for local dev\nDATABASE_URL=postgres://localhost/app\nAPI_KEY=sup3rSecretValue123\nDEBUG=true\n"
	got := Text(input)
	if strings.Contains(got, "sup3rSecretValue123") {
		t.Fatalf("Text() leaked the secret: %q", got)
	}
	for _, keep := range []string{"# .env for local dev", "DATABASE_URL=postgres://localhost/app", "DEBUG=true"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("Text() corrupted surrounding content, want to keep %q in %q", keep, got)
		}
	}
}

// TestTextKnownFalsePositiveOnGenericAssignmentShape demonstrates the
// design's own disclosed, accepted false-positive case (design §9): a
// comment that merely mentions rotating a secret, shaped like an
// assignment, is redacted even though nothing sensitive is present. This
// is the accepted cost of catching real .env-shaped secrets, proven here
// rather than left as an unverified claim in prose.
func TestTextKnownFalsePositiveOnGenericAssignmentShape(t *testing.T) {
	input := "# TODO: rotate the API secret = soon"
	want := "# TODO: rotate the API secret = [redacted]"
	if got := Text(input); got != want {
		t.Fatalf("Text(%q) = %q, want the disclosed false positive %q", input, got, want)
	}
}
