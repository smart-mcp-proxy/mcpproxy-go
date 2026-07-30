//go:build unix

package sandbox

import "testing"

// TestThreadLost pins the fail-closed guard's decision table. RunChild consults
// it immediately before execve: a true verdict must abort the exec, because
// exec'ing from a thread outside the Landlock domain runs the untrusted command
// with no confinement at all.
func TestThreadLost(t *testing.T) {
	cases := []struct {
		name   string
		rep    Report
		nowTID int
		want   bool
	}{
		{
			name:   "same thread — safe to exec",
			rep:    Report{LandlockABI: 5, LandlockTID: 4242},
			nowTID: 4242,
			want:   false,
		},
		{
			name:   "thread changed after confinement — must refuse",
			rep:    Report{LandlockABI: 5, LandlockTID: 4242},
			nowTID: 4243,
			want:   true,
		},
		{
			name:   "no domain enforced (rlimits only) — nothing to verify",
			rep:    Report{LandlockTID: 0},
			nowTID: 4243,
			want:   false,
		},
		{
			name:   "Landlock unavailable under BestEffort — nothing to verify",
			rep:    Report{LandlockABI: -1, LandlockTID: 0},
			nowTID: 99,
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := threadLost(c.rep, c.nowTID); got != c.want {
				t.Errorf("threadLost(%+v, %d) = %v, want %v", c.rep, c.nowTID, got, c.want)
			}
		})
	}
}
