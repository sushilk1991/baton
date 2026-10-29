//go:build !darwin

package pressure

import "context"

// TotalBytes reads physical memory from the native provider, which needs
// no subprocess on Linux or Windows.
func TotalBytes(ctx context.Context) (uint64, error) {
	sample, err := System().Sample(ctx)
	if err != nil {
		return 0, err
	}
	return sample.TotalBytes, nil
}
