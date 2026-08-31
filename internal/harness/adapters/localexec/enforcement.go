package localexec

// EnforcementLevel is how completely a Runner confines or bounds one
// effect of a command it runs.
type EnforcementLevel string

const (
	// EnforcementFull means the effect is bounded by the kernel or OS,
	// not by this process's own bookkeeping.
	EnforcementFull EnforcementLevel = "full"
	// EnforcementPartial means a bound exists but is weaker than a kernel-
	// enforced guarantee (for example a virtual-address-space rlimit
	// standing in for a monitored memory ceiling).
	EnforcementPartial EnforcementLevel = "partial"
	// EnforcementNone means nothing beyond this package's existing
	// argv-only/scrubbed-environment/process-group handling bounds the
	// effect.
	EnforcementNone EnforcementLevel = "none"
)

// Enforcement reports, per effect, how completely a Runner confines or
// bounds the commands it runs. It is a fact computed from what is actually
// active at construction time, never an assumed promise: a Runner must not
// report "full" for an effect it does not actually confine.
type Enforcement struct {
	Filesystem EnforcementLevel
	Network    EnforcementLevel
	Memory     EnforcementLevel
	CPU        EnforcementLevel
}
