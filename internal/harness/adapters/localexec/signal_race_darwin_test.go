//go:build darwin

package localexec

import (
	"errors"
	"syscall"
	"testing"
)

func TestDarwinSignalRechecksExitRaceWithoutExcusingLivePermissionFailure(t *testing.T) {
	root := processIdentity{pid: 42, start: 1}
	for _, test := range []struct {
		name      string
		after     []processUsage
		sampleErr error
		wantGone  bool
	}{
		{"exited", []processUsage{{identity: root, alive: false}}, nil, true},
		{"gone", nil, nil, true},
		{"still_live", []processUsage{{identity: root, alive: true}}, nil, false},
		{"inaccessible", nil, syscall.EPERM, false},
		{"reused", []processUsage{{identity: processIdentity{pid: 42, start: 2}, alive: false}}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			sample := func(int) ([]processUsage, error) {
				calls++
				if calls == 1 {
					return []processUsage{{identity: root, alive: true}}, nil
				}
				return test.after, test.sampleErr
			}
			err := signalVerifiedDarwinGroup(42, 1, nil, syscall.SIGKILL, sample, func(pid int, sig syscall.Signal) error {
				if pid != -42 || sig != syscall.SIGKILL {
					t.Fatal("wrong signal target")
				}
				return syscall.EPERM
			})
			want := syscall.EPERM
			if test.wantGone {
				want = syscall.ESRCH
			}
			if !errors.Is(err, want) || calls != 2 {
				t.Fatalf("error=%v samples=%d", err, calls)
			}
		})
	}
}
