package profile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 108-f: the wire spellings the profiles REST surface adds. They are the
// values the generated frontend types and the Swift models read, so a rename
// here is a contract change.
func TestContract_ProfilesRESTEnums(t *testing.T) {
	require.Equal(t, "profile_in_use", ErrorCodeProfileInUse)
	require.Equal(t, "profile_is_anonymous_profile", ErrorCodeProfileIsAnonymous)
	require.Equal(t, "profile_exists", ErrorCodeProfileExists)
	require.Equal(t, "name_mismatch", ErrorCodeNameMismatch)
	require.Equal(t, "precondition_failed", ErrorCodePreconditionFailed)

	require.Equal(t, WarningSeverity("warn"), WarningSeverityWarn)
	require.Equal(t, WarningSeverity("info"), WarningSeverityInfo)
	require.Equal(t, "upgrade_admin_key_holders", WarningActionUpgradeAdminKeyHolders)

	require.Equal(t, ExplainVerdict("allowed"), ExplainVerdictAllowed)
	require.Equal(t, ExplainVerdict("blocked"), ExplainVerdictBlocked)
	require.Equal(t, ExplainVerdict("hidden"), ExplainVerdictHidden)

	require.Equal(t, AccessSubjectKind("client"), AccessSubjectClient)
	require.Equal(t, AccessSubjectKind("token"), AccessSubjectToken)
	require.Equal(t, AccessSubjectKind("profile"), AccessSubjectProfile)
	require.Equal(t, AccessSubjectKind("anonymous"), AccessSubjectAnonymous)

	require.Equal(t, AccessStepStatus("pass"), AccessStepPass)
	require.Equal(t, AccessStepStatus("fail"), AccessStepFail)
	require.Equal(t, AccessStepStatus("skip"), AccessStepSkip)
}
