// Command skillsfixture serves a fake upstream MCP server with three skills so
// the Skills tab of the Web UI can be exercised against a real core (spec 125,
// docs/development/web-ui-verification.md). It is a development tool, not part
// of any release.
//
//	skillsfixture -addr 127.0.0.1:18931
//
// Point an upstream at <printed URL>/mcp (streamable HTTP). To make a skill
// change, POST the new bytes of one file:
//
//	curl -X POST -H 'X-Skills-Fixture: mutate' --data-binary @SKILL.md \
//	  'http://127.0.0.1:18931/_fixture/mutate?skill=plain&file=SKILL.md'
//
// It answers only requests whose Host is the address it listens on (no DNS
// rebinding), and mutate refuses any request with an Origin header.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address (loopback only)")
	flag.Parse()
	if err := run(*addr); err != nil {
		log.Fatal(err)
	}
}

func run(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("skillsfixture listens on loopback only, got %q", host)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	f := skillstest.NewDetached()
	Seed(f)
	fmt.Fprintf(os.Stdout, "skillsfixture listening on http://%s/mcp\n", ln.Addr())
	return http.Serve(ln, Handler(f, allowedHosts(addr, ln.Addr())...))
}

// Seed registers the three skills: a plain one, one with a supporting text file
// and a binary file, and one with a file large enough to need several parts.
func Seed(f *skillstest.Fixture) {
	f.AddSkill(skillstest.Skill{Path: "plain"})
	f.AddSkill(skillstest.Skill{
		Path: "with-files",
		Files: map[string][]byte{
			"notes.txt": []byte("Supporting notes\nsecond line\n"),
			"logo.bin":  {0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02, 0x03},
		},
		Binary: []string{"logo.bin"},
	})
	var big strings.Builder
	for i := 1; big.Len() < 150*1024; i++ {
		fmt.Fprintf(&big, "line %06d of the large reference file\n", i)
	}
	f.AddSkill(skillstest.Skill{
		Path:  "big",
		Files: map[string][]byte{"data/large.txt": []byte(big.String())},
	})
}

// MutateHeader must carry MutateHeaderValue on every mutate request. A web page
// cannot add a custom header to a cross-site request without a CORS preflight,
// which the fixture never grants, so a page the operator happens to have open
// cannot change a skill under review.
const (
	MutateHeader      = "X-Skills-Fixture"
	MutateHeaderValue = "mutate"
)

// allowedHosts is the set of Host values the fixture answers: the address it
// actually listens on, plus localhost:<port> when it was started as localhost.
func allowedHosts(requested string, actual net.Addr) []string {
	out := []string{actual.String()}
	if host, _, err := net.SplitHostPort(requested); err == nil && host == "localhost" {
		if _, port, err := net.SplitHostPort(actual.String()); err == nil {
			out = append(out, net.JoinHostPort("localhost", port))
		}
	}
	return out
}

// Handler serves the fixture's JSON-RPC on every path except /_fixture/mutate,
// which replaces one file: POST ?skill=<path>&file=<relpath> with the new bytes
// as the body and the MutateHeader set. Any request whose Host is not one of
// hosts (DNS rebinding: a foreign name that resolves to 127.0.0.1) is refused
// with 421, and a mutate request that carries an Origin header (every browser
// request does) is refused with 403.
func Handler(f *skillstest.Fixture, hosts ...string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/_fixture/mutate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := r.Header["Origin"]; ok {
			http.Error(w, "mutate refuses browser requests (Origin header present)", http.StatusForbidden)
			return
		}
		if r.Header.Get(MutateHeader) != MutateHeaderValue {
			http.Error(w, "mutate needs the header "+MutateHeader+": "+MutateHeaderValue, http.StatusForbidden)
			return
		}
		skill, file := r.URL.Query().Get("skill"), r.URL.Query().Get("file")
		if skill == "" || file == "" {
			http.Error(w, "skill and file are required", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil || len(body) == 0 {
			http.Error(w, "a non-empty body is required", http.StatusBadRequest)
			return
		}
		if err := f.TryMutate(skill, file, body); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", f.Handler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(hosts, r.Host) {
			http.Error(w, "unexpected Host", http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
