package eval

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

func customSubjectToolPolicy(t *testing.T, raw string) *SubjectToolPolicy {
	t.Helper()
	canonical, digest, err := toolpolicy.CanonicalConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return &SubjectToolPolicy{ID: "deny_tools", Version: "1.0.0", ConfigDigest: digest, Config: canonical}
}

func TestSubjectToolPolicyCanonicalIdentityAndACPFlags(t *testing.T) {
	first := validSubject()
	first.Policy.ToolPolicy = customSubjectToolPolicy(t, `{"names":["exec"],"enabled":true}`)
	second := validSubject()
	second.Policy.ToolPolicy = customSubjectToolPolicy(t, `{ "enabled": true, "names": ["exec"] }`)

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical subjects differ:\n%s\n%s", firstJSON, secondJSON)
	}
	firstDigest, err := SubjectDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := SubjectDigest(second)
	if err != nil || firstDigest != secondDigest {
		t.Fatalf("subject digests differ: %q %q %v", firstDigest, secondDigest, err)
	}

	argv, err := NormalizedArgv(first)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"-tool-policy":         "deny_tools",
		"-tool-policy-version": "1.0.0",
		"-tool-policy-config":  `{"enabled":true,"names":["exec"]}`,
	}
	for flag, value := range want {
		if countArg(argv, flag) != 1 || countArg(argv, value) != 1 {
			t.Fatalf("argv missing unique %s %s: %v", flag, value, argv)
		}
	}
	if _, err := BuildConfig(first, AttemptRootDirectories{}, "runtime", nil); err == nil || !strings.Contains(err.Error(), "ACP launcher") {
		t.Fatalf("BuildConfig() error = %v", err)
	}
}

func TestSubjectToolPolicyRejectsInvalidIdentityAndConfiguration(t *testing.T) {
	valid := customSubjectToolPolicy(t, `{"names":["exec"]}`)
	tests := []struct {
		name   string
		mutate func(*Subject)
	}{
		{name: "wrong digest", mutate: func(subject *Subject) { subject.Policy.ToolPolicy.ConfigDigest = strings.Repeat("b", 64) }},
		{name: "reserved ID", mutate: func(subject *Subject) { subject.Policy.ToolPolicy.ID = toolpolicy.DefaultID }},
		{name: "invalid ID", mutate: func(subject *Subject) { subject.Policy.ToolPolicy.ID = "deny/tools" }},
		{name: "invalid version", mutate: func(subject *Subject) { subject.Policy.ToolPolicy.Version = "bad version" }},
		{name: "invalid config", mutate: func(subject *Subject) { subject.Policy.ToolPolicy.Config = json.RawMessage(`{"a":1,"a":2}`) }},
		{name: "non-default mode", mutate: func(subject *Subject) { subject.Policy.Mode = string(policy.ModeReadOnly) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subject := validSubject()
			copy := *valid
			copy.Config = append(json.RawMessage(nil), valid.Config...)
			subject.Policy.ToolPolicy = &copy
			test.mutate(&subject)
			if err := subject.Validate(); err == nil {
				t.Fatalf("Validate() accepted %#v", subject.Policy.ToolPolicy)
			}
		})
	}
}

func TestSubjectWithoutToolPolicyKeepsLegacyJSON(t *testing.T) {
	subject := validSubject()
	encoded, err := json.Marshal(subject)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"toolPolicy"`)) {
		t.Fatalf("legacy Subject gained toolPolicy: %s", encoded)
	}
}

func countArg(argv []string, target string) int {
	count := 0
	for _, value := range argv {
		if value == target {
			count++
		}
	}
	return count
}
