package eval

import (
	"fmt"
	"os"
)

// requiredChildEnvironmentVariables are the minimal OS runtime variables
// an och -acp child process needs to run at all (design §16's "required
// OS runtime variables"): PATH so any exec-adapter tool invocation can
// resolve a program, HOME for anything in the Go runtime or standard
// library that consults it, and TMPDIR for os.TempDir(). Each is
// forwarded only when actually set in this process's own environment —
// never fabricated.
var requiredChildEnvironmentVariables = []string{"PATH", "HOME", "TMPDIR"}

// BuildChildEnvironment constructs the minimal allowlisted environment an
// ACP subprocess writer receives (design §16): the required OS runtime
// variables above, plus subject's own named credential variable and its
// current value. It never forwards os.Environ() wholesale — an unrelated
// environment variable never reaches the child.
//
// design §16 also allows "explicitly declared fixture variables" for a
// fixture-lane Subject. Subject carries no field naming any today, so
// there is nothing yet to add here beyond the credential — a future
// Subject field would need a matching addition to this function, not a
// guess at what such a field might contain.
func BuildChildEnvironment(subject Subject) ([]string, error) {
	if err := subject.Validate(); err != nil {
		return nil, fmt.Errorf("eval: build child environment: %w", err)
	}
	var env []string
	for _, name := range requiredChildEnvironmentVariables {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	value, ok := os.LookupEnv(subject.Provider.CredentialEnvVar)
	if !ok {
		return nil, fmt.Errorf("eval: build child environment: credential env var %q is not set in this process's own environment", subject.Provider.CredentialEnvVar)
	}
	env = append(env, subject.Provider.CredentialEnvVar+"="+value)
	return env, nil
}
