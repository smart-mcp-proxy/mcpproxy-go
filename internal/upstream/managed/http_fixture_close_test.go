package managed

import (
	"net/http/httptest"
	"time"
)

// closeHTTPFixture closes an in-process upstream that the code under test may
// hold a standalone GET stream to (GH #1910: streamable-HTTP upstreams that
// advertise tools.listChanged). httptest.Server.Close waits for every active
// request, and that GET only ends when the client disconnects. The stream is
// opened asynchronously after connect, so a GET can arrive after a single
// CloseClientConnections: Close runs in the background (it closes the
// listener first, so no new connection is accepted) while open connections are
// cut until it returns. This is also what a dying server looks like.
func closeHTTPFixture(s *httptest.Server) {
	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		s.CloseClientConnections()
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}
