package dev

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"

	"charm.land/huh/v2"
)

// Answers are what a new dev.yaml is written from: dev init's questions,
// and app init's API.
type Answers struct {
	// Domain is Kayra's API domain: infrapi.io when empty.
	Domain string
	// Platform is sandbox, preprod or prod: sandbox when empty.
	Platform string
	// Region is eu-west-1 when empty.
	Region string
	// Image is Kayra's: ghcr.io/infrapi/kayra:latest when empty.
	Image string
	// API is the API developed against the stack, its group in full or its
	// name: compute when empty.
	API string
	// Port is where it listens on the host: 8080 when empty.
	Port int
}

func (a *Answers) defaults() {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&a.Domain, "infrapi.io")
	def(&a.Platform, "sandbox")
	def(&a.Region, "eu-west-1")
	def(&a.Image, "ghcr.io/infrapi/kayra:latest")
	def(&a.API, "compute")
	if a.Port == 0 {
		a.Port = 8080
	}
}

// ErrConfigExists is WriteConfig's refusal to overwrite a dev.yaml.
var ErrConfigExists = errors.New("dev.yaml exists")

// WriteConfig writes a new dev.yaml at path from a, its every setting
// commented, and checks it loads. It never overwrites one.
func WriteConfig(path string, a Answers) error {
	a.defaults()
	src, err := templates.ReadFile("templates/dev.yaml.tmpl")
	if err != nil {
		return err
	}
	t, err := template.New("dev.yaml").Delims("[[", "]]").Option("missingkey=error").Parse(string(src))
	if err != nil {
		return err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, map[string]any{
		"Domain": a.Domain, "Platform": a.Platform, "Region": a.Region, "Image": a.Image,
		"API": a.API, "Port": a.Port, "AppName": AppName(a.API), "DexImage": DexImage,
	}); err != nil {
		return err
	}
	// the answers make a dev.yaml the CLI takes, or none is written
	if _, err := parseConfig(b.Bytes(), filepath.Dir(path)); err != nil {
		return fmt.Errorf("dev.yaml: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //#nosec G302 G304 -- dev.yaml, committed with the project
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s: %w", path, ErrConfigExists)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(b.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Ask asks for the answers dev init needs, a's values the defaults.
func Ask(a *Answers) error {
	a.defaults()
	return huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Domain").
			Description("Kayra's API domain: its groups are system.<domain>, catalog.<domain>, authorization.<domain>").
			Value(&a.Domain).Validate(func(s string) error {
			if !dnsName.MatchString(s) {
				return errors.New("a lower case DNS name of two labels or more, like infrapi.io")
			}
			return nil
		}),
		huh.NewSelect[string]().Title("Platform").Description("as infrapi/lib takes it").
			Options(huh.NewOptions("sandbox", "preprod", "prod")...).Value(&a.Platform),
		huh.NewInput().Title("Region").Value(&a.Region).Validate(func(s string) error {
			if !dnsLabel.MatchString(s) {
				return errors.New("a lower case DNS label")
			}
			return nil
		}),
		huh.NewInput().Title("Kayra image").
			Description("set kayra.src in dev.yaml to build it from a checkout instead").Value(&a.Image),
	)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).Run()
}
