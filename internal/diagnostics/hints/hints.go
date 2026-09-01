// Package hints builds diagnostics.ClassifierHints from configuration.
//
// It exists so the supervisor and the managed client classify the SAME failure
// the same way. Each used to (or would) hand-roll the Docker-isolation
// predicate, and a hand-rolled mirror has already drifted from the spawn path
// once (GH #1142): a `mode: "docker"` server was Docker-launched but classified
// as a plain host ENOENT. Since GH #1145 the classification decides whether a
// server is parked as terminally failed, a drift now changes retry behaviour and
// not just a help message.
package hints

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

// For builds the classifier hints for one server's failure.
//
// The Docker predicate delegates to config.ResolveIsolation — the SAME resolver
// the spawn path branches on (core.IsolationManager.ShouldIsolate wraps it) — so
// the hint cannot drift from the launch decision. The predicate is
// Mode == docker, not ResolvedIsolation.Isolated: the DOCKER remediation codes
// describe the container LAUNCH path, which runs whenever the mode is docker.
//
// The enrichment fields (command, image override, default images) only feed
// MCP-2909's runtime-aware remediation text; they never change classification.
func For(global *config.Config, srv *config.ServerConfig, transport string) diagnostics.ClassifierHints {
	h := diagnostics.ClassifierHints{Transport: transport}
	if srv == nil {
		return h
	}
	h.ServerID = srv.Name

	var dockerGlobal *config.DockerIsolationConfig
	if global != nil {
		dockerGlobal = global.DockerIsolation
	}
	h.DockerIsolated = config.ResolveIsolation(dockerGlobal, srv).Mode == config.IsolationModeDocker
	if !h.DockerIsolated {
		return h
	}

	h.DockerCommand = srv.Command
	// The args carry the git-dependency signal (`--from pkg@git+https://…`) that
	// the #1143 remediation reads to decide whether the automatic git-capable
	// image substitution applies to this server. Without them the toolchain
	// remediation cannot tell a git install from an ordinary one.
	h.DockerArgs = srv.Args
	if srv.Isolation != nil {
		h.DockerImageOverride = srv.Isolation.Image
	}
	if dockerGlobal != nil {
		h.DockerDefaultImages = dockerGlobal.DefaultImages
	}
	return h
}
