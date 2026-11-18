---
title: Develop an API
description: Build an InfraPI API on your machine - a Go project from its contract, run against a local Kayra, etcd and APISIX on podman or docker.
weight: 60
---

# Develop an API

Two commands of [infrapi-cli](https://infrapi.github.io/cli/getting-started/#1-install)
take an API from nothing to a tenant calling it:

- **`infrapi-cli app`** writes and grows **your project**: a Go module on
  [infrapi/lib](https://github.com/infrapi/lib), its OpenAPI contract, its CI and
  release (`app init`), a resource added to the contract (`app add`), the Go code
  generated from it (`app generate`), its registration in Kayra (`app register`).
  What it writes is yours from then on: it never writes it again.
- **`infrapi-cli dev`** runs **a local InfraPI** to develop it against: Kayra
  authenticates and authorizes every request and reconciles subscriptions, APISIX
  routes Kayra and your API, etcd stores both. It runs on podman or docker,
  configured by the project's `dev.yaml`, and nothing reaches outside the stack's
  network once the images are pulled.

| I want to... | Go to |
|---|---|
| Install what is needed, create a project and its stack, start it, log in | [Getting started](getting-started.md) |
| Add a resource, write it, register the API, call it as a tenant | [Develop an API](develop-an-api.md) |
| Know what runs, how it is configured, the commands, how to reset | [The dev stack](dev-stack.md) |
| Follow one API end to end, with a real backend | [Build an EC2 instance API](https://infrapi.github.io/learn/build-an-ec2-api-with-lib/) |

Examples of calls to Kayra and to your API are given twice: with
`infrapi-cli`, then with `curl`.
