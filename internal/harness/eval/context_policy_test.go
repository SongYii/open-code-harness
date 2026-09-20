package eval

import (
	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
	"testing"
)

func TestCustomContextPolicyIdentityAndACPFlags(t *testing.T) {
	subject := validSubject()
	baseline, err := SubjectDigest(subject)
	if err != nil {
		t.Fatal(err)
	}
	config, digest, err := contextpolicy.CanonicalConfig([]byte(`{"turns":2}`))
	if err != nil {
		t.Fatal(err)
	}
	subject.Context.Policy = &SubjectContextPolicy{ID: "keep_last_n_turns", Version: "1.0.0", ConfigDigest: digest, Config: config}
	custom, err := SubjectDigest(subject)
	if err != nil || custom == baseline {
		t.Fatalf("policy missing from identity: %v", err)
	}
	argv, err := NormalizedArgv(subject)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"-context-policy", "keep_last_n_turns", "-context-policy-version", "1.0.0", "-context-policy-config", string(config)} {
		if !containsArg(argv, value) {
			t.Fatalf("missing %s in %v", value, argv)
		}
	}
	if _, err := BuildConfig(subject, AttemptRootDirectories{}, "runtime", nil); err == nil {
		t.Fatal("stock in-process silently ran builtin")
	}
	subject.Context.Policy.Version = "2.0.0"
	next, err := SubjectDigest(subject)
	if err != nil || next == custom {
		t.Fatal("version not frozen")
	}
	subject.Context.Policy.Config = []byte(`{"turns":3}`)
	if err := subject.Validate(); err == nil {
		t.Fatal("config/digest mismatch accepted")
	}
}
