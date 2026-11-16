---
title: Getting started
description: Install infrapi-cli, log in to a Kayra server, and make a first call.
weight: 20
---

# Getting started

## 1. Install

Every [GitHub release](https://github.com/infrapi/cli/releases) publishes
an archive per OS and architecture (`infrapi-cli_<version>_<os>_<arch>`).
Install it with [bin](https://github.com/marcosnils/bin/), which picks the
archive matching your machine and tracks it for `bin update`:

```bash
bin install github.com/infrapi/cli ~/.local/bin/infrapi-cli
```

Or with Go 1.27.3 or later:

```bash
go install github.com/infrapi/cli/cmd/infrapi-cli@latest
```

<details>
<summary>Building from source instead</summary>

```bash
go build -o infrapi-cli ./cmd/infrapi-cli
```

</details>

## 2. Log in

`login` opens your browser on the server's own login page, and saves the
result as a context in `~/.config/infrapi/config` (`INFRAPI_CONTEXT_FILE`
to keep it elsewhere):

```bash
infrapi-cli login --server https://kayra.example.com
infrapi-cli auth whoami
```

On a machine without a browser, `login --no-browser` prints the URL to
open elsewhere and asks for the code the server then shows. With a token
you already have, a bootstrap token from `kayra token mint` say, save it
directly:

```bash
infrapi-cli context set admin --server https://kayra.example.com --token "$TOKEN"
```

## 3. Call the API

**infrapi-cli**

```bash
infrapi-cli apis                                  # every API group and its resources
infrapi-cli get catalog apiregistrations
infrapi-cli auth can-i create subscriptions --api catalog.infrapi.io --namespace core
```

**curl**

```bash
TOKEN=$(infrapi-cli context token)
curl -s https://kayra.example.com/apis -H "Authorization: Bearer $TOKEN"
curl -s https://kayra.example.com/apis/catalog.infrapi.io/v1/apiregistrations -H "Authorization: Bearer $TOKEN"
```

Every command is in [Commands](commands.md). To get a Kayra of your own to
try this against, see [dev](dev.md).
