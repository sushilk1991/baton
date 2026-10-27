package config

import "testing"

func TestShimPolicy(t *testing.T) {
	settings, err := compile(defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	commands := settings.ShimCommands()
	commands[0] = "changed"
	if settings.ShimCommands()[0] != "ffmpeg" {
		t.Fatal("caller mutated config")
	}
	for _, test := range []struct {
		command string
		args    []string
		want    bool
	}{
		{"ffprobe", nil, true}, {"ffprobe", []string{"movie"}, true},
		{"ffmpeg", []string{"-version"}, true}, {"ffmpeg", []string{"-f", "null", "-"}, true},
		{"ffmpeg", []string{"-i", "movie", "out.mp4"}, false},
		{"ffmpeg", []string{"-format", "nullify"}, false}, {"npm", []string{"install"}, false},
	} {
		if got := settings.ShimBypass(test.command, test.args); got != test.want {
			t.Errorf("%s %v: %t, want %t", test.command, test.args, got, test.want)
		}
	}
	override, err := decode("[shims.fast_paths]\nffmpeg = ['^probe$']\n", defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	if override.ShimBypass("ffmpeg", []string{"-version"}) || !override.ShimBypass("ffmpeg", []string{"probe"}) {
		t.Fatal("fast-path override ignored")
	}
}
