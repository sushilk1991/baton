// Package buildinfo reports the version Go embedded in this binary.
package buildinfo

import "runtime/debug"

const develVersion = "(devel)"

// Version returns the main module's version: v0.1.0 for a release or
// go install @v0.1.0, a pseudo-version for an untagged checkout build,
// and "(devel)" when Go recorded none, as in test binaries.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return develVersion
	}
	return info.Main.Version
}
