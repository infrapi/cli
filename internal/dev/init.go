package dev

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/hay-kot/scaffold/app/core/engine"
	"github.com/hay-kot/scaffold/app/core/rwfs"
	"github.com/hay-kot/scaffold/app/scaffold"
	"github.com/hay-kot/scaffold/app/scaffold/pkgs"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// DefaultTemplate is the dev stack's template: infrapi/dev, a scaffold
// (github.com/hay-kot/scaffold) whose scaffold.yaml asks for the domain,
// the platform, the region and Kayra's image.
const DefaultTemplate = "https://github.com/infrapi/dev"

// InitOptions are infrapi-cli dev init's.
type InitOptions struct {
	// Template is a scaffold: a git URL (…/dev@v1.2.0 pins a tag,
	// …/dev#subdir a directory in it) or a local path. DefaultTemplate
	// when empty.
	Template string
	// Name is the new stack's directory, in Dir. Asked when empty and
	// prompting.
	Name string
	// Dir is where the stack's directory is created, "." when empty.
	Dir string
	// Vars answers the template's questions: domain, platform... Prompting,
	// they are the answers' defaults; with NoPrompt, the answers, every
	// other question taking its default.
	Vars map[string]any
	// NoPrompt asks nothing: Name is required.
	NoPrompt bool
	// Force overwrites files the stack's directory has already.
	Force bool
	// Cache is where a remote template is cloned, and reused on the next
	// init. os.UserCacheDir()/infrapi/templates when empty.
	Cache string
}

// Init renders the template into Dir/Name, and returns that directory.
func Init(o InitOptions, out io.Writer) (string, error) {
	// scaffold logs through zerolog's global logger, at debug by default:
	// its errors come back as errors, the rest is noise here.
	log.Logger = zerolog.Nop()
	if o.Template == "" {
		o.Template = DefaultTemplate
	}
	if o.Dir == "" {
		o.Dir = "."
	}
	if o.Cache == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		o.Cache = filepath.Join(base, "infrapi", "templates")
	}
	if o.NoPrompt && o.Name == "" {
		return "", errors.New("dev init: a name is required without prompts")
	}
	if err := os.MkdirAll(o.Cache, 0o750); err != nil {
		return "", err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	path, err := pkgs.NewResolver(nil, o.Cache, cwd).Resolve(o.Template, nil, noAuth)
	if err != nil {
		return "", fmt.Errorf("dev init: template %s: %w", o.Template, err)
	}

	template := os.DirFS(path)
	p, err := scaffold.LoadProject(template, scaffold.Options{NoClobber: !o.Force})
	if err != nil {
		return "", fmt.Errorf("dev init: template %s: %w", o.Template, err)
	}

	vars := map[string]any{}
	for k, v := range o.Vars {
		vars[k] = v
	}
	if o.Name != "" {
		vars["Project"] = o.Name
	}
	eng := engine.New()
	if o.NoPrompt {
		if err := defaults(p, vars); err != nil {
			return "", err
		}
		p.Name = o.Name
	} else {
		vars, err = p.AskQuestions(vars, eng, huh.ThemeFunc(huh.ThemeCharm))
		if err != nil {
			return "", err
		}
	}
	if p.Name == "" || strings.ContainsAny(p.Name, `/\`) || p.Name == "." || p.Name == ".." {
		return "", fmt.Errorf("dev init: name %q: a directory name", p.Name)
	}

	all, err := scaffold.BuildVars(eng, p, vars)
	if err != nil {
		return "", err
	}
	err = scaffold.RenderRWFS(eng, &scaffold.RWFSArgs{Project: p, ReadFS: template, WriteFS: rwfs.NewOsWFS(o.Dir)}, all)
	if err != nil {
		return "", fmt.Errorf("dev init: %w", err)
	}

	if msg := p.Conf.Messages.Post; msg != "" {
		rendered, err := eng.TmplString(msg, all)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintln(out, strings.TrimSpace(rendered))
	}
	return filepath.Join(o.Dir, p.Name), nil
}

// defaults fills vars with the default of every question it has no answer
// to, and fails on a required one that has none.
func defaults(p *scaffold.Project, vars map[string]any) error {
	for _, q := range p.Conf.Questions {
		if _, ok := vars[q.Name]; ok {
			continue
		}
		if q.Prompt.Default == nil {
			if q.Required {
				return fmt.Errorf("dev init: %s is required, --set %s=<value>", q.Name, q.Name)
			}
			continue
		}
		vars[q.Name] = q.Prompt.Default
	}
	return nil
}

// noAuth clones a public template anonymously.
var noAuth = pkgs.AuthProviderFunc(func(string) (transport.AuthMethod, bool) { return nil, false })
