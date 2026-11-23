package app

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"charm.land/huh/v2"

	"github.com/infrapi/cli/internal/dev"
)

//go:embed all:templates
var templates embed.FS

// Versions a new project starts with.
const (
	GoVersion  = "1.27.3"
	LibVersion = "v0.20.0"
	GinVersion = "v1.12.0"
	// CIRef is the infrapi/ci version its CI imports.
	CIRef = "v1"
)

// CI systems a project's CI is written for.
const (
	CIGitHub = "github"
	CIGitLab = "gitlab"
	CINone   = "none"
)

// Packages a release builds.
const (
	PackageArchive = "archive"
	PackageDeb     = "deb"
	PackageRPM     = "rpm"
	PackageImage   = "image"
	// PackageNone, alone, is none of them.
	PackageNone = "none"
)

// Options are a new project's: infrapi-cli app init's flags and answers.
type Options struct {
	// Name is the API's: its directory, binary and Go package. Lower case
	// letters and digits.
	Name string
	// Dir is where its directory is created: "." when empty.
	Dir string
	// Module is its Go module: example.com/<name> when empty.
	Module string
	// Group is its API group: <name>.infrapi.io when empty.
	Group string
	// CI is github, gitlab or none: github when empty.
	CI string
	// Packages are what a release builds: archive, deb, rpm, image, or
	// none alone. Every one when empty.
	Packages []string
	// Image is the image's name, without a tag: from Module when empty,
	// ghcr.io/<owner>/<name> for a github.com module.
	Image string
	// GoPrivate is the GOPRIVATE of the CI and the Makefile: the private
	// modules' patterns, gitlab.example.com/* say. Asked for when Module's
	// host is neither github.com nor gitlab.com.
	GoPrivate string
	// Dev are the answers of its dev.yaml; API and Port are its own.
	Dev dev.Answers
}

