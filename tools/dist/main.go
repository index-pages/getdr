// Command dist turns a getdr release – the GoReleaser archives and
// checksums.txt – into the Homebrew formula, the npm packages and the PyPI
// wheels. The release workflow runs it after GoReleaser:
//
//	go run ./tools/dist homebrew|npm|pypi -version 0.2.1 -dist dist -out pkg
//
// homebrew needs only checksums.txt; npm and pypi take the binaries out of
// the archives after checking them against checksums.txt.
package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	homepage = "https://index-pages.com/getdr/"
	repo     = "https://github.com/index-pages/getdr"
	summary  = "Check Ahrefs Domain Rating (DR) for domains from the command line – one, a list, a file or stdin"
)

//go:embed templates
var templates embed.FS

// target is one platform of a release, named the way each channel names it.
type target struct {
	goos, goarch  string // GoReleaser
	npmOS, npmCPU string // process.platform, process.arch
	wheel         string // wheel platform tag, compressed tag sets allowed
}

var targets = []target{
	{"darwin", "amd64", "darwin", "x64", "macosx_11_0_x86_64"},
	{"darwin", "arm64", "darwin", "arm64", "macosx_11_0_arm64"},
	{"linux", "amd64", "linux", "x64", "manylinux_2_17_x86_64.manylinux2014_x86_64.musllinux_1_1_x86_64"},
	{"linux", "arm64", "linux", "arm64", "manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64"},
	{"windows", "amd64", "win32", "x64", "win_amd64"},
	{"windows", "arm64", "win32", "arm64", "win_arm64"},
}

func (t target) archive(version string) string {
	ext := ".tar.gz"
	if t.goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("getdr_%s_%s_%s%s", version, t.goos, t.goarch, ext)
}

func (t target) exe() string {
	if t.goos == "windows" {
		return "getdr.exe"
	}
	return "getdr"
}

func (t target) npmName() string { return "getdr-" + t.npmOS + "-" + t.npmCPU }

func (t target) url(version string) string {
	return fmt.Sprintf("%s/releases/download/v%s/%s", repo, version, t.archive(version))
}

var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dist:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: dist homebrew|npm|pypi -version V -dist DIR -out DIR")
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	version := fs.String("version", "", "release version, e.g. 0.2.1")
	dist := fs.String("dist", "dist", "directory with the archives and checksums.txt")
	out := fs.String("out", "pkg", "output directory")
	license := fs.String("license", "LICENSE", "license file to ship in the npm packages and wheels")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !versionRe.MatchString(*version) {
		return fmt.Errorf("-version %q is not X.Y.Z", *version)
	}
	r, err := openRelease(*dist, *version)
	if err != nil {
		return err
	}
	if r.license, err = os.ReadFile(*license); err != nil {
		return err
	}
	switch cmd {
	case "homebrew":
		return r.homebrew(*out)
	case "npm":
		return r.npm(*out)
	case "pypi":
		return r.pypi(*out)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

type release struct {
	dir, version string
	sums         map[string]string // archive name -> sha256 hex
	license      []byte
}

func openRelease(dir, version string) (*release, error) {
	f, err := os.Open(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := &release{dir: dir, version: version, sums: map[string]string{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if ok {
			r.sums[name] = sum
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, t := range targets {
		if len(r.sums[t.archive(version)]) != 64 {
			return nil, fmt.Errorf("checksums.txt has no sha256 for %s", t.archive(version))
		}
	}
	return r, nil
}

// binary returns the getdr executable from t's archive, after checking the
// archive against checksums.txt.
func (r *release) binary(t target) ([]byte, error) {
	name := t.archive(r.version)
	data, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != r.sums[name] {
		return nil, fmt.Errorf("%s does not match checksums.txt", name)
	}
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == t.exe() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s has no %s", name, t.exe())
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s has no %s", name, t.exe())
		}
		if err != nil {
			return nil, err
		}
		if h.Name == t.exe() && h.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}

func readTemplate(name string) string {
	b, err := templates.ReadFile("templates/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
