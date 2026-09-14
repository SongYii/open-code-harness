package docsguard_test

import "testing"

// TestIsNonRoutableExampleSeparatesIllustrationsFromCitations covers the
// classifier that decides which URLs TestExternalCitationsResolve probes.
//
// It runs offline and therefore on every pull request, unlike the gate it
// serves, which needs the network and runs nightly. That split matters: the
// classifier was wrong for months without anyone noticing, failing the
// nightly lane on four URLs that were never citations -- two placeholder
// ports, and two names RFC 2606 and RFC 6761 reserve precisely so that they
// never resolve.
//
// The negative cases are the point. A classifier that excused too much would
// make the whole gate green and worthless.
func TestIsNonRoutableExampleSeparatesIllustrationsFromCitations(t *testing.T) {
	illustrations := []struct {
		url    string
		reason string
	}{
		{"http://127.0.0.1:8080/", "loopback address in a configuration example"},
		{"http://localhost:3000", "loopback by name"},
		{"http://169.254.169.254/latest/meta-data/", "link-local: probing it would test this runner's own metadata service"},
		{"http://10.0.0.1/", "private range"},
		{"http://127.0.0.1:<port", "placeholder port the reader substitutes; url.Parse refuses it"},
		{"http://host:port/?token=", "wholly placeholder authority"},
		{"https://api.example.com/v1", "RFC 2606 reserved second-level name"},
		{"https://provider.invalid/v1", "RFC 6761 reserved TLD"},
		{"https://something.test", "RFC 6761 reserved TLD"},
	}
	for _, testCase := range illustrations {
		if !isNonRoutableExample(testCase.url) {
			t.Errorf("isNonRoutableExample(%q) = false, want true: %s", testCase.url, testCase.reason)
		}
	}

	citations := []struct {
		url    string
		reason string
	}{
		{"https://github.com/openai/codex", "an ordinary citation"},
		{"https://github.com/openai/codex/blob/67cc3c318d/codex-rs/thread-store/src/local/writer_lock.rs", "a pinned citation"},
		{"https://api.deepseek.com", "a real endpoint, even though it answers 401"},
		{"https://docs.kurrent.io/getting-started/", "a real documentation host"},
		{"https://example.computer/real", "a real host that merely starts with the reserved label"},
		{"https://exampleinvalid/x", "a real host that merely contains the reserved label"},
		// The one that matters: url.Parse refuses this too, but for a
		// reason that is a documentation defect rather than a placeholder,
		// so it must reach the network and fail the gate.
		{"https://github.com/openai/%zzcodex", "a typo'd real citation must still be probed, not excused"},
	}
	for _, testCase := range citations {
		if isNonRoutableExample(testCase.url) {
			t.Errorf("isNonRoutableExample(%q) = true, want false: %s", testCase.url, testCase.reason)
		}
	}
}
