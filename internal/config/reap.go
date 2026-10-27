package config

import (
	"regexp"
	"strings"
	"time"
)

// ReapTiming returns the validated minimum age and termination grace period.
func (config *Config) ReapTiming() (time.Duration, time.Duration) {
	older, _ := time.ParseDuration(config.doc.Reap.OlderThan)
	grace, _ := time.ParseDuration(config.doc.Reap.GracePeriod)
	return older, grace
}

// ReapMatch restricts eligible heavy processes using the configured regexes.
func (config *Config) ReapMatch(args []string) bool {
	joined := strings.Join(args, " ")
	for _, pattern := range config.doc.Reap.Matchers {
		if regexp.MustCompile(pattern).MatchString(joined) {
			return true
		}
	}
	return false
}
