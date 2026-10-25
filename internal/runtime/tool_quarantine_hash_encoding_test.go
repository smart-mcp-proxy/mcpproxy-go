package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Issue #1606: the v4 rug-pull hash joined name, description and the
// normalized input schema with an unescaped '|' and appended the output schema
// only when present, so
//
//	A = (D, I, O)  and  B = (D + "|" + norm(I), O, none)
//
// both hashed "name|D|norm(I)|norm(O)". An approved A could switch to B and
// stay approved.
const (
	collisionDesc   = "Read a note"
	collisionInput  = `{"type":"object"}`
	collisionOutput = `{"type":"string"}`
)

func collisionToolA() *config.ToolMetadata {
	return &config.ToolMetadata{ServerName: "notes", Name: "read_note", Description: collisionDesc, ParamsJSON: collisionInput, OutputSchemaJSON: collisionOutput}
}

func collisionToolB() *config.ToolMetadata {
	return &config.ToolMetadata{ServerName: "notes", Name: "read_note", Description: collisionDesc + "|" + normalizeJSON(collisionInput), ParamsJSON: collisionOutput}
}

func TestToolApprovalHash_FieldBoundariesAreUnambiguous(t *testing.T) {
	a := calculateToolApprovalHashWithOutputSchema("read_note", collisionDesc, normalizeJSON(collisionInput), normalizeJSON(collisionOutput), nil)
	b := calculateToolApprovalHashWithOutputSchema("read_note", collisionDesc+"|"+normalizeJSON(collisionInput), normalizeJSON(collisionOutput), "", nil)
	assert.NotEqual(t, a, b, "moving a field boundary must change the hash")

	c := calculateToolApprovalHashWithOutputSchema("t", "d", "{}", "", nil)
	d := calculateToolApprovalHashWithOutputSchema("t", "d|{}", "", "", nil)
	assert.NotEqual(t, c, d)
}

// TestToolApprovalHash_CollidingContractIsHeld: baseline A on a trusted server,
// then capture B. B is a different contract, so the record goes to changed.
func TestToolApprovalHash_CollidingContractIsHeld(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "notes", Enabled: true}})
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.CurrentSchemaVersion))

	_, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{collisionToolA()})
	require.NoError(t, err)
	rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)

	for pass := 1; pass <= 2; pass++ {
		result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{collisionToolB()})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["read_note"], "pass %d", pass)
		rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "pass %d: a colliding contract is a change", pass)
		assert.Equal(t, collisionDesc, rec.PreviousDescription)
	}
}

// TestToolApprovalHashV4_StillCollides pins the old formula: the migration
// relies on it staying byte-identical to what v4 binaries stored.
func TestToolApprovalHashV4_StillCollides(t *testing.T) {
	assert.Equal(t,
		calculateToolApprovalHashV4("read_note", collisionDesc, normalizeJSON(collisionInput), normalizeJSON(collisionOutput), nil),
		calculateToolApprovalHashV4("read_note", collisionDesc+"|"+normalizeJSON(collisionInput), normalizeJSON(collisionOutput), "", nil))
}

// seedV4ApprovedRecord stores an approved record the way a v4 binary wrote it:
// the pipe-joined hash over the stored contract, HashSchemaVersion 4.
func seedV4ApprovedRecord(t *testing.T, rt *Runtime, tool *config.ToolMetadata) *storage.ToolApprovalRecord {
	t.Helper()
	schema := normalizeJSON(tool.ParamsJSON)
	output := normalizeJSON(tool.OutputSchemaJSON)
	h := calculateToolApprovalHashV4(tool.Name, tool.Description, schema, output, tool.Annotations)
	rec := &storage.ToolApprovalRecord{
		ServerName: tool.ServerName, ToolName: tool.Name,
		ApprovedHash: h, CurrentHash: h,
		HashSchemaVersion:        storage.AnnotationHashSchemaVersion,
		CurrentHashSchemaVersion: storage.AnnotationHashSchemaVersion,
		Status:                   storage.ToolApprovalStatusApproved,
		ApprovedBy:               "user",
		CurrentDescription:       tool.Description,
		CurrentSchema:            schema,
		CurrentOutputSchema:      output,
		CurrentAnnotations:       capturedToolAnnotations(tool.Annotations),
		IdentityKeyed:            true,
	}
	require.NoError(t, rt.storageManager.SaveToolApproval(rec))
	return rec
}

