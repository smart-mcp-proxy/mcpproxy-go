package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActivityFilter_ExcludeAdminOnlyTypes(t *testing.T) {
	skill := &ActivityRecord{Type: ActivityTypeSkillQuarantineChange, ServerName: "acme"}
	tool := &ActivityRecord{Type: ActivityTypeToolQuarantineChange, ServerName: "acme"}

	open := ActivityFilter{}
	assert.True(t, open.Matches(skill))
	assert.True(t, open.Matches(tool))

	scoped := ActivityFilter{AllowedServers: []string{"acme"}, ExcludeAdminOnlyTypes: true}
	assert.False(t, scoped.Matches(skill), "an administrator-only row is not matched, even on an entitled server")
	assert.True(t, scoped.Matches(tool))

	assert.True(t, IsAdminOnlyActivityType(ActivityTypeSkillQuarantineChange))
	assert.False(t, IsAdminOnlyActivityType(ActivityTypeToolQuarantineChange))
	assert.Contains(t, ValidActivityTypes, string(ActivityTypeSkillQuarantineChange))
}
