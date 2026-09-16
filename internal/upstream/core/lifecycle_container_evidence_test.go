package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// Codex round 9 (PR E), MUST-FIX 1 and 2 (Spec 105 D8): lifecycle housekeeping
// records (connection failure, init failure, disconnect) and
// GetConnectionDiagnostics named a container — or trusted `docker inspect` by
// id alone — without current ownership evidence. containerID is assigned only
// by trackCidfileContainer or the cidfile-timeout name-recovery fallback
// (docker.go), both of which verify ownership via Docker's read-back before
// ever setting it, and always pair it with containerOwner; containerName
// alone is the GENERATED canonical name, set before Docker has confirmed
// anything. The tests below drive the actual lifecycle code paths (not just
// the shared field-selection helper) through the fake docker shim and a
// failing MCP transport, covering both arms at every site the round 8
// finding named.

// TestDockerContainerLogFields is the shared field-selection helper every
// lifecycle site (connection.go, connection_stdio.go, connection_lifecycle.go
// x2) now goes through: empty containerID (never verified — trackCidfileContainer
// / the name-recovery fallback never adopted anything, whether or not a
// GENERATED containerName exists) yields no fields at all; a non-empty
// containerID (only ever set alongside containerOwner) yields all three.
func TestDockerContainerLogFields(t *testing.T) {
	t.Run("no tracked id names nothing, even with a generated name", func(t *testing.T) {
		fields := dockerContainerLogFields("", "mcpproxy-a-wxyz", "")
		assert.Nil(t, fields, "an unverified generated name must not be treated as evidence")
	})

	t.Run("no tracked id and no generated name names nothing", func(t *testing.T) {
		fields := dockerContainerLogFields("", "", "")
		assert.Nil(t, fields)
	})

	t.Run("tracked id carries id, name and the owner read back", func(t *testing.T) {
		fields := dockerContainerLogFields(ownContainerID, ownContainerName, "a")
		enc := zapcoreEncodeFields(t, fields)
		assert.Equal(t, ownContainerID, enc["container_id"])
		assert.Equal(t, ownContainerName, enc["container_name"])
		assert.Equal(t, "a", enc["container_owner"])
	})
}

// zapcoreEncodeFields renders zap.Field values into a map the way the
// observer would, so a direct-return test of dockerContainerLogFields can
// assert on field values without duplicating zap's internals.
func zapcoreEncodeFields(t *testing.T, fields []zap.Field) map[string]interface{} {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	zap.New(core).Debug("probe", fields...)
	all := logs.All()
	require.Len(t, all, 1)
	return all[0].ContextMap()
}

// failingMCPTransport is a minimal transport.Interface whose SendRequest
// always fails — deterministic and instant, no subprocess needed — so
// c.initialize(ctx) fails the same way a real handshake timeout would,
// without the cost/flakiness of spawning one.
type failingMCPTransport struct{ err error }

func (failingMCPTransport) Start(context.Context) error { return nil }
func (f failingMCPTransport) SendRequest(context.Context, uptransport.JSONRPCRequest) (*uptransport.JSONRPCResponse, error) {
	return nil, f.err
}
func (failingMCPTransport) SendNotification(context.Context, mcp.JSONRPCNotification) error {
	return nil
}
func (failingMCPTransport) SetNotificationHandler(func(mcp.JSONRPCNotification)) {}
func (failingMCPTransport) Close() error                                         { return nil }
func (failingMCPTransport) GetSessionId() string                                 { return "" }

// TestInitializeFailure_DockerCleanupLog is connection_lifecycle.go's
// "Direct initialization failed for Docker command" site: initialize() is
// called directly (not via connectStdio), so this is the "cleanup may be
// handled by caller" comment's own scenario.
func TestInitializeFailure_DockerCleanupLog(t *testing.T) {
	const msg = "Direct initialization failed for Docker command - cleanup may be handled by caller"

	arms := []struct {
		name                                       string
		containerID, containerName, containerOwner string
		verified                                   bool
	}{
		{name: "nothing tracked yet"},
		{name: "only a generated name, never verified", containerName: ownContainerName},
		{name: "tracked and verified", containerID: ownContainerID, containerName: ownContainerName, containerOwner: "a", verified: true},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			c, mainLogs, _ := newOwnershipClient("a", nil)
			c.isDockerCommand = true
			c.containerID = arm.containerID
			c.containerName = arm.containerName
			c.containerOwner = arm.containerOwner
			c.client = mcpclient.NewClient(failingMCPTransport{err: errors.New("boom")})

			require.Error(t, c.initialize(context.Background()))

			records := mainLogs.FilterMessage(msg).All()
			require.NotEmpty(t, records, "expected the Docker cleanup log line")
			for _, entry := range records {
				fields := entry.ContextMap()
				if arm.verified {
					assert.Equal(t, arm.containerID, fields["container_id"])
					assert.Equal(t, arm.containerName, fields["container_name"])
					assert.Equal(t, arm.containerOwner, fields["container_owner"])
					continue
				}
				_, hasID := fields["container_id"]
				_, hasName := fields["container_name"]
				_, hasOwner := fields["container_owner"]
				assert.False(t, hasID, "unverified state must not name a container id")
				assert.False(t, hasName, "unverified state must not name a container name")
				assert.False(t, hasOwner, "unverified state must not name a container owner")
			}
		})
	}
}

