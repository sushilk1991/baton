package config

import "time"

// GCPolicy returns copies of the validated roots, slash globs, and file age.
func (config *Config) GCPolicy() ([]string, []string, time.Duration) {
	age, _ := time.ParseDuration(config.doc.GC.OlderThan)
	return append([]string{}, config.doc.GC.Roots...), append([]string{}, config.doc.GC.Patterns...), age
}
