package localexec

import "testing"

// TestEnforcementForReportsOnlyWhatABackendProvides is the environment-free
// half of this package's enforcement-reporting coverage, and it replaces the
// coverage an earlier TestEnforcementReportsNoneWithoutAPlatformBackend only
// appeared to give.
//
// That test called New() and asserted all-none. It therefore asserted the
// truth of exactly one environment: it passed on CI, where bwrap is not
// installed, and failed on any developer machine where it is — while the
// six tests that actually exercise confinement skip on CI for the same
// reason. The claim being made here ("a Runner must not report full for an
// effect it does not actually confine") is a property of the derivation, not
// of the host, so it is tested as one, for every combination, everywhere.
func TestEnforcementForReportsOnlyWhatABackendProvides(t *testing.T) {
	cases := []struct {
		name      string
		available backendAvailability
		want      Enforcement
	}{
		{
			name:      "nothing available reports none for every effect",
			available: backendAvailability{MemoryRlimit: EnforcementNone, CPURlimit: EnforcementNone},
			want:      Enforcement{EnforcementNone, EnforcementNone, EnforcementNone, EnforcementNone},
		},
		{
			name:      "a sandbox backend alone confines filesystem and network, never memory or CPU",
			available: backendAvailability{Sandbox: true, MemoryRlimit: EnforcementNone, CPURlimit: EnforcementNone},
			want:      Enforcement{EnforcementFull, EnforcementFull, EnforcementNone, EnforcementNone},
		},
		{
			name:      "a memory cgroup whose cpu.max write failed bounds memory only",
			available: backendAvailability{MemoryCgroup: true, MemoryRlimit: EnforcementNone, CPURlimit: EnforcementNone},
			want:      Enforcement{EnforcementNone, EnforcementNone, EnforcementFull, EnforcementNone},
		},
		{
			name:      "a fully delegated cgroup bounds both memory and CPU",
			available: backendAvailability{MemoryCgroup: true, CPUCgroup: true, MemoryRlimit: EnforcementNone, CPURlimit: EnforcementNone},
			want:      Enforcement{EnforcementNone, EnforcementNone, EnforcementFull, EnforcementFull},
		},
		{
			name:      "a CPU cgroup without a memory cgroup cannot exist and reports neither",
			available: backendAvailability{CPUCgroup: true, MemoryRlimit: EnforcementNone, CPURlimit: EnforcementNone},
			want:      Enforcement{EnforcementNone, EnforcementNone, EnforcementNone, EnforcementNone},
		},
		{
			name:      "the macOS shape: Seatbelt plus a partial RLIMIT_AS and a full RLIMIT_CPU",
			available: backendAvailability{Sandbox: true, MemoryRlimit: EnforcementPartial, CPURlimit: EnforcementFull},
			want:      Enforcement{EnforcementFull, EnforcementFull, EnforcementPartial, EnforcementFull},
		},
		{
			name:      "an rlimit never silently upgrades itself to a cgroup's full guarantee",
			available: backendAvailability{MemoryRlimit: EnforcementPartial, CPURlimit: EnforcementNone},
			want:      Enforcement{EnforcementNone, EnforcementNone, EnforcementPartial, EnforcementNone},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := enforcementFor(testCase.available); got != testCase.want {
				t.Fatalf("enforcementFor(%+v) = %+v, want %+v", testCase.available, got, testCase.want)
			}
		})
	}
}
