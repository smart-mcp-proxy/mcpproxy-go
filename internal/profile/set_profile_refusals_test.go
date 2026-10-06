package profile

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetProfileRefusalGolden(t *testing.T) {
	var golden map[string]string
	require.NoError(t, json.Unmarshal(readFixture(t, "set_profile_refusals.json"), &golden))

	require.Equal(t, strings.ReplaceAll(golden["locked"], "<p>", "%s"), SetProfileLockedRefusalFormat)
	require.Equal(t, strings.ReplaceAll(golden["switchable"], "<p>", "%s"), SetProfileNotSwitchableRefusalFormat)
	require.Equal(t, "cannot switch to profile 'x': this client's profile is locked", fmt.Sprintf(SetProfileLockedRefusalFormat, "x"))
}
