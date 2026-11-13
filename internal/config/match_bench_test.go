package config

import "testing"

func BenchmarkPolicyMatchers(b *testing.B) {
	settings, err := compile(defaults(1, gib, Paths{CacheDir: b.TempDir()}))
	if err != nil {
		b.Fatal(err)
	}
	args := []string{"ffmpeg", "-i", "in.mp4", "out.mp4"}
	for _, tc := range []struct {
		name  string
		match func() bool
		want  bool
	}{
		{"reap", func() bool { return settings.ReapMatch(args) }, true},
		{"shim", func() bool { return settings.ShimBypass("ffmpeg", args[1:]) }, false},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				if tc.match() != tc.want {
					b.Fatal("matcher result changed")
				}
			}
		})
	}
}