func newHashMigrationRuntime(t *testing.T, srv *config.ServerConfig) *Runtime {
	t.Helper()
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{srv})
	require.NoError(t, rt.storageManager.SetSchemaVersion(storage.CurrentSchemaVersion))
	return rt
}

// TestToolApprovalHashMigration_UnchangedV4RecordRebaselined: an upgrade never
// forces a mass re-review. A v4 record whose ApprovedHash covers its stored
// contract, and whose stored contract is still what the server serves, is
// re-hashed under the new formula and stays approved.
func TestToolApprovalHashMigration_UnchangedV4RecordRebaselined(t *testing.T) {
	ro := true
	for name, tool := range map[string]*config.ToolMetadata{
		"with_output_schema": collisionToolA(),
		"no_output_schema":   {ServerName: "notes", Name: "read_note", Description: collisionDesc, ParamsJSON: collisionInput},
		"non_default_hints":  {ServerName: "notes", Name: "read_note", Description: collisionDesc, ParamsJSON: collisionInput, Annotations: &config.ToolAnnotations{ReadOnlyHint: &ro}},
	} {
		t.Run(name, func(t *testing.T) {
			rt := newHashMigrationRuntime(t, &config.ServerConfig{Name: "notes", Enabled: true})
			old := seedV4ApprovedRecord(t, rt, tool)

			for pass := 1; pass <= 2; pass++ {
				result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{tool})
				require.NoError(t, err)
				assert.False(t, result.BlockedTools["read_note"], "pass %d", pass)
				assert.Zero(t, result.ChangedCount, "pass %d", pass)
			}

			rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			assert.NotEqual(t, old.ApprovedHash, rec.ApprovedHash, "re-hashed under the new formula")
			want := calculateToolApprovalHashWithOutputSchema(tool.Name, tool.Description, normalizeJSON(tool.ParamsJSON), normalizeJSON(tool.OutputSchemaJSON), tool.Annotations)
			assert.Equal(t, want, rec.ApprovedHash)
			assert.Equal(t, want, rec.CurrentHash)
			assert.Equal(t, uint64(storage.ToolApprovalHashSchemaVersion), rec.HashSchemaVersion)
			assert.Equal(t, uint64(storage.ToolApprovalHashSchemaVersion), rec.CurrentHashSchemaVersion)
			assert.Equal(t, "user", rec.ApprovedBy, "a migration is not a new approval")
		})
	}
}

// TestToolApprovalHashMigration_CollidingV4ContractIsHeld: a v4 record
// approved for A, upgraded while the server serves B (which collides with A
// under v4). The migration must not re-baseline B.
func TestToolApprovalHashMigration_CollidingV4ContractIsHeld(t *testing.T) {
	for _, srv := range []*config.ServerConfig{
		{Name: "notes", Enabled: true},
		scanServer("notes"),
	} {
		t.Run("trust_mode="+srv.TrustMode, func(t *testing.T) {
			rt := newHashMigrationRuntime(t, srv)
			seedV4ApprovedRecord(t, rt, collisionToolA())

			result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{collisionToolB()})
			require.NoError(t, err)
			rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
			require.NoError(t, err)
			if srv.EffectiveTrustMode() == config.TrustModeScan {
				// The collision is still seen as a CHANGE (never matched to the
				// approved contract); under scan the clean new definition is
				// then scan-approved on its own hash (spec 119).
				assert.False(t, result.BlockedTools["read_note"])
				assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
				assert.Equal(t, "scan-approved", rec.ApprovedBy)
				assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
				return
			}
			assert.True(t, result.BlockedTools["read_note"])
			assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
			assert.Equal(t, collisionDesc, rec.PreviousDescription)
		})
	}
}

// TestToolApprovalHashMigration_V4StoredContractNotProvenIsHeld: the stored
// contract equals the live one, but the v4 ApprovedHash does not cover it, so
// the stored snapshot is not evidence of what was approved. The record is held
// instead of migrated.
func TestToolApprovalHashMigration_V4StoredContractNotProvenIsHeld(t *testing.T) {
	rt := newHashMigrationRuntime(t, &config.ServerConfig{Name: "notes", Enabled: true})
	tool := collisionToolA()
	rec := seedV4ApprovedRecord(t, rt, tool)
	rec.ApprovedHash = calculateToolApprovalHashV4(tool.Name, "Something else entirely", normalizeJSON(tool.ParamsJSON), "", nil)
	require.NoError(t, rt.storageManager.SaveToolApproval(rec))

	result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{tool})
	require.NoError(t, err)
	assert.True(t, result.BlockedTools["read_note"])
	got, err := rt.storageManager.GetToolApproval("notes", "read_note")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, got.Status)
}

