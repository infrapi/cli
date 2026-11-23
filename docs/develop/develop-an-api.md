---
title: Develop an API
description: Grow an API project from its contract - add a resource, generate its Go code, write it, register it in Kayra, create a namespace, call it as one of its users, then test, ship and release it.
weight: 20
---

# Develop an API

This grows the project of [Getting started](getting-started.md), `compute`,
into an API of virtual machines, from its contract to a user of a namespace
creating one through the gateway:

1. [Start](#1-start)
2. [Add a resource](#2-add-a-resource)
3. [Describe it in the contract](#3-describe-it-in-the-contract)
4. [Run it, register it](#4-run-it-register-it)
5. [Create a namespace](#5-create-a-namespace)
6. [Subscribe the namespace](#6-subscribe-the-namespace)
7. [Grant the namespace's users access](#7-grant-the-namespaces-users-access)
8. [Call it](#8-call-it)
9. [Write it](#9-write-it)
10. [Change it](#10-change-it)
11. [Ship it](#11-ship-it)

Every command runs in the project made by `infrapi-cli app init compute` with
its defaults: group `compute.infrapi.io`, Kayra's domain `infrapi.io`,
platform `sandbox`, the gateway on `localhost:9080`. With other answers,
every `compute.infrapi.io` below is your group, and `catalog.infrapi.io`...
Kayra's `catalog.<domain>`.

## 1. Start

Follow [Getting started](getting-started.md) to its end: infrapi-cli
installed, the project created, its stack started, the contexts saved. In
short:

> [!TIP]
> The commands below use `infrapi-cli`: [install it](https://infrapi.github.io/cli/getting-started/#1-install).

```bash
infrapi-cli app init compute --no-prompt --module github.com/acme/compute
cd compute
infrapi-cli dev init
infrapi-cli dev up
infrapi-cli dev contexts
```

`infrapi-cli dev contexts` saves three contexts, all on the gateway:
`dev-admin`, a sysadmin and the current one, `dev-jdoe`, a member of the
namespace's group `sandbox-acme-admins`, and `dev-nobody`, who belongs to
nothing (see [the stack](dev-stack.md#users-and-tokens)). The `curl`
examples read their tokens:

```bash
ADMIN=$(infrapi-cli auth context token dev-admin)
JDOE=$(infrapi-cli auth context token dev-jdoe)
NOBODY=$(infrapi-cli auth context token dev-nobody)
GW=http://localhost:9080
META='"metadata": {"namespace": "acme", "region": "eu-west-1", "platform": "sandbox"}'
```

## 2. Add a resource

```bash
infrapi-cli app add vm
# singular vm, plural vms
# api/compute-v1.yaml updated
# api/zz_generated.go written
# internal/compute/zz_generated.go written
# internal/compute/vms.go written
# internal/compute/vms_test.go written
#
#   operations: ListVMs, CreateVM, GetVM, ReplaceVM, DeleteVM, each answering 501 until written.
```

It adds to the contract, `api/compute-v1.yaml`, the paths of `vms` and a
schema, `VM`:

| Path | Operations |
|---|---|
| `/apis/compute.infrapi.io/v1/namespaces/{namespace}/vms` | `GET` `listVMs`, `POST` `createVM` |
| `/apis/compute.infrapi.io/v1/namespaces/{namespace}/vms/{name}` | `GET` `getVM`, `PUT` `replaceVM`, `DELETE` `deleteVM` |

Then it generates the Go code from the contract, and writes the resource's
operations in `internal/compute/vms.go`, each answering
`501 Not Implemented`:

```go
// GetVM: Get a vm.
func (s *Service) GetVM(ctx context.Context, namespace, name string) (api.VM, error) {
	return api.VM{}, notImplemented("GetVM")
}
```

`vms.go` and `vms_test.go` are yours: `app add` writes them once, and
refuses a resource the contract has already. `app add vms` does the same:
the singular or the plural, the other one is inferred. When it is
inferred wrong, `--singular` and `--plural` set it, `--kind` names the
schema (`VM`), and `--cluster` puts the resource outside the namespaces,
`/apis/compute.infrapi.io/v1/vms`.

## 3. Describe it in the contract

A `VM` has a `name` and a `created_at` so far. Its own fields go in the
contract, under `components.schemas.VM.properties`:

```yaml
    VM:
      type: object
      description: A vm.
      required: [name]
      properties:
        name:
          type: string
          pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
          maxLength: 63
        created_at:
          type: string
          format: date-time
          readOnly: true
        cpus:
          type: integer
          minimum: 1
          maximum: 64
        size:
          type: string
          enum: [small, medium, large]
```

Then generate the Go code again:

```bash
infrapi-cli app generate   # what make generate and go generate ./... run too
```

`api/zz_generated.go` now has:

```go
type VM struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	CPUs      int64     `json:"cpus,omitzero"`
	Size      string    `json:"size,omitzero"`
}
```

The contract is the API: infrapi/lib serves it under
`/apis/compute.infrapi.io/v1/-/openapi/compute/v1.yaml` and in `-/docs`, and
checks every request against it before any handler runs (`server.WithOpenAPI`):
a `cpus` of 100 or a `size` of `huge` is answered `400` without your code
seeing it. What the generator reads:

| In the contract | In Go |
|---|---|
| `info.title`, `compute` | the package of the implementation, `internal/compute` |
| `info.x-infrapi-group`, and every path under `/apis/<group>/v1` | `api.Group`, `api.Roots` |
| a schema of `components.schemas` | a type of package `api`; an inline object, a type of its own (`VMDisk`) |
| `string`, `integer`, `number`, `boolean`, `array`, `object` | `string` (`time.Time` for `date-time`), `int64` (`int32`), `float64`, `bool`, `[]T`, `map[string]T` |
| a property not in `required` | `omitzero`: left out of the JSON when empty |
| an operation's `operationId`, `getVM` | a method of `Handlers`, `GetVM` |
| its path and query parameters, its JSON body | its arguments, `namespace, name string, body api.VM` |
| its first `2xx` response | its result and status: `201` for `createVM`, none for `204` |

`internal/compute/zz_generated.go` has `Handlers`, one method per
operation, and `Register`, their routes. `cmd/compute/main.go` registers
`compute.New()`, whose `Service` must have every method: an operation added
to the contract by hand is a compile error until written.

## 4. Run it, register it

In a second terminal:

```bash
infrapi-cli app run
# …	info	compute	serving	{"address": "0.0.0.0:8080", "roots": ["/apis/compute.infrapi.io/v1"], "auth": "http://localhost:9080"}
```

`infrapi-cli app run` runs `go run ./cmd/compute` with `.infrapi-dev/app.env`, its
settings rendered from `dev.yaml`: infrapi/lib's `INFRAPI_APP_*`, and
`COMPUTE_AUTH_URL`, Kayra's URL through the gateway. It listens on
`0.0.0.0:8080` (`app.port`), for APISIX to reach it from its container.

Then register it:

```bash
infrapi-cli app register
# gateway: /apis/compute.infrapi.io/* -> host.containers.internal:8080
# kayra: compute.infrapi.io registered, its spec read again, on http://localhost:9080
```

It routes the gateway to your API, then registers it in Kayra, as the
current context, `dev-admin`. Kayra's reconciler reads the API's spec,
`/apis/compute.infrapi.io/v1/-/openapi.json`, the contract and infrapi/lib's
own `-/` endpoints, through the gateway, and learns its resources:

**infrapi-cli**

```bash
infrapi-cli apis --api-group compute
# NAME  SHORTNAME  APIGROUP            NAMESPACED  VERBS
# vms   compute    compute.infrapi.io  true        create,delete,get,list,update
```

**curl**

```bash
curl -s $GW/apis -H "Authorization: Bearer $ADMIN" | jq '.[] | select(.api == "compute.infrapi.io") | .resources'
```

The registration also gives the API an identity of its own,
`system:api:compute.infrapi.io`: Kayra reads the public half of its key at
`/apis/compute.infrapi.io/v1/-/jwks.json`, which the API serves from
`INFRAPI_IDENTITY_KEY_PATH`, the dev stack's `keys/app-identity.pem`. With
it, your code gets tokens as the API, to call Kayra or another API: see
[API identity](https://infrapi.github.io/kayra/architecture/auth/api-identity/).

`infrapi-cli app register` is two calls, which you make yourself where there
is no dev stack, its route set by whoever runs the gateway:

**infrapi-cli**

```bash
infrapi-cli dev api add       # the route: /apis/compute.infrapi.io/* -> your API, dev.yaml's app.port
infrapi-cli create apiregistrations \
  -d '{"name": "compute.infrapi.io", "short_name": "compute", "version": "v1", "active": true, "owner": "dev",
      "identity": {"jwks_from": "spec"}}'
```

**curl**

```bash
curl -s -X POST $GW/apis/catalog.infrapi.io/v1/apiregistrations \
  -H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json" \
  -d '{"name": "compute.infrapi.io", "short_name": "compute", "version": "v1", "active": true, "owner": "dev",
      "identity": {"jwks_from": "spec"}}'
```

## 5. Create a namespace

As `dev-admin`: a namespace, `acme`, and the cluster RBAC that lets
its admins manage its subscriptions and its own RBAC. A namespace is not a
tenant: a team or a customer owns one or several, `acme-prod` and
`acme-dev` say (see [Multi-tenancy](https://infrapi.github.io/kayra/architecture/multi-tenancy/)).

**infrapi-cli**

```bash
infrapi-cli create namespaces -d '{"name": "acme"}'

infrapi-cli create clustergroups \
  -d "{\"name\": \"acme-admins\", $META, \"groups\": [\"sandbox-core-sysadmin\", \"sandbox-acme-admins\"]}"

infrapi-cli create clusterroles \
  -d "{\"name\": \"acme-admin-role\", $META, \"rules\": [{\"apis\": [\"catalog.infrapi.io\", \"authorization.infrapi.io\"], \"resources\": [\"*\"], \"verbs\": [\"*\"]}]}"

infrapi-cli create clusterrolebindings \
  -d "{\"name\": \"acme-admin-binding\", $META, \"role\": \"acme-admin-role\", \"groups\": [\"acme-admins\"]}"
```

**curl**

```bash
H=(-H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json")

curl -s -X POST $GW/apis/system.infrapi.io/v1/namespaces "${H[@]}" -d '{"name": "acme"}'

curl -s -X POST $GW/apis/authorization.infrapi.io/v1/clustergroups "${H[@]}" \
  -d "{\"name\": \"acme-admins\", $META, \"groups\": [\"sandbox-core-sysadmin\", \"sandbox-acme-admins\"]}"

curl -s -X POST $GW/apis/authorization.infrapi.io/v1/clusterroles "${H[@]}" \
  -d "{\"name\": \"acme-admin-role\", $META, \"rules\": [{\"apis\": [\"catalog.infrapi.io\", \"authorization.infrapi.io\"], \"resources\": [\"*\"], \"verbs\": [\"*\"]}]}"

curl -s -X POST $GW/apis/authorization.infrapi.io/v1/clusterrolebindings "${H[@]}" \
  -d "{\"name\": \"acme-admin-binding\", $META, \"role\": \"acme-admin-role\", \"groups\": [\"acme-admins\"]}"
```

## 6. Subscribe the namespace

A namespace uses an API once subscribed. The reconciler then runs the `dev`
pipeline ([the stack](dev-stack.md#the-reconcile-pipeline)) and activates
the subscription.

**infrapi-cli**

```bash
infrapi-cli create subscriptions -n acme -d '{"api": "compute.infrapi.io"}'

# a few seconds later
infrapi-cli get subscriptions -n acme compute.infrapi.io -o json
# {"api": "compute.infrapi.io", "active": true, "state": "success", "synced": true, …}
```

**curl**

```bash
curl -s -X POST $GW/apis/catalog.infrapi.io/v1/namespaces/acme/subscriptions "${H[@]}" \
  -d '{"api": "compute.infrapi.io"}'

# a few seconds later
curl -s $GW/apis/catalog.infrapi.io/v1/namespaces/acme/subscriptions/compute.infrapi.io "${H[@]}"
# {"api":"compute.infrapi.io","active":true,"state":"success","synced":true,…}
```

`infrapi-cli dev logs reconciler` shows it at work:

```text
catalog: reconciler: … stored openapi spec of "compute.infrapi.io" (1 resources)
catalog: reconciler: … step 0 POST http://apisix:9080/hooks/compute.infrapi.io/namespaces/acme/sync -> 200 …
catalog: reconciler: … step 1 GET http://apisix:9080/hooks/compute.infrapi.io/namespaces/acme/sync/… -> 200 …
catalog: reconciler: … activated subscription acme/compute.infrapi.io
```

## 7. Grant the namespace's users access

`jdoe` is in the namespace's group, but nothing in the namespace grants it
`vms` yet: Kayra's check refuses them.

**infrapi-cli**

```bash
infrapi-cli get vms -n acme --context dev-jdoe
# Error: Forbidden (403): you are not a member of namespace "acme": neither your user id nor any of your groups is listed in any of its groups
```

**curl**

```bash
curl -s $GW/apis/compute.infrapi.io/v1/namespaces/acme/vms -H "Authorization: Bearer $JDOE"
# {"title":"Forbidden","status":403,"detail":"you are not a member of namespace \"acme\": …"}
```

The namespace's own RBAC grants it: a group of the namespace naming the
identity provider's group, a role on the API's resources, a binding of the
two.

**infrapi-cli**

```bash
infrapi-cli create groups -n acme \
  -d "{\"name\": \"devs\", $META, \"groups\": [\"sandbox-acme-admins\"]}"

infrapi-cli create roles -n acme \
  -d "{\"name\": \"vms-admin\", $META, \"rules\": [{\"apis\": [\"compute.infrapi.io\"], \"resources\": [\"vms\"], \"verbs\": [\"*\"]}, {\"apis\": [\"events.infrapi.io\"], \"resources\": [\"events\"], \"verbs\": [\"get\", \"list\"]}]}"

infrapi-cli create rolebindings -n acme \
  -d "{\"name\": \"devs-vms-admin\", $META, \"role\": \"vms-admin\", \"groups\": [\"devs\"]}"

infrapi-cli auth can-i create vms --namespace acme --context dev-jdoe
# yes
```

**curl**

```bash
NS=$GW/apis/authorization.infrapi.io/v1/namespaces/acme

curl -s -X POST $NS/groups "${H[@]}" \
  -d "{\"name\": \"devs\", $META, \"groups\": [\"sandbox-acme-admins\"]}"

curl -s -X POST $NS/roles "${H[@]}" \
  -d "{\"name\": \"vms-admin\", $META, \"rules\": [{\"apis\": [\"compute.infrapi.io\"], \"resources\": [\"vms\"], \"verbs\": [\"*\"]}, {\"apis\": [\"events.infrapi.io\"], \"resources\": [\"events\"], \"verbs\": [\"get\", \"list\"]}]}"

curl -s -X POST $NS/rolebindings "${H[@]}" \
  -d "{\"name\": \"devs-vms-admin\", $META, \"role\": \"vms-admin\", \"groups\": [\"devs\"]}"

curl -s -X POST $GW/apis/authorization.infrapi.io/v1/check -H "Authorization: Bearer $JDOE" -H "Content-Type: application/json" \
  -d '{"namespace": "acme", "api": "compute.infrapi.io", "resource": "vms", "verb": "create"}'
# {"allowed":true}
```

A role names only resources the API's spec declares: Kayra answers 404 for
any other. Its second rule lets the namespace's users read its
[events](https://infrapi.github.io/kayra/architecture/events/).

## 8. Call it

As `jdoe`, through the gateway. Every operation is routed, checked against
the contract, authorized by Kayra, and answers 501: none is written yet.

**infrapi-cli**

```bash
infrapi-cli create vms -n acme -d '{"name": "web-1", "cpus": 2}' --context dev-jdoe
# Error: Not Implemented (501): CreateVM is not implemented yet

infrapi-cli create vms -n acme -d '{"name": "web-1", "cpus": 100}' --context dev-jdoe
# Error: Invalid request (400): request body has an error: doesn't match schema #/components/schemas/VM: Error at "/cpus": number must be at most 64

infrapi-cli get vms -n acme --context dev-nobody
# Error: Forbidden (403): you are not a member of namespace "acme": …
```

**curl**

```bash
VMS=$GW/apis/compute.infrapi.io/v1/namespaces/acme/vms

curl -s -X POST $VMS -H "Authorization: Bearer $JDOE" -H "Content-Type: application/json" -d '{"name": "web-1", "cpus": 2}'
# {"title":"Not Implemented","status":501,"detail":"CreateVM is not implemented yet",…}

curl -s $VMS -H "Authorization: Bearer $NOBODY"
# {"title":"Forbidden","status":403,…}
```

Every write the API served left an event: the API reports it to Kayra
itself, with its own identity (`INFRAPI_EVENTS_ENABLED` in
`.infrapi-dev/app.env`). The refused calls left none: they are in Kayra's
audit, `infrapi-cli dev logs kayra`.

**infrapi-cli**

```bash
infrapi-cli get events -n acme --context dev-jdoe
# LAST SEEN  TYPE     REASON          OBJECT     COUNT  NOTE
# 12s        Warning  VMCreateFailed  vms/web-1  1      CreateVM is not implemented yet
```

**curl**

```bash
curl -s $GW/apis/events.infrapi.io/v1/namespaces/acme/events -H "Authorization: Bearer $JDOE"
```

The reason comes from the operation, `createVM`: `VMCreated` once it is
written. A worker records its own with the API's recorder: see
[lib's events package](https://infrapi.github.io/lib/packages/events/).

## 9. Write it

An operation is a method of `Service`, in `internal/compute/vms.go`: it
gets the request's parameters and body, already checked, and returns its
result or an error. An infrapi/lib error
([pkg/errors](https://infrapi.github.io/lib/packages/errors/)) is answered
with its status as a problem document; any other error is a `500`.

Here, the VMs kept in memory: they go with the process, enough to try the
API. A real one calls what makes VMs, a cloud's SDK say: see
[Build an EC2 instance API](https://infrapi.github.io/learn/build-an-ec2-api-with-lib/).

`internal/compute/compute.go`, the store as the service's field:

```go
type Service struct {
	mu  sync.Mutex
	vms map[string]map[string]api.VM // namespace -> name -> VM
}

func New() *Service {
	return &Service{vms: map[string]map[string]api.VM{}}
}
```

`internal/compute/vms.go`, two operations written, the others still 501:

```go
func (s *Service) ListVMs(ctx context.Context, namespace string) ([]api.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []api.VM
	for _, vm := range s.vms[namespace] {
		out = append(out, vm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) CreateVM(ctx context.Context, namespace string, body api.VM) (api.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.vms[namespace][body.Name]; ok {
		return api.VM{}, infraerrors.AlreadyExists(cur, "vm %q already exists in namespace %q", body.Name, namespace)
	}
	if s.vms[namespace] == nil {
		s.vms[namespace] = map[string]api.VM{}
	}
	body.CreatedAt = time.Now().UTC()
	s.vms[namespace][body.Name] = body
	return body, nil
}
```

`infraerrors` is `github.com/infrapi/lib/pkg/errors`. A nil list is
answered `[]`. `vms_test.go` expects 501 from every operation: change the
two written ones to `http.StatusOK` and `http.StatusCreated`, then
`make test`. Restart `infrapi-cli app run`, and call it again:

**infrapi-cli**

```bash
infrapi-cli create vms -n acme -d '{"name": "web-1", "cpus": 2, "size": "small"}' --context dev-jdoe
# NAMESPACE  NAME   CREATED AT                 UPDATED AT
# acme  web-1  2026-11-17T20:57:14+01:00  -

infrapi-cli get vms -n acme -o json --context dev-jdoe
# [{"name": "web-1", "created_at": "2026-11-17T19:57:14Z", "cpus": 2, "size": "small"}]

infrapi-cli create vms -n acme -d '{"name": "web-1"}' --context dev-jdoe
# Error: Already exists (409): vm "web-1" already exists in namespace "acme"

infrapi-cli get vms -n acme web-1 --context dev-jdoe
# Error: Not Implemented (501): GetVM is not implemented yet
```

**curl**

```bash
curl -s -X POST $VMS -H "Authorization: Bearer $JDOE" -H "Content-Type: application/json" \
  -d '{"name": "web-1", "cpus": 2, "size": "small"}'
# {"name":"web-1","created_at":"2026-11-17T19:57:14Z","cpus":2,"size":"small"}

curl -s $VMS -H "Authorization: Bearer $JDOE"
# [{"name":"web-1","created_at":"2026-11-17T19:57:14Z","cpus":2,"size":"small"}]
```

## 10. Change it

A field, an operation or a resource more: change the contract (or
`infrapi-cli app add <resource>`), then:

```bash
infrapi-cli app generate   # the Go code from the contract
go build ./...             # an operation not written yet is a compile error: write it in internal/compute
infrapi-cli app register   # Kayra reads the spec again; restart infrapi-cli app run first
```

Then grant the new resource in a role, as in step 7. `infrapi-cli app
register` registers the API again with a new `sync_id`: Kayra reads its
spec, and resyncs every subscribed namespace.

The `dev` pipeline does nothing on the API's side, calling the gateway's
`hooks` stub: an API that provisions something per namespace serves its own
sync endpoints, and its registration names a reconcile mode calling them
(see Kayra's
[subscriptions](https://github.com/infrapi/kayra/blob/main/docs/architecture/subscriptions.md#http-step-pipeline)).

## 11. Ship it

| | Run | |
|---|---|---|
| format, lint, test | `make fmt lint test` | golangci-lint with the project's `.golangci.yml`; the `zz_generated.go` files are left out |
| build | `make build` | `bin/compute` |
| everything a release builds, locally | `make snapshot` | GoReleaser: archives, deb and rpm packages, the image, in `dist/` |

**The CI**, `.github/workflows/ci.yml` or `.gitlab-ci.yml`, imports
[infrapi/ci](https://github.com/infrapi/ci): on a push or a pull request,
lint, test and build; on a tag `v<x.y.z>`, the release, GoReleaser reading
`.goreleaser.yaml`. Its inputs are in the file: the binary's name, the
image, `goprivate`.

```bash
git tag v0.1.0 && git push --tags     # the release
```

**What a release builds** is in `.goreleaser.yaml`, written by `app init`
from the packages chosen:

| | What | Where |
|---|---|---|
| archives | `compute_<version>_<os>_<arch>.tar.gz`, linux and darwin, amd64 and arm64 | the release |
| deb, rpm | `/usr/bin/compute`, a systemd unit, its settings in `/etc/compute/compute.env` | the release |
| image | Alpine, the binary, its settings from `/etc/compute/compute.env`; linux/amd64 and arm64 | the registry, `ghcr.io/acme/compute` |

The packages and the image read a dotenv file, `/etc/compute/compute.env`,
as `infrapi-cli app run` reads `.infrapi-dev/app.env`: `packaging/compute.env` is its
template.

**Private modules**: a module on a host the Go proxy can't read,
`gitlab.example.com/team/compute` say, is fetched from its repository.
`app init --goprivate 'gitlab.example.com/*'` sets `GOPRIVATE` in the
Makefile and the CI, and the CI reads a token for it, `GOPRIVATE_TOKEN`
(see infrapi/ci). On your machine, `git` needs the same access:
`git config --global url."git@gitlab.example.com:".insteadOf "https://gitlab.example.com/"`.

Next: [The dev stack](dev-stack.md), how the stack you used here works,
and how to change it or start over.
