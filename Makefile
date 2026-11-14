# Version stamped into the pf binary at link time: internal/version.Version,
# printed by `pf --version` / `pf aihub --version`. Defaults to the release tag
# (vX.Y.Z) when HEAD is exactly tagged with one, else "dev" - never another
# tag on HEAD, such as a green/<date>-<n> releasable marker (tools/green-tag).
# The documented release procedure tags AFTER the build, so it passes
# VERSION=<tag> explicitly to `make release`.
VERSION ?= $(shell git describe --tags --exact-match --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo dev)
GO_LDFLAGS := -X github.com/silgrid/polyforge/internal/version.Version=$(VERSION)

.PHONY: check build webui-build crossbuild vet fmt test xrefs release pi-web-build pi-web-lint pi-web-test pi-web-e2e

# The console SPA build: every binary-producing target
# builds the locked frontend FIRST, so the go:embed dist tree exists
# before any go build. Node is checked with an explicit message against
# the repo-root .nvmrc, which pins the node MAJOR version (the pin nvm
# itself reads): any node of that major passes, whatever its patch
# level; any other major fails. The build is `npm ci` + `npm run build`
# from the one committed lockfile — no node_modules shortcut. A host
# without a node of the pinned major fails loudly, never skips.
WEBUI_NODE := $(shell cat .nvmrc 2>/dev/null)
# NODE_CHECK is that check, shared by every target that runs npm (the console
# SPA and the pi-web package); $@ names the target.
NODE_CHECK = command -v node >/dev/null 2>&1 || { echo "$@: node $(WEBUI_NODE).x is required (major version pinned by .nvmrc); install node $(WEBUI_NODE).x first"; exit 1; }; \
	if [ "$$(node --version | cut -d. -f1)" != "v$(WEBUI_NODE)" ]; then \
		echo "$@: node $$(node --version) is not major $(WEBUI_NODE) (pinned by .nvmrc); install node $(WEBUI_NODE).x first"; exit 1; \
	fi

webui-build:
	@$(NODE_CHECK)
	cd webui && npm ci && npm run build

# The pi-web package: its own npm package under pi-web/,
# built and tested with its own scripts. pi-web-build is a production
# `next build` (the release path's build; never during `npm run dev`).
# pi-web-lint and pi-web-test install the locked dependencies only when
# pi-web/node_modules is absent; pi-web-e2e needs Chromium
# (npx playwright install chromium, once).
PIWEB_DEPS = cd pi-web && { [ -d node_modules ] || npm ci; }

pi-web-build:
	@$(NODE_CHECK)
	cd pi-web && npm ci && npm run build

pi-web-lint:
	@$(NODE_CHECK)
	$(PIWEB_DEPS) && npm run lint && node_modules/.bin/tsc --noEmit

pi-web-test:
	@$(NODE_CHECK)
	$(PIWEB_DEPS) && npm test

pi-web-e2e:
	@$(NODE_CHECK)
	$(PIWEB_DEPS) && npm run test:e2e:all

build: webui-build
	go build -trimpath -ldflags "$(GO_LDFLAGS)" ./...

vet:
	go vet ./...

# crossbuild is the darwin cross-build gate (polyforge#218): the tree must
# build for both darwin architectures, so the pf binary (client, daemon and
# the `pf aihub` server) can exist on macOS. This is part of `check`, ahead
# of the test gate.
crossbuild: webui-build
	GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath ./...
	GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath ./...

test: webui-build
	# go test suppresses skip messages unless -v (or -json) is on, so a plain
	# `make test` does NOT show which tests skipped; run `go test -v ./...` when
	# that evidence matters. Opt-in tests (skipped unless their env var is set):
	#   POLYFORGE_TEST_SYSTEM_INSTALL=1    installer scenarios: write /usr/local/bin/pf
	# The ./test acceptance package runs through test/e2e-parallel.sh: one
	# process per test, E2E_JOBS at a time. `go test ./test/ -timeout 20m`
	# still runs it serially; it takes longer than go's default 10m
	# per-package timeout.
	go test $$(go list ./... | grep -v '/test$$') -count=1
	sh test/e2e-parallel.sh

# fmt lists tracked .go files only: pi-web/node_modules is
# untracked and may carry stray .go files, which a `gofmt -l .` walk would
# read. .polyforge/verify-scope.conf runs the same gate.
fmt:
	@out=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$out" ]; then \
		echo "gofmt needed on:"; echo "$$out"; exit 1; \
	fi

# xrefs checks the design doc set's cross-references and section anchors
# (docs/design/polyforge-vnext/tools/check-xrefs.py; archived docs are not
# checked). python3 is not a build dependency: without it the step is
# skipped with a message instead of failing the whole check.
# .polyforge/release.sh runs the same checker before its test gate, but with
# python3 required (a release fails instead of skipping); RELEASE_XREFS_CMD
# overrides it the way RELEASE_TEST_CMD overrides the test gate.
xrefs:
	@if command -v python3 >/dev/null 2>&1; then \
		python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py; \
	else \
		echo "xrefs: python3 not found; SKIPPING the design-doc cross-reference check"; \
	fi

# check covers both components: the Go gates, then the pi-web package's
# lint/typecheck and unit tests (its e2e suites are pi-web-e2e).
check: build vet fmt xrefs crossbuild test pi-web-lint pi-web-test

# release builds the three shipped pf binaries with the git tag injected
# into internal/version.Version via -ldflags -X, into artifacts/release-<tag>/
# (one pf binary — the server runs as `pf aihub` — for
# linux-amd64, darwin-arm64, darwin-amd64; release.sh consumes the linux
# one under the un-suffixed name pf and the darwin ones under their asset
# names).
# Pass VERSION=<tag> (the release procedure builds before tagging) or run
# it on the tagged commit itself. Refuses to stamp the un-injected "dev".
release: webui-build
	@if [ "$(VERSION)" = dev ]; then \
		echo "make release: no git tag on HEAD and no VERSION=<tag> given; refusing to stamp 'dev'"; \
		exit 1; \
	fi
	GOWORK=off go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/pf ./cmd/pf
	GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/pf-darwin-arm64 ./cmd/pf
	GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/pf-darwin-amd64 ./cmd/pf
	@echo "release: injected internal/version.Version=$(VERSION) (ldflags -X) into the 3 pf binaries: linux-amd64 + darwin-arm64 + darwin-amd64"
