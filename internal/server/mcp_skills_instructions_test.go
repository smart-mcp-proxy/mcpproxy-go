package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The default instructions name the skill tools only for a caller that sees
// them: with enable_skills off the text is what it was before the feature.
func TestComposeDefaultInstructions_SkillsClauseFollowsTheTool(t *testing.T) {
	base := map[string]bool{"retrieve_tools": true, "call_tool_read": true, "upstream_servers": true}
	off := composeDefaultInstructions(base)
	assert.NotContains(t, off, "retrieve_skills")
	assert.NotContains(t, off, "SKILLS:")

	with := map[string]bool{"retrieve_tools": true, "call_tool_read": true, "upstream_servers": true, toolRetrieveSkills: true}
	on := composeDefaultInstructions(with)
	assert.Contains(t, on, "'retrieve_skills'")
	assert.Contains(t, on, "'load_skill'")
	assert.Contains(t, on, "untrusted guidance")
	assert.Equal(t, off, removeOnce(on, instrSkills), "the clause is the only difference")
}

func removeOnce(s, part string) string {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return s[:i] + s[i+len(part):]
		}
	}
	return s
}
