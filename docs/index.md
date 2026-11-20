---
title: infrapi-cli
description: InfraPI's command line - log in to Kayra and call its API, create an API project from its contract, and run a local dev stack.
weight: 10
---

# infrapi-cli

InfraPI's command line, one binary for three things:

- **Kayra's client.** Log in to a Kayra server you only know the URL of,
  keep the login as a context, ask whether you may act, list the APIs, and
  call them: `auth` (`login`, `context`, `can-i`, `whoami`), `apis`, `get`, `create`,
  `update`, `patch`, `delete`. It never reads a server's configuration:
  everything goes through the server's own API.
- **Your API.** Create a Go project on infrapi/lib, add resources to its
  OpenAPI contract, generate its code, run it, register it in Kayra:
  `app init`, `app add`, `app generate`, `app run`, `app register`.
- **The dev stack.** Run a local InfraPI (Kayra, etcd and APISIX on
  podman or docker) to develop the API against: `dev init`, `dev up`,
  `dev contexts`, `dev logs`, `dev down`...

| I want to... | Go to |
|---|---|
| Install it, log in, make a first call | [Getting started](getting-started.md) |
| Know every command against Kayra, and its flags | [Commands](commands.md) |
| Develop an API, from nothing to a tenant calling it | [Develop an API](develop/index.md) |
| Know every `app` command | [app](app.md) |
| Know every `dev` command | [dev](dev.md) |
| Read the code: contexts, the login flow, the project generator, the dev stack | [Packages](packages.md) |

Examples of calls to Kayra are given twice: with `infrapi-cli`, then with
`curl`.
