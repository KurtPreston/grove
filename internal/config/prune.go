package config

// PruneScope returns the configured scope, defaulting to all branches. The
// command validates it after applying the CLI override.
func (c Config) PruneScope() string {
	if c.Prune == nil || c.Prune.Scope == nil {
		return "all"
	}
	return *c.Prune.Scope
}
