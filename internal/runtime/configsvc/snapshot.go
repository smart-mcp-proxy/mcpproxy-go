package configsvc

import (
	"mcpproxy-go/internal/config"
	"time"
)

// Snapshot is an immutable point-in-time view of the configuration.
// It allows lock-free reads without blocking on disk I/O or mutations.
type Snapshot struct {
	Config    *config.Config
	Path      string
	Version   int64 // Monotonically increasing version number
	Timestamp time.Time
}

// Clone creates a deep copy of the configuration to ensure immutability.
func (s *Snapshot) Clone() *config.Config {
	if s.Config == nil {
		return nil
	}

	// Clone the config structure
	cloned := *s.Config

	// Clone server list
	if s.Config.Servers != nil {
		cloned.Servers = make([]*config.ServerConfig, len(s.Config.Servers))
		for i, srv := range s.Config.Servers {
			if srv != nil {
				clonedSrv := *srv

				// Clone maps and slices
				if srv.Headers != nil {
					clonedSrv.Headers = make(map[string]string, len(srv.Headers))
					for k, v := range srv.Headers {
						clonedSrv.Headers[k] = v
					}
				}

				if srv.Env != nil {
					clonedSrv.Env = make(map[string]string, len(srv.Env))
					for k, v := range srv.Env {
						clonedSrv.Env[k] = v
					}
				}

				if srv.Args != nil {
					clonedSrv.Args = make([]string, len(srv.Args))
					copy(clonedSrv.Args, srv.Args)
				}

				// Clone OAuth config if present
				if srv.OAuth != nil {
					oauthClone := *srv.OAuth
					if srv.OAuth.Scopes != nil {
						oauthClone.Scopes = make([]string, len(srv.OAuth.Scopes))
						copy(oauthClone.Scopes, srv.OAuth.Scopes)
					}
					clonedSrv.OAuth = &oauthClone
				}

				// Clone isolation config if present
				if srv.Isolation != nil {
					isolationClone := *srv.Isolation
					clonedSrv.Isolation = &isolationClone
				}

				cloned.Servers[i] = &clonedSrv
			}
		}
	}

	// Clone logging config if present
	if s.Config.Logging != nil {
		loggingClone := *s.Config.Logging
		cloned.Logging = &loggingClone
	}

	// Clone tokenizer config if present
	if s.Config.Tokenizer != nil {
		tokenizerClone := *s.Config.Tokenizer
		cloned.Tokenizer = &tokenizerClone
	}

	// Clone docker isolation config if present
	if s.Config.DockerIsolation != nil {
		dockerClone := *s.Config.DockerIsolation
		if s.Config.DockerIsolation.DefaultImages != nil {
			dockerClone.DefaultImages = make(map[string]string, len(s.Config.DockerIsolation.DefaultImages))
			for k, v := range s.Config.DockerIsolation.DefaultImages {
				dockerClone.DefaultImages[k] = v
			}
		}
		cloned.DockerIsolation = &dockerClone
	}

	return &cloned
}

// GetServer returns a copy of the server configuration by name, or nil if not found.
func (s *Snapshot) GetServer(name string) *config.ServerConfig {
	if s.Config == nil || s.Config.Servers == nil {
		return nil
	}

	for _, srv := range s.Config.Servers {
		if srv != nil && srv.Name == name {
			// Return a cloned copy to maintain immutability
			cloned := *srv

			// Clone maps and slices
			if srv.Headers != nil {
				cloned.Headers = make(map[string]string, len(srv.Headers))
				for k, v := range srv.Headers {
					cloned.Headers[k] = v
				}
			}

			if srv.Env != nil {
				cloned.Env = make(map[string]string, len(srv.Env))
				for k, v := range srv.Env {
					cloned.Env[k] = v
				}
			}

			if srv.Args != nil {
				cloned.Args = make([]string, len(srv.Args))
				copy(cloned.Args, srv.Args)
			}

			return &cloned
		}
	}

	return nil
}

// ServerNames returns a list of all server names in this snapshot.
func (s *Snapshot) ServerNames() []string {
	if s.Config == nil || s.Config.Servers == nil {
		return []string{}
	}

	names := make([]string, 0, len(s.Config.Servers))
	for _, srv := range s.Config.Servers {
		if srv != nil {
			names = append(names, srv.Name)
		}
	}
	return names
}

// ServerCount returns the number of servers in this snapshot.
func (s *Snapshot) ServerCount() int {
	if s.Config == nil || s.Config.Servers == nil {
		return 0
	}
	return len(s.Config.Servers)
}