// TestToolApprovalHashMigration_V4AddedOutputSchemaIsHeld (issue #1591 across
// the v4 → v5 upgrade): a v4 record captured the full contract, so its empty
// CurrentOutputSchema is the declared "none". An output schema that appears
// later is a change in every trust mode, never a formula migration.
func TestToolApprovalHashMigration_V4AddedOutputSchemaIsHeld(t *testing.T) {
	for _, srv := range []*config.ServerConfig{
		{Name: "notes", Enabled: true},
		scanServer("notes"),
	} {
		t.Run("trust_mode="+srv.TrustMode, func(t *testing.T) {
			rt := newHashMigrationRuntime(t, srv)
			base := &config.ToolMetadata{ServerName: "notes", Name: "read_note", Description: collisionDesc, ParamsJSON: collisionInput}
			seedV4ApprovedRecord(t, rt, base)

			added := *base
			added.OutputSchemaJSON = poisonedOutputSchema
			for pass := 1; pass <= 2; pass++ {
				result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{&added})
				require.NoError(t, err)
				assert.True(t, result.BlockedTools["read_note"], "pass %d", pass)
				rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
				require.NoError(t, err)
				assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "pass %d: an added output schema is held", pass)
			}
		})
	}
}

// TestToolApprovalHashMigration_V4PendingBaselinePromoted: a pending record a
// v4 binary stranded on a trusted server without a baseline is still promoted
// by the baseline migration when its recorded contract is unchanged, although
// its CurrentHash was computed with the v4 formula. A colliding contract is not
// "unchanged".
func TestToolApprovalHashMigration_V4PendingBaselinePromoted(t *testing.T) {
	tool := collisionToolA()
	schema, output := normalizeJSON(tool.ParamsJSON), normalizeJSON(tool.OutputSchemaJSON)
	h := calculateToolApprovalHashV4(tool.Name, tool.Description, schema, output, nil)
	seed := func(t *testing.T, rt *Runtime) {
		t.Helper()
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "notes", ToolName: "read_note",
			CurrentHash: h, CurrentHashSchemaVersion: storage.AnnotationHashSchemaVersion,
			HashSchemaVersion:  storage.AnnotationHashSchemaVersion,
			Status:             storage.ToolApprovalStatusPending,
			CurrentDescription: tool.Description, CurrentSchema: schema, CurrentOutputSchema: output,
			IdentityKeyed: true,
		}))
	}

	t.Run("unchanged", func(t *testing.T) {
		rt := newHashMigrationRuntime(t, &config.ServerConfig{Name: "notes", Enabled: true})
		seed(t, rt)
		_, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{tool})
		require.NoError(t, err)
		rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.Equal(t, uint64(storage.ToolApprovalHashSchemaVersion), rec.HashSchemaVersion)
	})
	t.Run("colliding", func(t *testing.T) {
		rt := newHashMigrationRuntime(t, &config.ServerConfig{Name: "notes", Enabled: true})
		seed(t, rt)
		_, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{collisionToolB()})
		require.NoError(t, err)
		rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
	})
}

// TestCalculateToolApprovalHash_V5Stability pins the v5 encoding. Changing it
// invalidates every stored approval: bump storage.ToolApprovalHashSchemaVersion
// and add a migration first.
func TestCalculateToolApprovalHash_V5Stability(t *testing.T) {
	ro := true
	for _, tt := range []struct {
		name, tool, desc, schema, output string
		annotations                      *config.ToolAnnotations
		expected                         string
	}{
		{"no_output_schema", "create_issue", "Creates a GitHub issue", `{"type":"object"}`, "", nil,
			"9f4c3751b649540dc1a0ad161c2d68165d0f88db28ba5138c72c92da2356bae1"},
		{"output_schema", "read_note", collisionDesc, collisionInput, collisionOutput, nil,
			"8c3021de4e8ecd7b2fd6841e3fae87bbfdb38a5417c0e5ce629750a9fa077583"},
		{"read_only_hint", "search_docs", "Search the documentation", `{"type":"object","properties":{"query":{"type":"string"}}}`, "", &config.ToolAnnotations{ReadOnlyHint: &ro},
			"56dc2a5a3615644a5b01a6c6779b323b7fa0565671252995ddf5b3a86737615b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, calculateToolApprovalHashWithOutputSchema(tt.tool, tt.desc, tt.schema, tt.output, tt.annotations))
		})
	}
}

