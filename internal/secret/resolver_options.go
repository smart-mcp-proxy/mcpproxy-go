package secret

// ResolveScope selects which guards apply when resolving secret references.
type ResolveScope int

const (
	// ScopeProxy resolves proxy-owned fields (api_key on load, data_dir,
	// server-edition OAuth secrets). secret_file_dirs applies; the MCPPROXY_
	// environment guard does not.
	ScopeProxy ResolveScope = iota
	// ScopeUpstream resolves upstream server entry fields. secret_file_dirs,
	// MCPROXY_ environment guard and proxy data deny paths all apply.
	ScopeUpstream
)

// DenyPaths lists resolved filesystem paths upstream ${file:} reads must never
// open, even in wildcard mode.
type DenyPaths struct {
	ConfigFile       string
	DataDir          string
	DataSecretsDir   string
	AdminKeyFiles    []string
}

// FilePolicy governs ${file:} resolution for one resolver.
type FilePolicy struct {
	// Wildcard disables the allow-list (compatibility mode). Deny paths still
	// apply for ScopeUpstream.
	Wildcard bool
	// Disabled refuses every ${file:} reference (explicit secret_file_dirs: []).
	Disabled bool
	// AllowedRoots are absolute, symlink-resolved directory roots.
	AllowedRoots []string
	Scope        ResolveScope
	Deny         DenyPaths
}

// ResolverOptions configures a production resolver.
type ResolverOptions struct {
	Scope      ResolveScope
	FilePolicy *FilePolicy
}
