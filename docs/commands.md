---
title: Commands
description: Reference for infrapi-cli's commands against a Kayra server - auth (login, logout, context, can-i, whoami), apis, and the get, create, update, patch and delete verbs.
weight: 30
---

# Commands

Every command here talks to a Kayra server's own API, through a saved
context (`auth login`, `auth context set`). The dev stack's commands are in
[dev](dev.md).

## Colors

On a terminal, infrapi-cli colors what it prints: tables' headers, errors
and warnings, JSON and YAML (`-o json`, `-o yaml`), and the dev stack's
diffs. It uses the terminal's own 16 colors, so they follow its theme,
light or dark.

Colors are off, with no escape sequence at all, when:

- the output is not a terminal: a pipe, a file, a script reading it;
- `NO_COLOR` is set to anything but an empty value
  ([no-color.org](https://no-color.org));
- `--no-color` is given, a global flag, before or after the command.

```bash
infrapi-cli get apiregistrations -o json | jq .   # plain: a pipe
NO_COLOR=1 infrapi-cli apis                               # plain
infrapi-cli apis --no-color                               # plain
```

## Behind a proxy

infrapi-cli takes its proxy from the environment, as curl and git do:

| Variable | |
|---|---|
| `https_proxy`, `HTTPS_PROXY` | the proxy for `https://` URLs |
| `http_proxy`, `HTTP_PROXY` | the proxy for `http://` URLs |
| `no_proxy`, `NO_PROXY` | hosts, domains (`.example.com`) or CIDRs reached directly, comma-separated |

```bash
export https_proxy=http://proxy.example.com:3128   # http://user:password@... when it asks for one
export http_proxy=$https_proxy
export no_proxy=.example.com,10.0.0.0/8
```

Every call goes through it: Kayra's API, `auth login`, `app register`. `go mod
tidy`, after `app init`, reads them too.
`localhost` and loopback addresses are always reached directly, so the
dev stack's own calls (Kayra, the gateway, APISIX's Admin API) never go
through it.

For the dev stack:

- `dev doctor` shows the proxy variables set, a password hidden;
- `dev build` passes them to the image build, for its Go modules, by name
  only (`--build-arg HTTPS_PROXY`): the command printed shows no password;
- `dev example` and `dev check` run `go`, which reads them too;
- podman pulls images with them. Docker pulls through its daemon, which
  has a proxy setting of its own
  ([Docker's daemon proxy](https://docs.docker.com/engine/daemon/proxy/)).

The containers of the stack don't get them: they only talk to each other.

## Logging in (auth)

`infrapi-cli auth` is everything about the login itself: `auth login`, `auth logout`,
the saved logins (`auth context`, or `auth ctx`), the logins at the server
(`auth sessions`), and what a server says of the current one
(`auth can-i`, `auth whoami`).

The portable, end-user client of a Kayra server — hand someone this
binary and a server URL, nothing else. It never reads `kayra.yaml`, has no `--config`
flag, and never needs to know which `authentifications.<name>` a server
configures: every command here only ever talks to that server's own API.

### login

Logs in at a Kayra server you only know the URL of, through the browser:
the server's [authorization server](https://infrapi.github.io/kayra/architecture/auth/oauth2-server/)
sends you to its login provider (Okta, say), and the context keeps the
server's own tokens. The access token lasts 15 minutes; every command
renews it before it expires, with the refresh token, without the browser,
for as long as the login lasts (30 days, a week unused, by default).

> [!TIP]
> The commands below use `infrapi-cli`: [install it](getting-started.md#1-install).

```bash
infrapi-cli auth login --server https://kayra.example.com
# oauth2login: opening the browser to log in:
#
#   https://kayra.example.com/oauth2/authorize?...
#
# infrapi-cli: logged in — saved context "kayra.example.com" to ~/.config/infrapi/config (now current)
# infrapi-cli: run `infrapi-cli auth context token` to print the bearer token
```

The token itself is never printed — it's already in the context file this
just wrote; read it back with [`auth context token`](#context) whenever you
actually need it (renewed first, when it is about to expire).

Nothing about the server but its URL is saved: what the commands need of
it, they discover — its authorization server from
`/.well-known/oauth-authorization-server`, its check endpoint and own
groups from [`/.well-known/kayra.json`](https://infrapi.github.io/kayra/packages/discovery/),
every API group's full name and short name from `GET /apis`.

| Flag | Default | Meaning |
|------|---------|---------|
| `--server` | — | required; the Kayra server's base URL |
| `--context-name` | sanitized `--server` host | name to save this login under (see [context](#context)) |
| `--timeout` | `2m` (`10m` with `--no-browser`) | how long to wait for the browser round trip |
| `--no-browser` | off | log in from a terminal with no usable browser — see below |
| `--service-account` | — | log in as a service account, `<namespace>/<name>`, with `--id-token`: see [As a service account](#as-a-service-account-ci) |
| `--id-token` | — | where the platform's identity token is: `env:<NAME>`, `file:<PATH>` or `github` |

#### Without a browser (SSH, container, air-gapped host)

`--no-browser` starts no local listener and opens nothing. It prints the
login URL; open it in a browser on **any** machine, log in as usual, and
the server shows a code instead of redirecting back to `127.0.0.1`. Paste
that code at the prompt:

```bash
infrapi-cli auth login --server https://kayra.example.com --no-browser
# oauth2login: open this URL in a browser on any machine to log in:
#
#   https://kayra.example.com/oauth2/authorize?...redirect_uri=urn%3Aietf%3Awg%3Aoauth%3A2.0%3Aoob...
#
# Paste the code shown in the browser: kq3V...
# infrapi-cli: logged in — saved context "kayra.example.com" to ~/.config/infrapi/config (now current)
```

The code is safe to carry across by hand: it is redeemable only with the
PKCE verifier held by the terminal that printed the URL, once, within a
minute. Paste it only into a terminal where you started the login
yourself.

#### As a service account (CI)

A CI job logs in as one of Kayra's
[service accounts](https://infrapi.github.io/kayra/architecture/auth/service-accounts/)
with its platform's own identity token, with no stored secret. The service
account must have a federated credential matching the job (see
[Federation](https://infrapi.github.io/kayra/architecture/auth/federation/)).

```bash
# GitLab CI: the job's id_tokens entry, aud Kayra's URL
infrapi-cli auth login --server https://kayra.example.com --service-account core/deployer --id-token env:KAYRA_ID_TOKEN
# GitHub Actions, with permissions: id-token: write
infrapi-cli auth login --server https://kayra.example.com --service-account core/deployer --id-token github
# a projected Kubernetes service account token
infrapi-cli auth login --server https://kayra.example.com --service-account core/deployer --id-token file:/var/run/secrets/kayra/token
# infrapi-cli: logged in: as system:serviceaccount:core:deployer, saved context kayra.example.com (now current)
```

The token lasts 15 minutes and has no refresh token. When it expires, the
next command asks for a new one with the identity token again (the context
keeps where it is read), for as long as that one is valid: the job's
lifetime.

### logout

Revokes the current login at its server (or `--context`'s): its refresh
token, and with it every token of that login. The context stays, its
server and namespace; `auth login` again to use it.

```bash
infrapi-cli auth logout
# infrapi-cli: logged out of kayra.example.com
```

See [oauth2login](packages.md#oauth2login) for how the flow itself works.

### sessions

Lists your logins at the server, infrapi-cli's on each machine and an MCP
client's such as Claude's, the latest used first, the current context's
marked `*`. `revoke` ends one, or every one with `--all`: its tokens are
refused at once. Revoking the current one logs this context out too.
`--user` names another user's, when your cluster roles grant `list` or
`delete` on `sessions`, as the sysadmin's do.

```bash
infrapi-cli auth sessions
# CURRENT  ID                      CLIENT                                              USER  CREATED  LAST USED  EXPIRES
# *        cUxSnWX-cf-L4RWydyruzA  infrapi-cli                                         jdoe  2h       now        2026-11-26 20:22:15
#          ECshjkCuuAl44zKe3Z4U6Q  https://claude.ai/oauth/mcp-oauth-client-metadata   jdoe  3d       1h         2026-11-25 19:10:02
infrapi-cli auth sessions revoke ECshjkCuuAl44zKe3Z4U6Q
# infrapi-cli: revoked session ECshjkCuuAl44zKe3Z4U6Q
infrapi-cli auth sessions revoke --all --user asmith
# infrapi-cli: revoked 2 session(s)
```

### context

`infrapi-cli auth context`, or `auth ctx`, manages `~/.config/infrapi/config` — the file `auth login` writes to on every
successful login ([internal/clicontext](packages.md#clicontext)). Its
shape mirrors `kubectl`'s own `~/.kube/config`: a map of named contexts
(server, token, expiry) plus a `current_context`, so switching back to an
earlier login never requires authenticating again. Override the path with
`$INFRAPI_CONTEXT_FILE`.

```bash
infrapi-cli auth login --server https://kayra.internal
infrapi-cli auth login --server https://kayra-staging.internal --context-name staging

infrapi-cli auth context list
# CURRENT  NAME            PROVIDER  SERVER                          NAMESPACE  EXPIRY
#          kayra.internal  okta      https://kayra.internal          acme  2026-11-03T16:04:05+01:00
# *        staging         okta      https://kayra-staging.internal  -          2026-11-03T17:04:05+01:00

# save an existing token (e.g. a `kayra token mint` bootstrap token) without logging in
infrapi-cli auth context set bootstrap --server http://localhost:8080 --token "$TOKEN"

infrapi-cli auth context use kayra.internal  # switch back without logging in again
infrapi-cli auth context current               # show the active context's details
infrapi-cli auth context token                 # print the active context's bearer token
infrapi-cli auth context token staging         # print a specific context's token
infrapi-cli auth context delete staging        # drop a saved login
```

Like `kubens`, a context keeps the namespace commands address when they
name none: `-n` overrides it for one command, and a cluster-scoped
resource ignores it. A new login, or `set` again, keeps it.

```bash
infrapi-cli auth ctx ns acme        # the current context now works in acme
infrapi-cli get vms                      # .../namespaces/acme/vms
infrapi-cli get vms -n core              # .../namespaces/core/vms, this once
infrapi-cli auth ctx ns                  # print it: acme
infrapi-cli auth ctx ns --unset          # forget it
```

| Subcommand | Args | Meaning |
|------------|------|---------|
| `list` | — | list saved contexts, marking the current one with `*` |
| `current` | — | print the current context's name, server, expiry |
| `set` | `<name>` | save `--token` for `--server` as a context and make it current — no login flow |
| `use` | `<name>` | switch the current context |
| `delete` | `<name>` | remove a saved context |
| `namespace`, `ns` | `[name]` | set the namespace of the current context (or `--context`'s), print it without `<name>`, forget it with `--unset` |
| `token` | `[name]` | print a context's bearer token (defaults to current) |

#### One call in another context

`--context <name>` runs one command in a saved context, without changing
the current one, like `kubectl --context`. Every command takes it, before
or after itself, and `$INFRAPI_CONTEXT` does the same for a whole shell:

```bash
infrapi-cli auth whoami --context staging          # staging, this once
infrapi-cli --context staging get apiregistrations
INFRAPI_CONTEXT=staging infrapi-cli apis           # or export it for a session
infrapi-cli auth context current                        # still the one you had
```

An unknown name is refused, and nothing is called.

### can-i and whoami

Two checks that ask a server about the current login itself, rather
than managing it. Like every command, they take
[`--context`](#one-call-in-another-context).

#### can-i

Mirrors `kubectl auth can-i`: asks the server's own
[check endpoint](https://infrapi.github.io/kayra/architecture/auth/rbac-and-auth/#who-decides),
the one its [`/.well-known/kayra.json`](https://infrapi.github.io/kayra/packages/discovery/) names,
whether the current context's already-authenticated identity is
granted `<verb>` on `<resource>`, named as
[the verbs name it](#naming-a-resource) — the decision is always
the server's, never asserted locally.

```bash
infrapi-cli auth can-i get pods --namespace core
# yes

infrapi-cli auth can-i delete pods --namespace core
# no
# infrapi-cli: your roles in namespace "core" grant api "compute.infrapi.io", but not "delete" on "pods"
```

Prints `yes`/`no` to stdout and exits `0`/`1` accordingly — scriptable
exactly like `kubectl auth can-i`. A denial's reason (when the server
gives one) goes to stderr, never mixed into stdout.

The check answers every route as that route decides it, Kayra's own
included:

| Asking about... | Decided by |
|---|---|
| an app's resources, with `--namespace` | the namespace's own groups, roles and rolebindings |
| `authorization.<domain>`'s `groups`/`roles`/`rolebindings`, `catalog.<domain>`'s `subscriptions`, with `--namespace` | the cluster bindings scoped to that namespace |
| a cluster-scoped resource, without `--namespace`: `system.<domain>`'s `namespaces`, `catalog.<domain>`'s `apiregistrations`, `authorization.<domain>`'s `cluster*`, an app's cluster-scoped resources | the cluster RBAC: the built-in sysadmin binding, then the global cluster bindings |

```bash
infrapi-cli auth can-i create namespaces
# no
# infrapi-cli: no global cluster role binding binds a cluster group of yours, and you are not a sysadmin
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--namespace`, `-n` | the context's | the namespace of a namespaced resource; a cluster-scoped one has none |

#### whoami

Prints exactly what the server's own `GET /oauth2/userinfo` returns
for the current login — `sub`, `provider`, `authorization`, `groups`,
plus whatever `authorizations.<name>` attributes (`email`, `name`,
...) that backend resolved. This is the same identity any `can-i`
decision, or any other RBAC check against this token, is actually made
against.

```bash
infrapi-cli auth whoami
# authorization:  idm
# email:           jdoe@example.com
# groups:          sandbox-core-sysadmin, sandbox-acme-admins
# name:            John Doe
# provider:        self
# sub:             jdoe
```

| Flag | Default | Meaning |
|------|---------|---------|

### apis

Like `oc api-resources`: every api group the server knows and the resources
each one serves, from the server's `GET /apis` discovery. That covers
Kayra's own groups and every registered app, whose resources come from the
OpenAPI spec the reconciler fetched for it (see
[API registrations](https://infrapi.github.io/kayra/architecture/api-registrations/#openapi-spec)).

```bash
infrapi-cli apis
# NAME                SHORTNAME  APIGROUP                   NAMESPACED  VERBS
# namespaces          -          system.infrapi.io         false       create,delete,get,list,update
# apiregistrations    -          catalog.infrapi.io        false       create,delete,get,list,update
# subscriptions       -          catalog.infrapi.io        true        create,delete,get,list,update
# dashboards          telemetry  telemetry.infrapi.io    true        create,delete,get,list
# -                   -          vault.infrapi.io          -           -
infrapi-cli apis --api-group telemetry --namespaced=true
infrapi-cli apis -o yaml
```

A registered api whose spec hasn't been fetched yet (no sync so far) is
listed with `-`.

| Flag | Meaning |
|------|---------|
| `--api-group` | only this api group: its long name, short name, or first label (`telemetry`, `catalog`) |
| `--namespaced` | only namespaced (`true`) or cluster-scoped (`false`) resources |
| `-o`, `--output` | `table` (default), `json` or `yaml` |

## Calling the API (get, create, update, patch, delete)

kubectl-style top-level verbs — a thin, generic REST client against
*any* app's own API, following the exact route convention Kayra's
own resources use (see
[RBAC & auth](https://infrapi.github.io/kayra/architecture/auth/rbac-and-auth/#who-decides)):

```
{server}/apis/<group>/<version>/namespaces/<namespace>/<resource>[/<name>[/<action>]]   # a namespaced resource
{server}/apis/<group>/<version>/<resource>[/<name>[/<action>]]                          # a cluster-scoped one
```

### Naming a resource

As with `kubectl`, a resource is named by its plural, or singular, alone
(`vms`, `vm`), or by its full name, its plural then its api group
(`vms.compute.infrapi.io`; the group's short name or first label works
too, `vms.compute`). `GET /apis` says which group serves it, at which
version, and whether it is namespaced: a namespaced one is addressed in
`-n`, else the [context's namespace](#context); a cluster-scoped one
never is.
A resource its group serves both ways, Kayra's `events` say, is addressed in
the namespace when one is given, cluster-scoped otherwise.

```bash
infrapi-cli get events -n core                                    # LAST SEEN, TYPE, REASON, OBJECT, COUNT, NOTE
infrapi-cli get events -n core --field-selector type=Warning,regarding.name=web-1
```

When several api groups serve the name, nothing is sent: a warning lists
the command again with each full name.

```bash
infrapi-cli get policies
# infrapi-cli: warning: "policies" is served by several api groups: run the command again with its full name:
#   infrapi-cli get policies.compute.infrapi.io
#   infrapi-cli get policies.legacy.infrapi.io
# infrapi-cli: get: "policies" is served by several api groups: give its full name, one of policies.compute.infrapi.io, policies.legacy.infrapi.io
```

A group discovery doesn't know yet (registered, never synced) is taken as
written in a full name: `secrets.vault.infrapi.io`, in `-n` when given.

Every request is authenticated with the current context's own bearer token. Kayra
itself never implements any of these APIs (see
[API registrations](https://infrapi.github.io/kayra/architecture/api-registrations/)) — this is just
the one HTTP convention every app following it shares, so one verb set
works against all of them, Kayra's own resources included — both
namespace-scoped (`subscription`'s subscriptions, `authorization`'s
per-namespace groups/roles/rolebindings) and cluster-scoped
(`catalog`'s `apiregistrations`, `authorization`'s
`clustergroups`/`clusterroles`/`clusterrolebindings`):

Responses print as a table like `auth context list`'s: `NAMESPACE` (only with
`-n`), `NAME` (the resource's `name`, `api` for a subscription, or `id`),
`CREATED AT` and `UPDATED AT`, in local time. A resource without
timestamps, an app's often, shows its first five string, number or boolean
fields instead, in the order the API sent them:

```bash
infrapi-cli get locations
# NAME              REGION  PLATFORM  DATACENTER
# emea-sandbox-dc1  emea    sandbox   dc1
```

`-o json` or `-o yaml` prints
the full response instead (YAML keeps the server's field order). A response that isn't a named resource (a status
document, say) always prints as JSON, so nothing is hidden. An empty list
prints `No resources found.` on stderr and no rows on stdout. Errors print
on stderr as `Error: <title> (<status>): <detail>`, followed by
`Solution: …` when the server gives one, and the command exits `1`.

Each example's `curl` block is the exact request the verb sends, against
`http://localhost:8080` with `app.api_domain` left at `infrapi.io` —
take the token from the current context with
`TOKEN=$(infrapi-cli auth context token)`.

**infrapi-cli**

```bash
# namespaced: in -n, or the context's namespace
infrapi-cli get subscriptions -n acme
# No resources found.
infrapi-cli create subscriptions -n acme -d '{"api": "vault.infrapi.io"}'
# NAMESPACE  NAME               CREATED AT                 UPDATED AT
# acme  vault.infrapi.io  2026-11-10T14:30:00+01:00  2026-11-10T14:30:00+01:00
infrapi-cli get subscriptions -n acme vault.infrapi.io -o json
# {"api": "vault.infrapi.io", "active": false, "state": "init", ...}
infrapi-cli update subscriptions/sync -n acme vault.infrapi.io
infrapi-cli delete subscriptions -n acme vault.infrapi.io
# subscriptions "vault.infrapi.io" deleted

# cluster-scoped: addressed as .../<resource>, never in a namespace
infrapi-cli create apiregistrations -d '{"name": "vault.infrapi.io", "short_name": "vault", "owner": "sre"}'
infrapi-cli get apiregistrations
infrapi-cli create clustergroups -d '{"name": "g", "metadata": {...}, "groups": [...]}'
```

**curl**

```bash
# namespaced
curl -s http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/acme/subscriptions \
  -H "Authorization: Bearer $TOKEN"
# []
curl -s -X POST http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/acme/subscriptions \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"api": "vault.infrapi.io"}'
# {"api": "vault.infrapi.io", "active": false, "state": "init", ...}
curl -s http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/acme/subscriptions/vault.infrapi.io \
  -H "Authorization: Bearer $TOKEN"
curl -s -X PUT http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/acme/subscriptions/vault.infrapi.io/sync \
  -H "Authorization: Bearer $TOKEN"
curl -s -X DELETE http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/acme/subscriptions/vault.infrapi.io \
  -H "Authorization: Bearer $TOKEN"

# cluster-scoped
curl -s -X POST http://localhost:8080/apis/catalog.infrapi.io/v1/apiregistrations \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "vault.infrapi.io", "short_name": "vault", "owner": "sre"}'
curl -s http://localhost:8080/apis/catalog.infrapi.io/v1/apiregistrations \
  -H "Authorization: Bearer $TOKEN"
curl -s -X POST http://localhost:8080/apis/authorization.infrapi.io/v1/clustergroups \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "g", "metadata": {...}, "groups": [...]}'
```

**OpenTofu/Terraform**

```hcl
# namespaced: its namespace an attribute
resource "kayra_subscription" "vault" {
  namespace = "acme"
  api       = "vault.infrapi.io"
}

# cluster-scoped
resource "kayra_api_registration" "vault" {
  name       = "vault.infrapi.io"
  short_name = "vault"
  owner      = "sre"
}
```

`get` maps to `GET` either way — no trailing `<name>` lists the whole
collection (`.../<resource>`), a `<name>` fetches that one item
(`.../<resource>/<name>`), exactly like `kubectl get` itself (and
exactly how Kayra's own collection vs. item routes already distinguish
the two) — there is no separate `list` verb. `create` is `POST`,
`update` is `PUT`, `patch` is `PATCH`, `delete` is `DELETE` —
`update`/`patch`/`delete` all require `<name>`; `create`/`update`/
`patch` all require a body, but for an action (below).

### Actions

An action on an item is named as Kayra's roles and `auth can-i` name it,
`<resource>/<action>`, then the item: `update subscriptions/sync
vault.infrapi.io` above is `PUT .../subscriptions/vault.infrapi.io/sync`.
The action must be one discovery lists for the resource (`infrapi-cli apis`
shows them); a name is one path segment, never `<name>/<action>`.

**infrapi-cli**

```bash
infrapi-cli update vms/resize web-1 -n acme -d '{"size": "large"}'
infrapi-cli update vms/reboot web-1 -n acme                     # no body
infrapi-cli get apiregistrations/status vault.infrapi.io
infrapi-cli auth can-i update vms/resize -n acme                # the same name
```

**curl**

```bash
curl -s -X PUT http://localhost:8080/apis/compute.infrapi.io/v1/namespaces/acme/vms/web-1/resize \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"size": "large"}'
curl -s -X PUT http://localhost:8080/apis/compute.infrapi.io/v1/namespaces/acme/vms/web-1/reboot \
  -H "Authorization: Bearer $TOKEN"
curl -s http://localhost:8080/apis/catalog.infrapi.io/v1/apiregistrations/vault.infrapi.io/status \
  -H "Authorization: Bearer $TOKEN"
```

An action's body is optional: without `-d` or `-f`, the request has none,
and the API's contract says whether it needs one (a `400` when it does).

### Field selectors

`get --field-selector key=value[,key=value...]`, kubectl's syntax, sends
each pair as a query parameter of the API's own: the filters its
contract declares.

**infrapi-cli**

```bash
infrapi-cli get vms -n acme --field-selector size=large,state=running
```

**curl**

```bash
curl -s 'http://localhost:8080/apis/compute.infrapi.io/v1/namespaces/acme/vms?size=large&state=running' \
  -H "Authorization: Bearer $TOKEN"
```

Only `=`: `!=` and `==` are refused, a filter the API would not apply. A
parameter its contract doesn't declare is the API's to refuse.

### Raw requests

`--raw <path>` sends a path of the context's server as it is, with its
token: an operation the verbs don't address, its query included. No
discovery, no `<resource>`, no `-n` or `--field-selector`; the body is
optional, on every verb. A JSON response prints as JSON (`-o yaml` as
YAML), anything else as it is. The path starts with `/`: a full URL is
refused, the token never leaves the context's server.

**infrapi-cli**

```bash
infrapi-cli get --raw /.well-known/kayra.json
infrapi-cli get --raw '/apis/compute.infrapi.io/v1/namespaces/acme/vms/web-1/console?lines=50' -o yaml
infrapi-cli create --raw /apis/catalog.infrapi.io/v1/apiregistrations -f registration.json
```

**curl**

```bash
curl -s http://localhost:8080/.well-known/kayra.json
curl -s 'http://localhost:8080/apis/compute.infrapi.io/v1/namespaces/acme/vms/web-1/console?lines=50' \
  -H "Authorization: Bearer $TOKEN"
curl -s -X POST http://localhost:8080/apis/catalog.infrapi.io/v1/apiregistrations \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d @registration.json
```

The response body is printed pretty-printed as-is — this never
interprets or re-shapes it, since it has no idea what shape any given
api's own resource actually is. A non-2xx response still prints
the body (typically the same RFC 9457 problem-detail shape
[`httpapi.Fail`](https://infrapi.github.io/kayra/packages/httpapi/) uses) and exits `1`.

| Flag | Default | Meaning |
|------|---------|---------|
| `--namespace`, `-n` | the context's | the namespace of a namespaced resource; a cluster-scoped one has none |
| `--data`, `-d` | — | request body, as a raw JSON string (`create`/`update`/`patch` only) |
| `--filename`, `-f` | — | request body, read from a file — `-` for stdin (`create`/`update`/`patch` only) |
