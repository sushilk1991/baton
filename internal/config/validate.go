package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func positiveDuration(key, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s: expected a positive duration, got %q", key, value)
	}
	return duration, nil
}

func validateExtras(doc document) error {
	for _, entry := range []struct{ key, value string }{
		{"reap.older_than", doc.Reap.OlderThan},
		{"reap.grace_period", doc.Reap.GracePeriod},
		{"gc.older_than", doc.GC.OlderThan},
		{"browser.idle_timeout", doc.Browser.IdleTimeout},
	} {
		if _, err := positiveDuration(entry.key, entry.value); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(doc.Cache.Dir) {
		return fmt.Errorf("cache.dir must be absolute, got %q", doc.Cache.Dir)
	}
	if _, err := ParseBytes(doc.Cache.MaxSize); err != nil {
		return fmt.Errorf("cache.max_size: %w", err)
	}
	for _, root := range doc.GC.Roots {
		if !filepath.IsAbs(root) {
			return fmt.Errorf("gc.roots must be absolute, got %q", root)
		}
	}
	for _, pattern := range doc.GC.Patterns {
		if pattern == "" || filepath.IsAbs(pattern) || strings.Contains(pattern, `\`) {
			return fmt.Errorf("gc.patterns must be nonempty relative slash patterns, got %q", pattern)
		}
		for _, part := range strings.Split(pattern, "/") {
			if part == ".." {
				return fmt.Errorf("gc.patterns cannot leave roots, got %q", pattern)
			}
		}
		if _, err := filepath.Match(pattern, ""); err != nil {
			return fmt.Errorf("gc.patterns: %w", err)
		}
	}
	for _, command := range doc.Shims.Commands {
		if !validCommand(command) {
			return fmt.Errorf("shims.commands: invalid command %q", command)
		}
	}
	for command, patterns := range doc.Shims.FastPaths {
		if !validCommand(command) {
			return fmt.Errorf("shims.fast_paths: invalid command %q", command)
		}
		if err := validateRegex("shims.fast_paths."+command, patterns); err != nil {
			return err
		}
	}
	if err := validateRegex("reap.matchers", doc.Reap.Matchers); err != nil {
		return err
	}
	for _, name := range doc.Cache.Env {
		if !envName.MatchString(name) {
			return fmt.Errorf("cache.env: invalid environment variable %q", name)
		}
	}
	if doc.Browser.Executable != "" && !filepath.IsAbs(doc.Browser.Executable) {
		return fmt.Errorf("browser.executable must be absolute or empty")
	}
	return nil
}

func validCommand(command string) bool {
	return command != "" && command != "." && command != ".." && !strings.ContainsAny(command, "/\\\x00\r\n\t ")
}

func validateRegex(key string, patterns []string) error {
	for _, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}
