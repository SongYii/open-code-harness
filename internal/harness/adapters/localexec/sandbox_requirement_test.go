package localexec

import (
	"os"
	"testing"
)

// requireExecSandboxEnv, when set to any non-empty value, turns "this
// environment has no usable OS-level exec sandbox" from a skip into a
// failure.
//
// It exists because the skip on its own is indistinguishable from success
// in a CI log. The bwrap-backed confinement tests are the only executable
// evidence for SECURITY.md's central claim — that a model-proposed command
// cannot write outside the workspace, reach the network, or see host
// processes — and an environment without bwrap skips every one of them
// while the job still reports green. A lane that installs the backend on
// purpose sets this variable, so a backend that is missing, unsupported
// (WSL1), or present-but-blocked (unprivileged user namespaces restricted
// by AppArmor, the default on recent Ubuntu) fails that lane instead of
// quietly emptying it.
const requireExecSandboxEnv = "OCH_REQUIRE_EXEC_SANDBOX"

// requireFunctionalSandbox skips the calling test when this environment's
// own confinement backend cannot actually confine a process, unless the
// lane has declared that it must.
func requireFunctionalSandbox(t *testing.T, runner *Runner) {
	t.Helper()
	if runner.Enforcement().Filesystem == EnforcementFull {
		return
	}
	available, reason := Availability()
	if os.Getenv(requireExecSandboxEnv) != "" {
		t.Fatalf("%s is set, but this environment has no usable exec sandbox backend: Availability() = %v, %q", requireExecSandboxEnv, available, reason)
	}
	t.Skipf("no usable exec sandbox backend in this environment: %s", reason)
}