// TestDisconnectWithContext_DockerCleanupLog is connection_lifecycle.go's
// disconnect-path pair: "Cleaning up Docker container by ID" (containerID
// tracked and verified) and "Cleaning up Docker container by name"
// (containerID empty, only the GENERATED containerName known).
func TestDisconnectWithContext_DockerCleanupLog(t *testing.T) {
	t.Run("tracked and verified: names the container with its owner", func(t *testing.T) {
		fd := installFakeDocker(t, ownFixtureNamed(ownContainerID, ownContainerName, "a"))
		c, mainLogs, _ := newOwnershipClient("a", nil)
		c.isDockerCommand = true
		c.containerID = ownContainerID
		c.containerName = ownContainerName
		c.containerOwner = "a"

		require.NoError(t, c.DisconnectWithContext(context.Background()))

		records := mainLogs.FilterMessage("Cleaning up Docker container by ID").All()
		require.NotEmpty(t, records)
		for _, entry := range records {
			fields := entry.ContextMap()
			assert.Equal(t, ownContainerID, fields["container_id"])
			assert.Equal(t, "a", fields["container_owner"])
		}
		assert.Contains(t, fd.mutationsOf(t, ownContainerID), "stop "+ownContainerID,
			"the verified container is still cleaned up")
	})

	t.Run("only a generated name: names the server only", func(t *testing.T) {
		fd := installFakeDocker(t, nil)
		c, mainLogs, _ := newOwnershipClient("a", nil)
		c.isDockerCommand = true
		c.containerName = ownContainerName // generated at spawn time; containerID never adopted

		require.NoError(t, c.DisconnectWithContext(context.Background()))

		records := mainLogs.FilterMessage("Cleaning up Docker container by name").All()
		require.NotEmpty(t, records)
		for _, entry := range records {
			fields := entry.ContextMap()
			_, hasName := fields["container_name"]
			_, hasOwner := fields["container_owner"]
			assert.False(t, hasName, "an unverified generated name must not be logged as evidence")
			assert.False(t, hasOwner)
		}
		// killDockerContainerByNameWithContext still re-verifies before acting;
		// nothing in the (empty) fixture is owned, so nothing is mutated.
		assert.Empty(t, fd.invocationsMatching(t, "stop"), "no container was owned, so none should be stopped")
	})
}

// invocationsMatching returns every invocation line starting with verb.
func (fd *fakeDocker) invocationsMatching(t *testing.T, verb string) []string {
	t.Helper()
	var out []string
	for _, line := range fd.invocations(t) {
		if line == verb || len(line) > len(verb) && line[:len(verb)+1] == verb+" " {
			out = append(out, line)
		}
	}
	return out
}

