package mcplogin

import "sort"

// CancelAllReport cancels every pending and approved-but-unconsumed request in
// ONE critical section and returns the ids of the requests that were still
// pending, so a caller can announce them without a race between listing and
// cancelling.
func (s *Store) CancelAllReport() (pendingIDs []string, cancelled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.reqs {
		if r := s.live(id); r != nil && r.Status == StatusPending {
			pendingIDs = append(pendingIDs, id)
		}
	}
	sort.Strings(pendingIDs)
	return pendingIDs, s.cancelWhere(func(*Request) bool { return true })
}
