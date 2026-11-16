package core

import "net/http/httptest"

// closeHTTPFixture closes an in-process upstream that the code under test may
// hold a standalone GET stream to (GH #1910: streamable-HTTP upstreams that
// advertise tools.listChanged). httptest.Server.Close waits for every active
// request, and that GET only ends when the client disconnects, so the open
// connections are cut first. This is also what a dying server looks like.
func closeHTTPFixture(s *httptest.Server) {
	s.CloseClientConnections()
	s.Close()
}
