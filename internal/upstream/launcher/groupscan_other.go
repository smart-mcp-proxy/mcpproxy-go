//go:build unix && !linux && !darwin

package launcher

import "errors"

// groupHasOtherMember has no process-group listing on this platform; the
// reaper then kills the rest of the group before the reap (review round 6).
func groupHasOtherMember(int, int) (bool, error) {
	return false, errors.New("process-group listing not implemented on this platform")
}
