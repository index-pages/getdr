package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRelease writes archives holding "binary <os> <arch>" and their
// checksums.txt, the way GoReleaser lays them out.
func fakeRelease(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	var sums strings.Builder
	for _, tg := range targets {
		content := []byte("binary " + tg.goos + " " + tg.goarch)
		var buf bytes.Buffer
		if tg.goos == "windows" {
			zw := zip.NewWriter(&buf)
			w, _ := zw.Create(tg.exe())
			w.Write(content)
			zw.Close()
		} else {
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
			tw.Write([]byte("hi"))
			tw.WriteHeader(&tar.Header{Name: tg.exe(), Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
			tw.Write(content)
			tw.Close()
			gz.Close()
		}
		name := tg.archive(version)
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(buf.Bytes())
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runDist(t *testing.T, cmd, dist string) (string, error) {
	t.Helper()
	out := t.TempDir()
	return out, run([]string{cmd, "-version", "1.2.3", "-dist", dist, "-out", out, "-license", "../../LICENSE"})
}

func TestHomebrew(t *testing.T) {
	dist := fakeRelease(t, "1.2.3")
	out, err := runDist(t, "homebrew", dist)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := os.ReadFile(filepath.Join(out, "Formula", "getdr.rb"))
	sums, _ := os.ReadFile(filepath.Join(dist, "checksums.txt"))
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		sum, name, _ := strings.Cut(line, "  ")
		if strings.Contains(name, "windows") {
			continue
		}
		want := fmt.Sprintf("url \"%s/releases/download/v1.2.3/%s\"\n      sha256 \"%s\"", repo, name, sum)
		if !strings.Contains(string(rb), want) {
			t.Errorf("formula lacks %s with its sha256", name)
		}
	}
	if !strings.Contains(string(rb), `version "1.2.3"`) || strings.Contains(string(rb), "<no value>") {
		t.Errorf("formula:\n%s", rb)
	}
}

func TestNPM(t *testing.T) {
	out, err := runDist(t, "npm", fakeRelease(t, "1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	shim, _ := os.ReadFile(filepath.Join(out, "getdr", "bin", "getdr.js"))
	pkg, _ := os.ReadFile(filepath.Join(out, "getdr", "package.json"))
	for _, tg := range targets {
		bin, err := os.ReadFile(filepath.Join(out, tg.npmName(), "bin", tg.exe()))
		if err != nil || string(bin) != "binary "+tg.goos+" "+tg.goarch {
			t.Errorf("%s: binary %q, %v", tg.npmName(), bin, err)
		}
		entry := fmt.Sprintf("%q: %q,", tg.npmOS+" "+tg.npmCPU, tg.npmName()+"/bin/"+tg.exe())
		if !strings.Contains(string(shim), entry) {
			t.Errorf("shim lacks %s", entry)
		}
		if !strings.Contains(string(pkg), fmt.Sprintf("%q: \"1.2.3\"", tg.npmName())) {
			t.Errorf("getdr/package.json lacks the optional dependency %s", tg.npmName())
		}
	}
}

func TestPyPI(t *testing.T) {
	out, err := runDist(t, "pypi", fakeRelease(t, "1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		path := filepath.Join(out, "getdr-1.2.3-py3-none-"+tg.wheel+".whl")
		zr, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		modes := map[string]os.FileMode{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			var b bytes.Buffer
			b.ReadFrom(rc)
			rc.Close()
			files[f.Name] = b.Bytes()
			modes[f.Name] = f.Mode()
		}
		zr.Close()
		script := "getdr-1.2.3.data/scripts/" + tg.exe()
		if string(files[script]) != "binary "+tg.goos+" "+tg.goarch || modes[script]&0o111 == 0 {
			t.Errorf("%s: script %q mode %v", tg.wheel, files[script], modes[script])
		}
		record := string(files["getdr-1.2.3.dist-info/RECORD"])
		for name, data := range files {
			if strings.HasSuffix(name, "/RECORD") {
				continue
			}
			sum := sha256.Sum256(data)
			want := fmt.Sprintf("%s,sha256=%s,%d\n", name, base64.RawURLEncoding.EncodeToString(sum[:]), len(data))
			if !strings.Contains(record, want) {
				t.Errorf("%s: RECORD lacks %q", tg.wheel, want)
			}
		}
		wheel := string(files["getdr-1.2.3.dist-info/WHEEL"])
		for _, plat := range strings.Split(tg.wheel, ".") {
			if !strings.Contains(wheel, "Tag: py3-none-"+plat+"\n") {
				t.Errorf("%s: WHEEL lacks tag %s", tg.wheel, plat)
			}
		}
	}
}

func TestTamperedArchive(t *testing.T) {
	dist := fakeRelease(t, "1.2.3")
	name := filepath.Join(dist, targets[0].archive("1.2.3"))
	data, _ := os.ReadFile(name)
	os.WriteFile(name, append(data, 0), 0o644)
	if _, err := runDist(t, "npm", dist); err == nil || !strings.Contains(err.Error(), "does not match checksums.txt") {
		t.Errorf("err = %v", err)
	}
}

func TestBadInput(t *testing.T) {
	dist := fakeRelease(t, "1.2.3")
	if _, err := runDist(t, "snap", dist); err == nil {
		t.Error("unknown command accepted")
	}
	if err := run([]string{"npm", "-version", "v1.2.3", "-dist", dist}); err == nil {
		t.Error("version with v accepted")
	}
	if _, err := runDist(t, "homebrew", t.TempDir()); err == nil {
		t.Error("missing checksums.txt accepted")
	}
}
