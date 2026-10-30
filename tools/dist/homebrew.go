package main

import (
	"bytes"
	"path/filepath"
	"text/template"
)

// homebrew writes Formula/getdr.rb for the index-pages/homebrew-tap repo. It
// installs the prebuilt binary, so Homebrew needs no Go and macOS sees no
// quarantine flag on it.
func (r *release) homebrew(out string) error {
	type platform struct{ URL, SHA256 string }
	data := struct {
		Homepage, Version string
		P                 map[string]platform
	}{homepage, r.version, map[string]platform{}}
	for _, t := range targets {
		data.P[t.goos+"_"+t.goarch] = platform{t.url(r.version), r.sums[t.archive(r.version)]}
	}
	tmpl := template.Must(template.New("formula").Parse(readTemplate("getdr.rb.tmpl")))
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return err
	}
	return writeFile(filepath.Join(out, "Formula", "getdr.rb"), b.Bytes(), 0o644)
}
