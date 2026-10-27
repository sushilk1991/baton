package config

// CachePolicy returns the cache directory, byte limit, and detached env allowlist.
func (config *Config) CachePolicy() (string, uint64, []string) {
	limit, _ := ParseBytes(config.doc.Cache.MaxSize)
	return config.doc.Cache.Dir, limit, append([]string{}, config.doc.Cache.Env...)
}
