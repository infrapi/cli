package app

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrapi/cli/internal/dev"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// parses checks every .go file under root parses.
func parses(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		if _, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.AllErrors); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newProject(t *testing.T, o Options) string {
	t.Helper()
	o.Dir = t.TempDir()
	root, err := Init(o)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// A new project has its files, its dev.yaml for its group, its generated
// code, and only the CI and packages asked for.
func TestInit(t *testing.T) {
	root := newProject(t, Options{Name: "compute", Module: "github.com/acme/compute"})
	for _, f := range []string{
		".github/workflows/ci.yml", ".gitignore", ".golangci.yml", ".goreleaser.yaml", "Dockerfile", "Makefile", "README.md",
		"api/api.go", "api/compute-v1.yaml", "api/zz_generated.go", "cmd/compute/main.go", "dev.yaml", "go.mod",
		"internal/compute/compute.go", "internal/compute/zz_generated.go", "internal/config/config.go",
		"packaging/compute.env", "packaging/compute.service",
	} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("no %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".gitlab-ci.yml")); err == nil {
		t.Error(".gitlab-ci.yml with github")
	}
	parses(t, root)
	c, err := dev.LoadConfig(filepath.Join(root, "dev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.App.API != "compute.infrapi.io" {
		t.Errorf("dev.yaml app.api %q", c.App.API)
	}
	gr := read(t, filepath.Join(root, ".goreleaser.yaml"))
	for _, want := range []string{"- deb\n", "- rpm\n", "- ghcr.io/acme/compute\n", "archives:"} {
		if !strings.Contains(gr, want) {
			t.Errorf(".goreleaser.yaml has no %q", want)
		}
	}
	if !strings.Contains(read(t, filepath.Join(root, "go.mod")), "module github.com/acme/compute\n") {
		t.Error("go.mod")
	}
	if !strings.Contains(read(t, filepath.Join(root, "internal/config/config.go")), `"COMPUTE_AUTH_URL"`) {
		t.Error("config.go reads no COMPUTE_AUTH_URL")
	}

	if _, err := Init(Options{Name: "compute", Dir: filepath.Dir(root)}); err == nil {
		t.Error("over an existing directory")
	}
}

func TestInit_gitlabNoPackages(t *testing.T) {
	root := newProject(t, Options{Name: "dns", Module: "gitlab.example.com/team/dns", CI: CIGitLab,
		GoPrivate: "gitlab.example.com/*", Packages: []string{PackageNone}})
	ci := read(t, filepath.Join(root, ".gitlab-ci.yml"))
	if !strings.Contains(ci, `goprivate: "gitlab.example.com/*"`) || strings.Contains(ci, "docker:") {
		t.Errorf(".gitlab-ci.yml:\n%s", ci)
	}
	for _, f := range []string{".github/workflows/ci.yml", "Dockerfile", "packaging/dns.service"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Errorf("%s with no packages", f)
		}
	}
	gr := read(t, filepath.Join(root, ".goreleaser.yaml"))
	if strings.Contains(gr, "nfpms:") || strings.Contains(gr, "dockers_v2:") || strings.Contains(gr, "archives:") {
		t.Errorf(".goreleaser.yaml:\n%s", gr)
	}
	if !strings.Contains(read(t, filepath.Join(root, "Makefile")), "export GOPRIVATE ?= gitlab.example.com/*") {
		t.Error("Makefile has no GOPRIVATE")
	}
}

func TestInit_refuses(t *testing.T) {
	for name, o := range map[string]Options{
		"name":    {Name: "Compute"},
		"module":  {Name: "compute", Module: "compute"},
		"group":   {Name: "compute", Group: "Compute"},
		"ci":      {Name: "compute", CI: "jenkins"},
		"package": {Name: "compute", Packages: []string{"msi"}},
	} {
		o.Dir = t.TempDir()
		if _, err := Init(o); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Add adds the paths and the schema, keeps the contract's other lines,
// generates, and writes the operations once.
func TestAdd(t *testing.T) {
	root := newProject(t, Options{Name: "compute", Module: "github.com/acme/compute"})
	contract := filepath.Join(root, "api", "compute-v1.yaml")
	before := read(t, contract)

	a, err := Add(root, Resource{Name: "vm"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(a.Ops, " ") != "ListVMs CreateVM GetVM ReplaceVM DeleteVM" {
		t.Errorf("ops %v", a.Ops)
	}
	after := read(t, contract)
	for _, l := range strings.Split(before, "\n") {
		if l != "paths: {}" && !strings.Contains(after, l) {
			t.Errorf("line lost: %q", l)
		}
	}
	for _, want := range []string{"\n  /apis/compute.infrapi.io/v1/namespaces/{namespace}/vms:\n", "\n    VM:\n", "operationId: deleteVM"} {
		if !strings.Contains(after, want) {
			t.Errorf("contract has no %q", want)
		}
	}
	parses(t, root)
	stubs := read(t, filepath.Join(root, "internal/compute/vms.go"))
	if !strings.Contains(stubs, "func (s *Service) GetVM(ctx context.Context, namespace, name string) (api.VM, error) {\n\treturn api.VM{}, notImplemented(\"GetVM\")") {
		t.Errorf("vms.go:\n%s", stubs)
	}
	gen := read(t, filepath.Join(root, "internal/compute/zz_generated.go"))
	if !strings.Contains(gen, `r.DELETE("/apis/compute.infrapi.io/v1/namespaces/:namespace/vms/:name"`) || !strings.Contains(gen, "c.Status(http.StatusNoContent)") {
		t.Errorf("zz_generated.go:\n%s", gen)
	}

	// once only
	if _, err := Add(root, Resource{Name: "vm"}); err == nil {
		t.Error("vm added twice")
	}
	if _, err := Add(root, Resource{Name: "vm", Plural: "machines"}); err == nil {
		t.Error("a second VM schema")
	}
	// a cluster resource, its file its plural's
	if _, err := Add(root, Resource{Name: "flavor", Cluster: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(root, "internal/compute/flavors.go")), "ListFlavors(ctx context.Context) ([]api.Flavor, error)") {
		t.Error("flavors.go")
	}
}

// Generate follows the contract: the fields' types, required or not, an
// inline object a type of its own, query parameters as arguments.
func TestGenerate_types(t *testing.T) {
	root := newProject(t, Options{Name: "compute", Module: "github.com/acme/compute"})
	contract := filepath.Join(root, "api", "compute-v1.yaml")
	data := strings.Replace(read(t, contract), "paths: {}", `paths:
  /apis/compute.infrapi.io/v1/images:
    get:
      operationId: listImages
      parameters:
        - {name: os, in: query, schema: {type: string}}
      responses:
        '200':
          description: The images.
          content: {application/json: {schema: {type: array, items: {$ref: '#/components/schemas/Image'}}}}`, 1)
	data = strings.Replace(data, "  schemas:\n", `  schemas:
    Image:
      type: object
      required: [name, size_gb]
      properties:
        name: {type: string}
        size_gb: {type: integer, format: int32}
        ratio: {type: number}
        public: {type: boolean}
        tags: {type: array, items: {type: string}}
        labels: {type: object, additionalProperties: {type: string}}
        build:
          type: object
          properties:
            commit_id: {type: string}
`, 1)
	if err := os.WriteFile(contract, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(root); err != nil {
		t.Fatal(err)
	}
	types := read(t, filepath.Join(root, "api", "zz_generated.go"))
	for _, want := range []string{
		"Name string `json:\"name\"`", "SizeGB int32 `json:\"size_gb\"`", "Ratio float64 `json:\"ratio,omitzero\"`",
		"Public bool `json:\"public,omitzero\"`", "Tags []string `json:\"tags,omitzero\"`",
		"Labels map[string]string `json:\"labels,omitzero\"`", "Build ImageBuild `json:\"build,omitzero\"`",
		"type ImageBuild struct", "CommitID string `json:\"commit_id,omitzero\"`",
	} {
		if !strings.Contains(strings.Join(strings.Fields(types), " "), want) {
			t.Errorf("api/zz_generated.go has no %q", want)
		}
	}
	h := read(t, filepath.Join(root, "internal/compute/zz_generated.go"))
	if !strings.Contains(h, "ListImages(ctx context.Context, os string) ([]api.Image, error)") || !strings.Contains(h, `c.Query("os")`) {
		t.Errorf("handlers:\n%s", h)
	}
	parses(t, root)
}

func TestGenerate_refuses(t *testing.T) {
	for name, edit := range map[string][2]string{
		"no operationId": {"paths: {}", "paths:\n  /apis/compute.infrapi.io/v1/x:\n    get:\n      responses: {'200': {description: x}}"},
		"another group":  {"paths: {}", "paths:\n  /apis/other.infrapi.io/v1/x:\n    get:\n      operationId: x\n      responses: {'200': {description: x}}"},
		"no group":       {"  x-infrapi-group: compute.infrapi.io\n", ""},
		"bad title":      {"title: compute", "title: Compute"},
		"bad schema":     {"    Problem:\n      type: object", "    problem:\n      type: object"},
	} {
		root := newProject(t, Options{Name: "compute"})
		contract := filepath.Join(root, "api", "compute-v1.yaml")
		if err := os.WriteFile(contract, []byte(strings.Replace(read(t, contract), edit[0], edit[1], 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(root); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestExported(t *testing.T) {
	for in, want := range map[string]string{
		"listVMs": "ListVMs", "memory_mb": "MemoryMB", "created_at": "CreatedAt", "vm": "VM", "vms": "VMs",
		"api_url": "APIURL", "replaceVM": "ReplaceVM", "dns-records": "DNSRecords", "size": "Size",
	} {
		if got := exported(in); got != want {
			t.Errorf("exported(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"namespace": "namespace", "vm_name": "vmName", "type": "typeParam", "id": "id", "ctx": "ctxParam", "os": "os", "vms": "vms"} {
		if got := ident(in); got != want {
			t.Errorf("ident(%q) = %q, want %q", in, got, want)
		}
	}
}

// insert keeps the other lines, comments and blank lines included.
func TestInsert(t *testing.T) {
	src := "a: 1\n\n# b's\nb:\n  x: 1   # x\n\n  # y's\n  y: 2\n\n# c's\nc: {}\nd: 3\n"
	out, err := insert([]byte(src), []string{"b"}, "z: 3\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "a: 1\n\n# b's\nb:\n  x: 1   # x\n\n  # y's\n  y: 2\n  z: 3\n\n# c's\nc: {}\nd: 3\n"
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
	out, err = insert([]byte(src), []string{"c"}, "k: v\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\nc:\n  k: v\nd: 3\n") {
		t.Errorf("into an empty map:\n%s", out)
	}
	if _, err := insert([]byte(src), []string{"a"}, "k: v\n"); err == nil {
		t.Error("into a scalar")
	}
}

func TestFindRoot(t *testing.T) {
	root := newProject(t, Options{Name: "compute"})
	got, err := FindRoot(filepath.Join(root, "internal", "compute"))
	if err != nil || got != root {
		t.Errorf("FindRoot: %s, %v", got, err)
	}
	if _, err := FindRoot(t.TempDir()); err == nil {
		t.Error("outside a project")
	}
}