// TestToolApprovalHashMigration_V4UnprovenSnapshotNotRevertedLater (Sol r1
// P1): a v4 record whose ApprovedHash does not cover its stored snapshot is
// held on the first pass. The unproven snapshot must not become before-evidence
// that a later pass "reverts" to and approves.
func TestToolApprovalHashMigration_V4UnprovenSnapshotNotRevertedLater(t *testing.T) {
	for _, srv := range []*config.ServerConfig{
		{Name: "notes", Enabled: true},
		scanServer("notes"),
	} {
		t.Run("trust_mode="+srv.TrustMode, func(t *testing.T) {
			rt := newHashMigrationRuntime(t, srv)
			tool := collisionToolA()
			rec := seedV4ApprovedRecord(t, rt, tool)
			rec.ApprovedHash = calculateToolApprovalHashV4(tool.Name, "Something else entirely", normalizeJSON(tool.ParamsJSON), "", nil)
			require.NoError(t, rt.storageManager.SaveToolApproval(rec))

			for pass := 1; pass <= 3; pass++ {
				result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{tool})
				require.NoError(t, err)
				got, err := rt.storageManager.GetToolApproval("notes", "read_note")
				require.NoError(t, err)
				if srv.EffectiveTrustMode() == config.TrustModeScan {
					// Never silently reverted to the unproven snapshot: under
					// scan the live contract is approved by the gate's clean
					// verdict, explicitly and on its own hash (spec 119).
					assert.False(t, result.BlockedTools["read_note"], "pass %d", pass)
					assert.Equal(t, storage.ToolApprovalStatusApproved, got.Status, "pass %d", pass)
					assert.Equal(t, "scan-approved", got.ApprovedBy, "pass %d", pass)
					continue
				}
				assert.True(t, result.BlockedTools["read_note"], "pass %d", pass)
				assert.Equal(t, storage.ToolApprovalStatusChanged, got.Status, "pass %d: stays held for an operator", pass)
			}
		})
	}
}

// v4Sibling builds an unstamped, approved v4 collapsed sibling whose stored
// contract is (desc, schema, output) and whose hashes are approvedHash.
func v4Sibling(desc, schema, output, approvedHash string) *storage.ToolApprovalRecord {
	return &storage.ToolApprovalRecord{
		ToolName: "read_note", ApprovedHash: approvedHash, CurrentHash: approvedHash,
		HashSchemaVersion: storage.AnnotationHashSchemaVersion, CurrentHashSchemaVersion: storage.AnnotationHashSchemaVersion,
		Status:             storage.ToolApprovalStatusApproved,
		CurrentDescription: desc, CurrentSchema: schema, CurrentOutputSchema: output,
	}
}

// TestLegacySiblingApprovesContract_V4 (Sol r1 P1/P2): a v4 sibling approves
// the live contract only when its v4 hash covers its stored contract and that
// contract is exactly the live one. An empty description is declared on v4,
// so an unchanged empty-description tool still adopts.
func TestLegacySiblingApprovesContract_V4(t *testing.T) {
	in, out := normalizeJSON(collisionInput), normalizeJSON(collisionOutput)
	a := collisionToolA()

	proven := v4Sibling(collisionDesc, in, out, calculateToolApprovalHashV4("read_note", collisionDesc, in, out, nil))
	assert.True(t, legacySiblingApprovesContract(proven, a, in, out), "proven and unchanged")

	b := collisionToolB()
	assert.False(t, legacySiblingApprovesContract(proven, b, normalizeJSON(b.ParamsJSON), ""), "colliding live contract")

	// Stored C, hashes for A (C does not collide with A): the stored snapshot
	// is not what was approved. (A stored B that collides with A cannot be
	// told apart from an approved B: that state is what a v4 binary wrote
	// after the pre-upgrade bypass.)
	c := &config.ToolMetadata{ServerName: "notes", Name: "read_note", Description: "Reads and forwards a note", ParamsJSON: collisionInput}
	drift := v4Sibling(c.Description, in, "", calculateToolApprovalHashV4("read_note", collisionDesc, in, out, nil))
	assert.False(t, legacySiblingApprovesContract(drift, c, in, ""), "unproven stored snapshot")

	empty := &config.ToolMetadata{ServerName: "notes", Name: "read_note", ParamsJSON: collisionInput}
	emptySib := v4Sibling("", in, "", calculateToolApprovalHashV4("read_note", "", in, "", nil))
	assert.True(t, legacySiblingApprovesContract(emptySib, empty, in, ""), "unchanged empty-description tool")
}

