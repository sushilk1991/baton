package config

import (
	"regexp"
	"strings"
)

// ShimCommands returns a detached list of the configured default shims.
func (config *Config) ShimCommands() []string {
	return append([]string{}, config.doc.Shims.Commands...)
}

// ShimBypass matches command arguments against validated fast-path rules.
func (config *Config) ShimBypass(command string, args []string) bool {
	joined := strings.Join(args, " ")
	for _, pattern := range config.doc.Shims.FastPaths[command] {
		// Load already validates every pattern; source policy stays private.
		if regexp.MustCompile(pattern).MatchString(joined) {
			return true
		}
	}
	return false
}
