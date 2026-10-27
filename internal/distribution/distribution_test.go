//go:build !windows

package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureProgram = "#!/bin/sh\nprintf 'baton fixture\\n'\n"

// Exercise the piped installer on every supported Unix release target.
func TestInstallTargets(t *testing.T) {
	for _, target := range []struct{ os, arch, asset string }{
		{"Darwin", "arm64", "darwin_arm64"},
		{"Darwin", "x86_64", "darwin_amd64"},
		{"Linux", "aarch64", "linux_arm64"},
		{"Linux", "x86_64", "linux_amd64"},
	} {
		t.Run(target.asset, func(t *testing.T) {
			f := newFixture(t, target.asset)
			out, err := f.install(map[string]string{"FIXTURE_OS": target.os, "FIXTURE_ARCH": target.arch})
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}

			binary := filepath.Join(f.home, ".local", "bin", "baton")
			cmd := exec.Command(binary, "help")
			cmd.Env = f.env(nil)
			if got, err := cmd.CombinedOutput(); err != nil || string(got) != "baton fixture\n" {
				t.Fatalf("installed program: %v %q", err, got)
			}

			requests := readFile(t, filepath.Join(f.root, "requests"))
			want := "https://github.com/sushilk1991/baton/releases/latest/download/"
			if !strings.Contains(requests, want+"baton_"+target.asset+".tar.gz") || !strings.Contains(requests, want+"checksums.txt") {
				t.Fatalf("wrong release URLs: %s", requests)
			}
			t.Logf("%s: piped install verified and executable", target.asset)
		})
	}
}

// Failed verification must leave the existing binary byte-identical.
func TestInstallPreservesBinary(t *testing.T) {
	for _, scenario := range []struct{ name, want string }{
		{"mismatch", "checksum mismatch"},
		{"missing", "checksum entry"},
		{"duplicate", "checksum entry"},
		{"invalid", "checksum entry"},
		{"archive-fetch", "download failed"},
		{"checksum-fetch", "download failed"},
		{"bad-archive", "extract"},
		{"empty-binary", "empty"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newFixture(t, "linux_amd64")
			archive := filepath.Join(f.root, "baton_linux_amd64.tar.gz")
			manifest := filepath.Join(f.root, "checksums.txt")
			content := readFile(t, manifest)
			switch scenario.name {
			case "mismatch":
				writeFile(t, archive, "changed", 0600)
			case "missing":
				content = ""
			case "duplicate":
				content += content
			case "invalid":
				content = "not-a-sha256  baton_linux_amd64.tar.gz\n"
			case "bad-archive":
				writeFile(t, archive, "invalid tar", 0600)
				content = checksumLine("baton_linux_amd64.tar.gz", []byte("invalid tar"))
			case "empty-binary":
				data := archiveBytes(t, "")
				writeFile(t, archive, string(data), 0600)
				content = checksumLine("baton_linux_amd64.tar.gz", data)
			}
			writeFile(t, manifest, content, 0600)
			dir := filepath.Join(f.home, "custom bin")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(dir, "baton")
			writeFile(t, binary, "existing binary", 0755)
			out, err := f.install(map[string]string{"BATON_INSTALL_DIR": dir, "FIXTURE_FAILURE": scenario.name})
			if err == nil || !strings.Contains(out, scenario.want) {
				t.Fatalf("want failure %q: %v %s", scenario.want, err, out)
			}
			if got := readFile(t, binary); got != "existing binary" {
				t.Fatalf("failure replaced binary: %q", got)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("staging residue: %v %v", entries, err)
			}
			t.Logf("%s: %s; existing binary preserved", scenario.name, scenario.want)
		})
	}
}

// Explicit versions and directories work without changing shell configuration.
func TestInstallOverrides(t *testing.T) {
	f := newFixture(t, "linux_amd64")
	dir := filepath.Join(f.home, "custom bin")
	out, err := f.install(map[string]string{"BATON_VERSION": "v1.2.3", "BATON_INSTALL_DIR": dir})
	if err != nil {
		t.Fatalf("install: %v %s", err, out)
	}
	if got := readFile(t, filepath.Join(dir, "baton")); got != fixtureProgram {
		t.Fatalf("wrong installed bytes: %q", got)
	}
	if got := readFile(t, filepath.Join(f.root, "requests")); !strings.Contains(got, "/releases/download/v1.2.3/") {
		t.Fatalf("version ignored: %s", got)
	}
}

// Reject unsupported input before contacting the release host or writing files.
func TestInstallRejectsInput(t *testing.T) {
	for _, overrides := range []map[string]string{
		{"FIXTURE_OS": "Windows"}, {"FIXTURE_ARCH": "i386"},
		{"BATON_VERSION": "../../other"}, {"BATON_INSTALL_DIR": "relative"},
	} {
		f := newFixture(t, "linux_amd64")
		out, err := f.install(overrides)
		if err == nil {
			t.Fatalf("accepted invalid input %v: %s", overrides, out)
		}
		if _, err := os.Stat(filepath.Join(f.root, "requests")); !os.IsNotExist(err) {
			t.Fatalf("invalid input contacted release host: %v", err)
		}
	}
}

