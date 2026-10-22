# Polyforge

[![License: PolyForm Noncommercial 1.0.0](https://img.shields.io/badge/license-PolyForm%20Noncommercial%201.0.0-blue)](LICENSE)

Work-item orchestration platform: an aihub server (WI store, memory, git
revision chain) and a pf work-machine binary (CLI + daemon with the Go
execution driver).

## Documentation

- What a term means: [concepts and glossary](docs/concepts.md) — every
  term a newcomer meets, defined once
- Installing, platforms, upgrading a work machine: [install](docs/install.md)
  (upgrade history: [upgrade](docs/upgrade.md))
- A first from-zero run: the quickstarts —
  [developers](docs/quickstart-developers.md), [macOS](docs/quickstart-macos.md)
- Command shapes: [commands](docs/reference/commands.md) — `pf help`,
  `pf help <verb>`, the skill fragments
- Every config key: [config](docs/reference/config.md)
- The daemon's local routes: [UDS routes](docs/reference/uds-routes.md)
- Recovery failure codes: [failure codes](docs/reference/failure-codes.md)
- Releases, build and test: [release](docs/reference/release.md)
- The system, the modules, the design docs: [architecture](docs/architecture.md)
- Extending without changing code: [extending](docs/extending.md)
- Authoring a custom workflow bundle: [bundles](docs/bundles.md)
- Contributing, filing work items, change coverage: [contributing](docs/contributing.md)
- Daily use: [user guide](docs/user-guide.md)

## Commercial licensing

Polyforge is licensed under the
[PolyForm Noncommercial License 1.0.0](LICENSE): personal use, secondary
development/modification and distribution are allowed for noncommercial
purposes.

In addition to that license, silgrid grants: personal and team internal use
is free, including within for-profit organizations for their own internal
purposes.

Commercial exploitation — selling Polyforge, offering it as a paid hosted
service, or embedding it in a commercial product — requires a separate
commercial license. Contact <wangxk1991@gmail.com>.
