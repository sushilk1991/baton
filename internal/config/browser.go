package config

import "time"

// BrowserPolicy returns executable discovery, extra flags, and idle lifetime.
func (config *Config) BrowserPolicy() (string, []string, time.Duration) {
	idle, _ := time.ParseDuration(config.doc.Browser.IdleTimeout)
	return config.doc.Browser.Executable, append([]string{}, config.doc.Browser.Flags...), idle
}
