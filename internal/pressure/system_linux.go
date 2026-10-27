package pressure

import "os"

// System returns the native memory provider without sampling it.
func System() Provider {
	return linuxProvider{read: os.ReadFile}
}