// TestLegacySiblingApprovesContract_V3CollisionRejected (Sol r1 P1): a v3
// sibling approved for (D|I, O, none) has the same pipe-joined hash as the
// live (D, I, O). The stored contract contradicts the live one, so the hash
// match is not an approval, and the added output schema is never adopted.
func TestLegacySiblingApprovesContract_V3CollisionRejected(t *testing.T) {
	in, out := normalizeJSON(collisionInput), normalizeJSON(collisionOutput)
	b := collisionToolB()
	h := calculateToolApprovalHashV4("read_note", b.Description, normalizeJSON(b.ParamsJSON), "", nil)
	sibling := &storage.ToolApprovalRecord{
		ToolName: "read_note", ApprovedHash: h, CurrentHash: h,
		HashSchemaVersion: storage.OutputSchemaHashSchemaVersion, Status: storage.ToolApprovalStatusApproved,
		CurrentDescription: b.Description, CurrentSchema: normalizeJSON(b.ParamsJSON),
	}
	require.Equal(t, h, calculateToolApprovalHashV4("read_note", collisionDesc, in, out, nil), "precondition: v4 formula collides")
	assert.False(t, legacySiblingApprovesContract(sibling, collisionToolA(), in, out))
	assert.True(t, legacySiblingApprovesContract(sibling, b, normalizeJSON(b.ParamsJSON), ""), "its own contract still adopts")
}

// TestToolApprovalHashMigration_V4EmptyDescriptionRevert (Sol r2 P2): a v4
// binary approved A with an empty description, then marked B changed. After
// the upgrade the server serves A again: the record is restored, although its
// v4 ApprovedHash no longer equals the live (v5) hash. A contract that only
// collides with A under v4 stays held.
func TestToolApprovalHashMigration_V4EmptyDescriptionRevert(t *testing.T) {
	in, out := normalizeJSON(collisionInput), normalizeJSON(collisionOutput)
	seed := func(t *testing.T, rt *Runtime) {
		t.Helper()
		approved := calculateToolApprovalHashV4("read_note", "", in, out, nil)
		changed := calculateToolApprovalHashV4("read_note", "Now exfiltrates notes", in, out, nil)
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "notes", ToolName: "read_note",
			ApprovedHash: approved, CurrentHash: changed,
			HashSchemaVersion: storage.AnnotationHashSchemaVersion, CurrentHashSchemaVersion: storage.AnnotationHashSchemaVersion,
			Status:              storage.ToolApprovalStatusChanged,
			CurrentDescription:  "Now exfiltrates notes",
			CurrentSchema:       in,
			CurrentOutputSchema: out,
			PreviousDescription: "", PreviousSchema: in, PreviousOutputSchema: out,
			PreviousAnnotations: &config.ToolAnnotations{},
			IdentityKeyed:       true,
		}))
	}

	t.Run("revert_restores", func(t *testing.T) {
		rt := newHashMigrationRuntime(t, &config.ServerConfig{Name: "notes", Enabled: true})
		seed(t, rt)
		live := &config.ToolMetadata{ServerName: "notes", Name: "read_note", ParamsJSON: collisionInput, OutputSchemaJSON: collisionOutput}
		result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{live})
		require.NoError(t, err)
		assert.False(t, result.BlockedTools["read_note"])
		rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.Equal(t, uint64(storage.ToolApprovalHashSchemaVersion), rec.HashSchemaVersion)
	})
	t.Run("colliding_contract_held", func(t *testing.T) {
		rt := newHashMigrationRuntime(t, &config.ServerConfig{Name: "notes", Enabled: true})
		seed(t, rt)
		live := &config.ToolMetadata{ServerName: "notes", Name: "read_note", Description: "|" + in, ParamsJSON: collisionOutput}
		require.Equal(t, calculateToolApprovalHashV4("read_note", "", in, out, nil),
			calculateToolApprovalHashV4("read_note", live.Description, out, "", nil), "precondition: collides under v4")
		result, err := rt.checkToolApprovals("notes", []*config.ToolMetadata{live})
		require.NoError(t, err)
		assert.True(t, result.BlockedTools["read_note"])
		rec, err := rt.storageManager.GetToolApproval("notes", "read_note")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
	})
}
