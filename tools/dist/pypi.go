package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pypi writes one wheel per platform. A wheel holds only the binary, in
// getdr-<version>.data/scripts, which pip, pipx and uv put on PATH; there is
// no Python code and no sdist, so nothing runs at install time.
func (r *release) pypi(out string) error {
	readme, err := renderReadme("pypi")
	if err != nil {
		return err
	}
	metadata := r.wheelMetadata(readme)
	for _, t := range targets {
		bin, err := r.binary(t)
		if err != nil {
			return err
		}
		var tags strings.Builder
		for _, plat := range strings.Split(t.wheel, ".") {
			fmt.Fprintf(&tags, "Tag: py3-none-%s\n", plat)
		}
		info := "getdr-" + r.version + ".dist-info/"
		files := []wheelFile{
			{"getdr-" + r.version + ".data/scripts/" + t.exe(), bin, 0o755},
			{info + "METADATA", []byte(metadata), 0o644},
			{info + "WHEEL", []byte("Wheel-Version: 1.0\nGenerator: getdr tools/dist\nRoot-Is-Purelib: false\n" + tags.String()), 0o644},
			{info + "licenses/LICENSE", r.license, 0o644},
		}
		name := fmt.Sprintf("getdr-%s-py3-none-%s.whl", r.version, t.wheel)
		if err := writeWheel(filepath.Join(out, name), files, info+"RECORD"); err != nil {
			return err
		}
	}
	return nil
}

func (r *release) wheelMetadata(readme string) string {
	var b strings.Builder
	for _, line := range []string{
		"Metadata-Version: 2.4",
		"Name: getdr",
		"Version: " + r.version,
		"Summary: " + summary,
		"Keywords: ahrefs,domain-rating,dr,seo,cli",
		"Author-email: Index Pages <mail@index-pages.com>",
		"License-Expression: MIT",
		"License-File: LICENSE",
		"Project-URL: Homepage, " + homepage,
		"Project-URL: Source, " + repo,
		"Project-URL: Changelog, " + repo + "/blob/main/CHANGELOG.md",
		"Project-URL: Issues, " + repo + "/issues",
		"Classifier: Environment :: Console",
		"Classifier: Intended Audience :: Developers",
		"Classifier: Operating System :: MacOS",
		"Classifier: Operating System :: Microsoft :: Windows",
		"Classifier: Operating System :: POSIX :: Linux",
		"Classifier: Programming Language :: Go",
		"Classifier: Topic :: Internet :: WWW/HTTP :: Indexing/Search",
		"Classifier: Topic :: Utilities",
		"Description-Content-Type: text/markdown",
	} {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + readme)
	return b.String()
}

type wheelFile struct {
	name string
	data []byte
	mode os.FileMode
}

// writeWheel writes a reproducible wheel: fixed timestamps, files in the
// given order, then RECORD with the sha256 and size of each.
func writeWheel(path string, files []wheelFile, record string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var rec strings.Builder
	add := func(f wheelFile) error {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		h.SetMode(f.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(f.data)
		return err
	}
	for _, f := range files {
		if err := add(f); err != nil {
			return err
		}
		sum := sha256.Sum256(f.data)
		fmt.Fprintf(&rec, "%s,sha256=%s,%d\n", f.name, base64.RawURLEncoding.EncodeToString(sum[:]), len(f.data))
	}
	fmt.Fprintf(&rec, "%s,,\n", record)
	if err := add(wheelFile{record, []byte(rec.String()), 0o644}); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return writeFile(path, buf.Bytes(), 0o644)
}
