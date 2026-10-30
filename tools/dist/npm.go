package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"text/template"
)

// npm writes the npm packages, each in its own directory ready for npm
// publish: getdr-<os>-<cpu> holds the binary for one platform, and getdr
// lists them all as optional dependencies, so npm installs only the one that
// matches os and cpu. getdr's bin is a small Node script that runs that
// binary. Nothing runs at install time.
func (r *release) npm(out string) error {
	optional := map[string]string{}
	for _, t := range targets {
		bin, err := r.binary(t)
		if err != nil {
			return err
		}
		dir := filepath.Join(out, t.npmName())
		pkg := npmPackage{
			Name:            t.npmName(),
			Version:         r.version,
			Description:     "The getdr binary for " + t.npmOS + " " + t.npmCPU + "; install the getdr package instead",
			Homepage:        homepage,
			Repository:      npmRepository,
			License:         "MIT",
			Author:          "Index Pages",
			OS:              []string{t.npmOS},
			CPU:             []string{t.npmCPU},
			Files:           []string{"bin/" + t.exe()},
			PreferUnplugged: true,
		}
		readme := "# " + t.npmName() + "\n\nThe [getdr](" + homepage + ") binary for " + t.npmOS + " " + t.npmCPU +
			". It is installed by the [getdr](https://www.npmjs.com/package/getdr) package; you don't need it directly.\n"
		if err := writePackage(dir, pkg, readme, r.license); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, "bin", t.exe()), bin, 0o755); err != nil {
			return err
		}
		optional[t.npmName()] = r.version
	}

	dir := filepath.Join(out, "getdr")
	pkg := npmPackage{
		Name:                 "getdr",
		Version:              r.version,
		Description:          summary,
		Keywords:             []string{"ahrefs", "domain-rating", "dr", "seo", "cli"},
		Homepage:             homepage,
		Bugs:                 repo + "/issues",
		Repository:           npmRepository,
		License:              "MIT",
		Author:               "Index Pages",
		Bin:                  map[string]string{"getdr": "bin/getdr.js"},
		Files:                []string{"bin/getdr.js"},
		OptionalDependencies: optional,
		Engines:              map[string]string{"node": ">=18"},
	}
	readme, err := renderReadme("npm")
	if err != nil {
		return err
	}
	if err := writePackage(dir, pkg, readme, r.license); err != nil {
		return err
	}
	shim := template.Must(template.New("shim").Parse(readTemplate("getdr.js.tmpl")))
	type entry struct{ Platform, Path string }
	var entries []entry
	for _, t := range targets {
		entries = append(entries, entry{t.npmOS + " " + t.npmCPU, t.npmName() + "/bin/" + t.exe()})
	}
	var b bytes.Buffer
	if err := shim.Execute(&b, struct {
		Homepage string
		Binaries []entry
	}{homepage, entries}); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "bin", "getdr.js"), b.Bytes(), 0o755)
}

var npmRepository = map[string]string{"type": "git", "url": "git+" + repo + ".git"}

type npmPackage struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Description          string            `json:"description"`
	Keywords             []string          `json:"keywords,omitempty"`
	Homepage             string            `json:"homepage"`
	Bugs                 string            `json:"bugs,omitempty"`
	Repository           map[string]string `json:"repository"`
	License              string            `json:"license"`
	Author               string            `json:"author"`
	Bin                  map[string]string `json:"bin,omitempty"`
	OS                   []string          `json:"os,omitempty"`
	CPU                  []string          `json:"cpu,omitempty"`
	Files                []string          `json:"files"`
	OptionalDependencies map[string]string `json:"optionalDependencies,omitempty"`
	Engines              map[string]string `json:"engines,omitempty"`
	PreferUnplugged      bool              `json:"preferUnplugged,omitempty"`
}

func writePackage(dir string, pkg npmPackage, readme string, license []byte) error {
	var js bytes.Buffer
	enc := json.NewEncoder(&js)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pkg); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "package.json"), js.Bytes(), 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "LICENSE"), license, 0o644)
}

// renderReadme renders the package page README for channel npm or pypi.
func renderReadme(channel string) (string, error) {
	tmpl := template.Must(template.New("readme").Parse(readTemplate("README.md.tmpl")))
	var b bytes.Buffer
	err := tmpl.Execute(&b, struct{ Channel, Homepage, Repo string }{channel, homepage, repo})
	return b.String(), err
}