// TestConnectStdioDirectDockerRun_ContainerEvidence drives the REAL Connect
// -> connectStdio -> initialize chain for a direct `docker run` upstream
// (config.Command == "docker"), through the fake docker shim, whose `run`
// verb logs the invocation and exits immediately — no cidfile is ever
// written, so this is the connectStdio/Connect equivalent of "nothing
// tracked yet" for the async cidfile path — while independently proving the
// wiring at connection.go's and connection_stdio.go's Docker cleanup log
// lines fires and, when the client already carries a tracked/verified
// container from an earlier attempt, carries container_owner too.
func TestConnectStdioDirectDockerRun_ContainerEvidence(t *testing.T) {
	sites := []string{
		"Connection failed for Docker command - cleaning up container",
		"Initialization failed for Docker command - cleaning up container",
		"Direct initialization failed for Docker command - cleanup may be handled by caller",
	}

	arms := []struct {
		name     string
		preset   func(c *Client)
		verified bool
	}{
		{name: "nothing tracked yet (real cidfile-less run)", preset: func(*Client) {}},
		{
			name: "already tracked and verified from an earlier attempt",
			preset: func(c *Client) {
				c.containerID = ownContainerID
				c.containerName = ownContainerName
				c.containerOwner = "a"
			},
			verified: true,
		},
	}

	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			installFakeDocker(t, nil)
			shortenCidfilePoll(t)

			cfg := &config.ServerConfig{
				Name:    "a",
				Command: "docker",
				Args:    []string{"run", "-i", "--rm", "mcp/example"},
				Enabled: true,
			}
			mainCore, mainLogs := observer.New(zap.DebugLevel)
			c, err := NewClient("a", cfg, zap.New(mainCore), nil, nil, nil, secret.NewResolver())
			require.NoError(t, err)
			arm.preset(c)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			connectErr := c.Connect(ctx)
			require.Error(t, connectErr, "the fake docker run exits immediately, so the handshake must fail")

			for _, msg := range sites {
				records := mainLogs.FilterMessage(msg).All()
				require.NotEmptyf(t, records, "expected %q to be logged; all records:\n%s", msg, dumpRecords(mainLogs))
				for _, entry := range records {
					fields := entry.ContextMap()
					if arm.verified {
						assert.Equal(t, ownContainerID, fields["container_id"], "%q", msg)
						assert.Equal(t, ownContainerName, fields["container_name"], "%q", msg)
						assert.Equal(t, "a", fields["container_owner"], "%q", msg)
						continue
					}
					_, hasID := fields["container_id"]
					_, hasOwner := fields["container_owner"]
					assert.False(t, hasID, "%q must not name an unverified container id", msg)
					assert.False(t, hasOwner, "%q must not name an unverified container owner", msg)
				}
			}
		})
	}
}

// dumpRecords renders every observed record's message and fields, for a
// require.NotEmptyf failure message.
func dumpRecords(logs *observer.ObservedLogs) string {
	var out string
	for _, e := range logs.All() {
		out += fmt.Sprintf("%s %v\n", e.Message, e.ContextMap())
	}
	return out
}

// TestGetConnectionDiagnostics_ReverifiesOwnershipBeforePublishing is codex
// round 9 finding 2: `docker inspect <id>` resolves purely by id, so
// publishing and inspecting the tracked id directly let a container another
// Docker client relabelled or renamed after tracking still report as this
// server's. GetConnectionDiagnostics must re-verify through
// ContainerMutator.Verify first, the same read+predicate the manager's
// health check (verifyDockerContainerHealthy) already uses.
func TestGetConnectionDiagnostics_ReverifiesOwnershipBeforePublishing(t *testing.T) {
	dockerCfg := &config.ServerConfig{Command: "docker", Args: []string{"run", "-i", "--rm", "mcp/example"}}

	t.Run("relabelled to a co-tenant after tracking: no id, not running", func(t *testing.T) {
		installFakeDocker(t, ownFixtureNamed(ownContainerID, ownContainerName, "a-b"))
		c, _, _ := newOwnershipClient("a", dockerCfg)
		c.isDockerCommand = true
		c.containerID = ownContainerID // tracked before the relabel

		diag := c.GetConnectionDiagnostics()

		_, hasID := diag["container_id"]
		assert.False(t, hasID, "a container relabelled away from this server must not be published as its id")
		_, hasOwner := diag["container_owner"]
		assert.False(t, hasOwner)
		assert.Equal(t, false, diag["container_running"])
	})

	t.Run("renamed to a co-tenant's canonical shape after tracking: no id, not running", func(t *testing.T) {
		installFakeDocker(t, ownFixtureNamed(ownContainerID, foreignContainerName, "a"))
		c, _, _ := newOwnershipClient("a", dockerCfg)
		c.isDockerCommand = true
		c.containerID = ownContainerID

		diag := c.GetConnectionDiagnostics()

		_, hasID := diag["container_id"]
		assert.False(t, hasID)
		assert.Equal(t, false, diag["container_running"])
	})

	t.Run("gone after tracking: no id, not running", func(t *testing.T) {
		installFakeDocker(t, nil)
		c, _, _ := newOwnershipClient("a", dockerCfg)
		c.isDockerCommand = true
		c.containerID = ownContainerID

		diag := c.GetConnectionDiagnostics()

		_, hasID := diag["container_id"]
		assert.False(t, hasID)
		assert.Equal(t, false, diag["container_running"])
	})

	t.Run("unchanged: id and owner published from the verification read", func(t *testing.T) {
		installFakeDocker(t, ownFixtureNamed(ownContainerID, ownContainerName, "a"))
		c, _, _ := newOwnershipClient("a", dockerCfg)
		c.isDockerCommand = true
		c.containerID = ownContainerID

		diag := c.GetConnectionDiagnostics()

		assert.Equal(t, ownContainerID, diag["container_id"])
		assert.Equal(t, "a", diag["container_owner"])
	})
}
