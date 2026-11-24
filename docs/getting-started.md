---
title: Getting started
description: Install infrapi-cli, log in to a Kayra server, and make a first call.
weight: 20
---

# Getting started

## 1. Install

`infrapi-cli` is published on every
[release](https://github.com/infrapi/cli/releases): an archive per OS and
architecture (`infrapi-cli_<version>_<os>_<arch>`), with `checksums.txt`.
Install it with [bin](https://github.com/marcosnils/bin/), which picks the
archive matching your OS and architecture, puts the binary on your `PATH`
and tracks it for `bin update`:

```bash
bin install github.com/infrapi/cli ~/.local/bin/infrapi-cli
infrapi-cli --version
```

### Or the container image

Every release also publishes an image, `ghcr.io/infrapi/cli`, for
`linux/amd64` and `linux/arm64`, tagged with the version
(`<major>.<minor>.<patch>`), its minor (`<major>.<minor>`) and `latest`: pin
one of the first two in a deployment. It runs as an unprivileged user (uid
10001). It is for a CI job or a script: its entrypoint is `infrapi-cli`, it
has no Go for `app`, and `dev` runs compose on the host, not in it. In a
GitLab CI job, set `entrypoint: [""]`: the job runs a shell in the image.

```bash
podman run --rm ghcr.io/infrapi/cli:latest --version
```

### Or with Go

With Go 1.27.4 or later:

```bash
go install github.com/infrapi/cli/cmd/infrapi-cli@latest
```

<details>
<summary>Building from source instead</summary>

```bash
git clone https://github.com/infrapi/cli && cd cli
go build -o infrapi-cli ./cmd/infrapi-cli
```

</details>

## 2. Log in

`auth login` opens your browser on the server's own login page, and saves the
result as a context in `~/.config/infrapi/config` (`INFRAPI_CONTEXT_FILE`
to keep it elsewhere):

```bash
infrapi-cli auth login --server https://kayra.example.com
infrapi-cli auth whoami
```

On a machine without a browser, `login --no-browser` prints the URL to
open elsewhere and asks for the code the server then shows. With a token
you already have, a bootstrap token from `kayra token mint` say, save it
directly:

```bash
infrapi-cli auth context set admin --server https://kayra.example.com --token "$TOKEN"
```

## 3. Call the API

**infrapi-cli**

```bash
infrapi-cli apis                                  # every API group and its resources
infrapi-cli get apiregistrations
infrapi-cli auth can-i create subscriptions --namespace core
```

**curl**

```bash
TOKEN=$(infrapi-cli auth context token)
curl -s https://kayra.example.com/apis -H "Authorization: Bearer $TOKEN"
curl -s https://kayra.example.com/apis/catalog.infrapi.io/v1/apiregistrations -H "Authorization: Bearer $TOKEN"
```

Every command is in [Commands](commands.md). To get a Kayra of your own to
try this against, see [dev](dev.md).
