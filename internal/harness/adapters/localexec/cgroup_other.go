//go:build !linux

package localexec

// cgroupQuota is an unused placeholder off Linux: newCgroupQuota always
// returns nil below, so no method here is ever reached, but Runner (built
// on every platform) needs the type and its methods to exist.
type cgroupQuota struct{}

func newCgroupQuota(_, _, _, _ uint64) (*cgroupQuota, string, string) {
	return nil, "cgroup v2 memory quotas are Linux-only", ""
}

func (q *cgroupQuota) addProcess(_ int) error              { return nil }
func (q *cgroupQuota) register(_ int) <-chan struct{}      { return nil }
func (q *cgroupQuota) unregister(_ int)                    {}
func (q *cgroupQuota) close()                              {}
func (q *cgroupQuota) readThrottledCount() (uint64, error) { return 0, nil }
