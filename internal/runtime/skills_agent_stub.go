package runtime

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"

// STUB: replaced by the runtime half

// SkillReach reports whether a server is within the caller's reach. nil means
// every server.
type SkillReach func(serverName string) bool

// AvailableSkill is a skill an agent may read: the approved copy.
type AvailableSkill struct {
	ID, Server, Path, URI, Name, Description, ApprovedHash string
	Files                                                  []storage.SkillFile
	TotalSize                                              int64
}

// AvailableSkill returns ErrSkillNotFound for every reason a skill is not
// available.
func (r *Runtime) AvailableSkill(serverName, skillPath string) (*AvailableSkill, error) {
	return nil, ErrSkillNotFound
}

// AvailableSkillFile returns the approved copy of one file.
func (r *Runtime) AvailableSkillFile(serverName, skillPath, relPath string) ([]byte, storage.SkillFile, *AvailableSkill, error) {
	return nil, storage.SkillFile{}, nil, ErrSkillNotFound
}

// SearchAvailableSkills searches the skills available to one caller.
func (r *Runtime) SearchAvailableSkills(query string, limit int, reach SkillReach) ([]AvailableSkill, int, error) {
	return nil, 0, nil
}
