package pressure

import "fmt"

const binaryUnit = 1024

var binaryPrefixes = []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// FormatBytes renders a memory figure for people in binary units with one
// decimal, the same units --mem accepts: 512 B, 1.1 MiB, 9.8 GiB.
func FormatBytes(value uint64) string {
	if value < binaryUnit {
		return fmt.Sprintf("%d B", value)
	}
	scaled, prefix := float64(value)/binaryUnit, 0
	for scaled >= binaryUnit && prefix < len(binaryPrefixes)-1 {
		scaled /= binaryUnit
		prefix++
	}
	return fmt.Sprintf("%.1f %s", scaled, binaryPrefixes[prefix])
}
