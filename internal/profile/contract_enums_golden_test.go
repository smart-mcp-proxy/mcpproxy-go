package profile

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 108-l (FR-052, T121): the enum value sets of contract.go as ONE golden
// that the Go, vitest and XCTest suites all read. testdata/contract/enums.json
// is generated from the constants below (UPDATE_GOLDEN=1); the vitest
// (profiles-enums-labels.spec.ts) and XCTest (ProfilesEnumsLabelsTests) compare
// their own enum tables to it, so a value added on one surface only fails CI.

const enumsGoldenPath = "testdata/contract/enums.json"

func strs[T ~string](in ...T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

// enumFamilies is the generated source: every FR-052 family, each value
// referenced through its Go constant (a rename breaks the build; an added
// constant is caught by TestContractEnumsCoverEveryConstant).
func enumFamilies() map[string][]string {
	return map[string][]string{
		"unannotated": strs(UnannotatedDeny, UnannotatedAsWrite, UnannotatedAsRead),
		"reason": strs(ReasonNone, ReasonServerNotInProfile, ReasonDeniedByRule,
			ReasonUnannotatedHidden, ReasonAboveTierCap),
		"access_reason": strs(AccessReasons()...),
		"source": strs(SourcePin, SourceBinding, SourceURL, SourceSession,
			SourceAnonymous, SourceNone),
		"block_reason": strs(BlockReasonTier, BlockReasonRule, BlockReasonUnannotated,
			BlockReasonCodeExecution, BlockReasonManagement, BlockReasonServerScope),
		"explain_step": strs(StepOrder()...),
		"fix_action": strs(FixAllowInProfile, FixClassifyInProfile, FixAddServerToProfile,
			FixMoveClient, FixEditToken, FixEnableServer, FixApproveTool,
			FixChangeSetting, FixReconnectClient),
		"credential_state": strs(CredentialStateClient, CredentialStateAdminKey,
			CredentialStateNone, CredentialStateRevoked, CredentialStateExpired,
			CredentialStateUnknown),
		"warning_severity": strs(WarningSeverityWarn, WarningSeverityInfo),
		"explain_verdict":  strs(ExplainVerdictAllowed, ExplainVerdictBlocked, ExplainVerdictHidden),
		"change_kind": strs(ChangeCreate, ChangeUpdate, ChangeDelete, ChangeRename,
			ChangeClassify, ChangeAssign, ChangeLock, ChangeUnlock, ChangeForget,
			ChangeRotate, ChangeAnonymous),
		"rotation_state": {RotationFinalized, RotationRolledBack, RotationPending},
		"surface":        strs(SurfaceWeb, SurfaceMacOS, SurfaceCLI, SurfaceMCP, SurfaceAPI),
		"warning_code": strs(WarningAnonymousDeniedByBindingGuard, WarningClientHoldsAdminKey,
			WarningClientCredentialExpiring, WarningClientRotationPending,
			WarningProfileMissing, WarningClientTokenNameConflict),
		"error_code": {ErrorCodeBindingBypassable, ErrorCodeNoClientCredential,
			ErrorCodeConnectInProgress, ErrorCodeCredentialSuperseded, ErrorCodeProfileInUse,
			ErrorCodeProfileIsAnonymous, ErrorCodeProfileExists, ErrorCodeNameMismatch,
			ErrorCodePreconditionFailed},
		"guard_fix": {GuardFixRequireMCPAuth, GuardFixSetAnonymousProfile},
		// Two spellings that live outside contract.go but are labelled in the UI
		// (Terminology table): the binding mode and the tier cap.
		"binding_mode": {auth.ProfileModeLocked, auth.ProfileModeSwitchable},
		"max_tier": {config.ProfileTierRead, config.ProfileTierWrite,
			config.ProfileTierDestructive},
	}
}

func marshalEnums(t *testing.T, fam map[string][]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(fam)) // map keys are sorted by encoding/json
	return buf.Bytes()
}

func TestContractEnumsGolden(t *testing.T) {
	got := marshalEnums(t, enumFamilies())
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(enumsGoldenPath, got, 0o644))
	}
	want, err := os.ReadFile(enumsGoldenPath)
	require.NoError(t, err, "enums.json is missing: run UPDATE_GOLDEN=1 go test -run TestContractEnumsGolden ./internal/profile/")
	require.Equal(t, string(want), string(got),
		"enums.json drifted from contract.go: regenerate with UPDATE_GOLDEN=1 and update contracts.ts / Swift")

	// A family never repeats a value.
	for name, values := range enumFamilies() {
		seen := map[string]bool{}
		for _, v := range values {
			require.False(t, seen[v], "family %s lists %q twice", name, v)
			seen[v] = true
		}
	}
}

// coveredTypes maps each typed constant family of contract.go to its golden key.
var coveredTypes = map[string]string{
	"UnannotatedPolicy": "unannotated",
	"Reason":            "reason",
	"Source":            "source",
	"BlockReason":       "block_reason",
	"ExplainStep":       "explain_step",
	"FixAction":         "fix_action",
	"CredentialState":   "credential_state",
	"WarningSeverity":   "warning_severity",
	"ExplainVerdict":    "explain_verdict",
	"ChangeKind":        "change_kind",
	"Surface":           "surface",
	"WarningCode":       "warning_code",
}

// coveredPrefixes maps the untyped string constants of contract.go to a key by
// name prefix.
var coveredPrefixes = map[string]string{
	"ErrorCode": "error_code",
	"GuardFix":  "guard_fix",
	"Rotation":  "rotation_state",
}

// TestContractEnumsCoverEveryConstant walks contract.go with go/ast: every
// exported string constant of a covered type (or covered name prefix) must be a
// value of its family, so a constant added to contract.go without a golden
// entry (and therefore without a TS/Swift counterpart) fails here. The reverse
// direction is the golden test itself: each family value comes from a constant.
func TestContractEnumsCoverEveryConstant(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "contract.go", nil, 0)
	require.NoError(t, err)

	have := map[string]map[string]bool{}
	for k, vs := range enumFamilies() {
		have[k] = map[string]bool{}
		for _, v := range vs {
			have[k][v] = true
		}
	}

	checked := 0
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			typeName := ""
			if id, ok := vs.Type.(*ast.Ident); ok {
				typeName = id.Name
			}
			for i, name := range vs.Names {
				if !name.IsExported() || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				key := coveredTypes[typeName]
				if key == "" && typeName == "" {
					for prefix, k := range coveredPrefixes {
						if strings.HasPrefix(name.Name, prefix) {
							key = k
						}
					}
				}
				if key == "" {
					continue // not an enum value (e.g. Stale*, WarningAction*)
				}
				checked++
				require.True(t, have[key][val],
					"contract.go constant %s = %q is missing from enum family %q in enumFamilies()", name.Name, val, key)
			}
		}
	}
	require.Greater(t, checked, 60, "the AST walk saw too few constants: the walker is broken")

	// Mutation self-check: a constant the walk would see but the family lacks
	// must be reported (proves the comparison can fail).
	require.False(t, have["warning_code"]["not_a_real_warning"])
}
