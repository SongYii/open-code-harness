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

// backendAvailability is what a Runner actually found at construction
// time, separated from the report it derives so the derivation itself can
// be exercised for every combination on any host. A test running on a
// machine that happens to have bwrap installed cannot otherwise observe
// the "nothing is available" answer at all, and one running without it
// cannot observe any other.
type backendAvailability struct {
	// Sandbox is whether this platform's OS-level confinement backend —
	// bwrap on Linux, Seatbelt on macOS — probed successfully.
	Sandbox bool
	// MemoryCgroup is whether a cgroup v2 memory quota was created.
	MemoryCgroup bool
	// CPUCgroup is whether that same cgroup's cpu.max write also
	// succeeded. cpu delegation fails independently of memory (CPU quota
	// design §3): a cpu.max write failure never undoes the memory quota
	// that already succeeded.
	CPUCgroup bool
	// MemoryRlimit and CPURlimit are the platform's own process-limit
	// fallbacks (RLIMIT_AS and RLIMIT_CPU on macOS, nothing elsewhere),
	// applied after any cgroup answer.
	MemoryRlimit EnforcementLevel
	CPURlimit    EnforcementLevel
}

// enforcementFor derives the per-effect report from what is available. It
// never reports a level a backend does not actually provide, and an
// rlimit-based bound, being the weaker mechanism, is the last word only
// because it is only ever non-none on a platform with no cgroup answer.
func enforcementFor(available backendAvailability) Enforcement {
	enforcement := Enforcement{
		Filesystem: EnforcementNone,
		Network:    EnforcementNone,
		Memory:     EnforcementNone,
		CPU:        EnforcementNone,
	}
	if available.Sandbox {
		// --unshare-net denies all network access outright (design §3.2);
		// the read-only host with only the workspace rebound read-write
		// gives the same guarantee for filesystem writes.
		enforcement.Filesystem = EnforcementFull
		enforcement.Network = EnforcementFull
	}
	if available.MemoryCgroup {
		enforcement.Memory = EnforcementFull
		if available.CPUCgroup {
			enforcement.CPU = EnforcementFull
		}
	}
	if available.MemoryRlimit != EnforcementNone {
		enforcement.Memory = available.MemoryRlimit
	}
	if available.CPURlimit != EnforcementNone {
		enforcement.CPU = available.CPURlimit
	}
	return enforcement
}