// A symlink destination must never redirect an upgrade into an unrelated file.
func TestInstallRejectsSymlink(t *testing.T) {
	f := newFixture(t, "linux_amd64")
	dir := filepath.Join(f.home, ".local", "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(f.root, "outside")
	writeFile(t, outside, "unrelated", 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "baton")); err != nil {
		t.Fatal(err)
	}
	out, err := f.install(nil)
	if err == nil || !strings.Contains(out, "symlink") || readFile(t, outside) != "unrelated" {
		t.Fatalf("symlink protection: %v %s", err, out)
	}
}

// A tap formula must carry all four exact release URLs and trusted digests.
func TestHomebrewFormula(t *testing.T) {
	f := newFixture(t, "linux_amd64")
	manifest := ""
	for _, target := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		manifest += checksumLine("baton_"+target+".tar.gz", []byte(target))
	}
	path := filepath.Join(f.root, "formula-checksums")
	writeFile(t, path, manifest, 0600)
	cmd := exec.Command("sh", "../../scripts/homebrew-formula.sh", "v1.2.3", path)
	cmd.Env = f.env(nil)
	out, err := cmd.CombinedOutput()
	if err != nil || bytes.Contains(out, []byte("@")) {
		t.Fatalf("render: %v %s", err, out)
	}
	for _, target := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		url := "https://github.com/sushilk1991/baton/releases/download/v1.2.3/baton_" + target + ".tar.gz"
		if !bytes.Contains(out, []byte(url)) || !bytes.Contains(out, []byte(fmt.Sprintf("%x", sha256.Sum256([]byte(target))))) {
			t.Fatalf("missing target or digest %s: %s", target, out)
		}
	}

	// Missing and duplicated archive hashes must fail with no partial formula.
	for _, content := range []string{"", manifest + manifest, strings.ReplaceAll(manifest, "baton_linux_arm64", "other")} {
		writeFile(t, path, content, 0600)
		cmd := exec.Command("sh", "../../scripts/homebrew-formula.sh", "v1.2.3", path)
		cmd.Env = f.env(nil)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil || stdout.Len() != 0 {
			t.Fatalf("invalid manifest emitted formula: %v %s %s", err, &stdout, &stderr)
		}
	}
}

type fixture struct {
	t                 *testing.T
	root, home, tools string
}

func newFixture(t *testing.T, target string) fixture {
	t.Helper()
	f := fixture{t: t, root: t.TempDir(), home: t.TempDir(), tools: t.TempDir()}
	data := archiveBytes(t, fixtureProgram)
	asset := "baton_" + target + ".tar.gz"
	writeFile(t, filepath.Join(f.root, asset), string(data), 0600)
	writeFile(t, filepath.Join(f.root, "checksums.txt"), checksumLine(asset, data), 0600)

	// These boundary fakes replace only network and platform detection. Tar,
	// checksum tools, filesystem publication, and execution remain real.
	writeFile(t, filepath.Join(f.tools, "uname"), `#!/bin/sh
case "$1" in
  -s) printf '%s\n' "$FIXTURE_OS" ;;
  -m) printf '%s\n' "$FIXTURE_ARCH" ;;
  *) exit 2 ;;
esac
`, 0755)
	writeFile(t, filepath.Join(f.tools, "curl"), `#!/bin/sh
set -eu
url= output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output=$2; shift ;;
    https://github.com/sushilk1991/baton/releases/*) url=$1 ;;
  esac
  shift
done
[ -n "$url" ] && [ -n "$output" ] || exit 2
printf '%s\n' "$url" >> "$FIXTURE_ROOT/requests"
asset=${url##*/}
case "${FIXTURE_FAILURE-}:$asset" in
  archive-fetch:*.tar.gz|checksum-fetch:checksums.txt) exit 22 ;;
esac
cp "$FIXTURE_ROOT/$asset" "$output"
`, 0755)
	return f
}

func (f fixture) env(overrides map[string]string) []string {
	// Every child gets a private HOME and every inherited XDG key is replaced.
	values := map[string]string{
		"HOME": f.home, "USERPROFILE": f.home, "TMPDIR": f.root,
		"PATH":         f.tools + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FIXTURE_ROOT": f.root, "FIXTURE_OS": "Linux", "FIXTURE_ARCH": "x86_64",
		"BATON_VERSION": "", "BATON_INSTALL_DIR": "",
		"XDG_CONFIG_HOME": "", "XDG_CACHE_HOME": "", "XDG_STATE_HOME": "",
		"XDG_DATA_HOME": "", "XDG_RUNTIME_DIR": "", "XDG_CONFIG_DIRS": "", "XDG_DATA_DIRS": "",
	}
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			values[key] = ""
		}
		if _, replaced := values[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key := range values {
		if strings.HasPrefix(key, "XDG_") {
			values[key] = filepath.Join(f.home, key)
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func (f fixture) install(overrides map[string]string) (string, error) {
	f.t.Helper()
	data, err := os.ReadFile("../../install.sh")
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command("sh")
	cmd.Env, cmd.Stdin = f.env(overrides), bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func archiveBytes(t *testing.T, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "baton", Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func checksumLine(name string, content []byte) string {
	return fmt.Sprintf("%x  %s\n", sha256.Sum256(content), name)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