var (
	appName  = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	module   = regexp.MustCompile(`^[a-z0-9.-]+\.[a-z]+(/[A-Za-z0-9._~-]+)+$`)
	dnsGroup = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)+$`)
)

// defaults fills what o leaves empty.
func (o *Options) defaults() {
	if o.Dir == "" {
		o.Dir = "."
	}
	if o.Module == "" {
		o.Module = "example.com/" + o.Name
	}
	if o.Group == "" {
		o.Group = o.Name + ".infrapi.io"
	}
	if o.CI == "" {
		o.CI = CIGitHub
	}
	switch {
	case len(o.Packages) == 0:
		o.Packages = []string{PackageArchive, PackageDeb, PackageRPM, PackageImage}
	case len(o.Packages) == 1 && o.Packages[0] == PackageNone:
		o.Packages = []string{}
	}
	if o.Image == "" {
		o.Image = defaultImage(o.Module, o.Name)
		if o.CI == CIGitLab {
			// the project's own registry, as the pipeline knows it
			o.Image = gitlabImage
		}
	}
}

// gitlabImage is a GitLab project's image: GoReleaser reads the variable
// the pipeline sets.
const gitlabImage = "{{ .Env.CI_REGISTRY_IMAGE }}"

// defaultImage is a module's image: on ghcr.io for github.com, the GitLab
// registry for gitlab.com, registry.example.com otherwise.
func defaultImage(module, name string) string {
	host, rest, _ := strings.Cut(module, "/")
	switch host {
	case "github.com":
		owner, _, _ := strings.Cut(rest, "/")
		return "ghcr.io/" + strings.ToLower(owner) + "/" + name
	case "gitlab.com":
		return "registry.gitlab.com/" + strings.ToLower(rest)
	}
	return "registry.example.com/" + name
}

// publicHost is a module host whose modules the Go proxy has.
func publicHost(module string) bool {
	host, _, _ := strings.Cut(module, "/")
	return host == "github.com" || host == "gitlab.com" || host == "example.com"
}

func (o *Options) check() error {
	if !appName.MatchString(o.Name) {
		return fmt.Errorf("name %q: lower case letters and digits, a Go package's name, like compute", o.Name)
	}
	if !module.MatchString(o.Module) {
		return fmt.Errorf("module %q: a Go module path, like github.com/acme/%s", o.Module, o.Name)
	}
	if !dnsGroup.MatchString(o.Group) {
		return fmt.Errorf("group %q: a lower case DNS name, like %s.infrapi.io", o.Group, o.Name)
	}
	switch o.CI {
	case CIGitHub, CIGitLab, CINone:
	default:
		return fmt.Errorf("ci %q: github, gitlab or none", o.CI)
	}
	for _, p := range o.Packages {
		switch p {
		case PackageArchive, PackageDeb, PackageRPM, PackageImage:
		default:
			return fmt.Errorf("package %q: archive, deb, rpm or image", p)
		}
	}
	return nil
}

// Ask asks for what o leaves empty, its values the defaults.
func Ask(o *Options) error {
	if o.Name == "" {
		if err := huh.NewInput().Title("Name").Description("the API's: its directory, binary and Go package").
			Value(&o.Name).Validate(func(s string) error {
			if !appName.MatchString(s) {
				return errors.New("lower case letters and digits, like compute")
			}
			return nil
		}).Run(); err != nil {
			return err
		}
	}
	imageGiven := o.Image != ""
	o.defaults()
	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Go module").Value(&o.Module).Validate(func(s string) error {
			if !module.MatchString(s) {
				return errors.New("a Go module path, like github.com/acme/" + o.Name)
			}
			return nil
		}),
		huh.NewInput().Title("API group").Description("its paths: /apis/<group>/v1/...").Value(&o.Group).
			Validate(func(s string) error {
				if !dnsGroup.MatchString(s) {
					return errors.New("a lower case DNS name, like " + o.Name + ".infrapi.io")
				}
				return nil
			}),
		huh.NewSelect[string]().Title("CI").Options(
			huh.NewOption("GitHub Actions", CIGitHub), huh.NewOption("GitLab CI", CIGitLab), huh.NewOption("none", CINone),
		).Value(&o.CI),
		huh.NewMultiSelect[string]().Title("A release builds").Options(
			huh.NewOption("archives (tar.gz)", PackageArchive).Selected(true),
			huh.NewOption("deb package", PackageDeb).Selected(true),
			huh.NewOption("rpm package", PackageRPM).Selected(true),
			huh.NewOption("container image", PackageImage).Selected(true),
		).Value(&o.Packages),
	)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).Run(); err != nil {
		return err
	}
	var more []huh.Field
	if !imageGiven {
		o.Image = ""
		o.defaults()
	}
	if slices.Contains(o.Packages, PackageImage) {
		more = append(more, huh.NewInput().Title("Image").Description("without a tag").Value(&o.Image))
	}
	if !publicHost(o.Module) && o.GoPrivate == "" {
		host, _, _ := strings.Cut(o.Module, "/")
		o.GoPrivate = host + "/*"
		more = append(more, huh.NewInput().Title("GOPRIVATE").
			Description("the private modules, fetched from their repository: empty when none").Value(&o.GoPrivate))
	}
	if len(more) == 0 {
		return nil
	}
	return huh.NewForm(huh.NewGroup(more...)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).Run()
}

// Init writes a new project into Dir/Name, its dev.yaml and its generated
// code, and returns its directory. It refuses a directory that exists.
func Init(o Options) (string, error) {
	o.defaults()
	if err := o.check(); err != nil {
		return "", fmt.Errorf("app init: %w", err)
	}
	root := filepath.Join(o.Dir, o.Name)
	if _, err := os.Stat(root); err == nil {
		return "", fmt.Errorf("app init: %s exists", root)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	// ImageName is the image as a reader sees it: the pipeline's
	// variable, not GoReleaser's template of it
	registry, _, _ := strings.Cut(o.Image, "/")
	imageName := o.Image
	if o.Image == gitlabImage {
		registry = "" // the pipeline's own, $CI_REGISTRY
		imageName = "$CI_REGISTRY_IMAGE"
	}
	vars := map[string]any{
		"Name": o.Name, "Module": o.Module, "Group": o.Group, "CI": o.CI, "GoPrivate": o.GoPrivate,
		"Image": o.Image, "ImageName": imageName, "Registry": registry, "EnvPrefix": dev.EnvPrefix(o.Name),
		"Archives": slices.Contains(o.Packages, PackageArchive), "Deb": slices.Contains(o.Packages, PackageDeb),
		"RPM": slices.Contains(o.Packages, PackageRPM), "Docker": slices.Contains(o.Packages, PackageImage),
		"GoVersion": GoVersion, "LibVersion": LibVersion, "GinVersion": GinVersion, "CIRef": CIRef,
	}
	if err := render(root, vars); err != nil {
		return "", err
	}
	a := o.Dev
	a.API = o.Group
	if err := dev.WriteConfig(filepath.Join(root, dev.ConfigName), a); err != nil {
		return "", err
	}
	if _, err := Generate(root); err != nil {
		return "", err
	}
	return root, nil
}

// render writes templates/project into root: _name_ in a path is the
// API's name, .tmpl is dropped, and a file rendering to nothing (the CI
// not chosen, say) is not written.
func render(root string, vars map[string]any) error {
	const base = "templates/project"
	return fs.WalkDir(templates, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := templates.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, base+"/"), ".tmpl")
		rel = strings.ReplaceAll(rel, "_name_", vars["Name"].(string))
		t, err := template.New(rel).Delims("[[", "]]").Option("missingkey=error").Parse(string(src))
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		var b bytes.Buffer
		if err := t.Execute(&b, vars); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if len(bytes.TrimSpace(b.Bytes())) == 0 {
			return nil
		}
		content := b.Bytes()
		if strings.HasSuffix(rel, ".go") {
			// imports sorted, as gofmt has them
			if content, err = format.Source(content); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
		}
		out := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //#nosec G301 -- a source directory
			return err
		}
		return os.WriteFile(out, content, 0o644) //#nosec G306 -- a source file
	})
}

// FindRoot is the project dir is in: the first directory, from dir up,
// with a go.mod and an api/ directory.
func FindRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := dir; ; d = filepath.Dir(d) {
		if fileExists(filepath.Join(d, "go.mod")) && fileExists(filepath.Join(d, "api")) {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("%s: not in an API project, no go.mod and api/ here or above (infrapi-cli app init)", dir)
		}
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
