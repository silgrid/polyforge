# Version stamped into both binaries at link time: internal/version.Version,
# printed by `pf --version` / `aihub --version`. Defaults to the git tag when
# HEAD is exactly tagged, else "dev". The documented release procedure tags
# AFTER the build, so it passes VERSION=<tag> explicitly to `make release`.
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
GO_LDFLAGS := -X github.com/silgrid/polyforge/internal/version.Version=$(VERSION)

.PHONY: check build webui-build crossbuild vet fmt test xrefs release

# The console SPA build: every binary-producing target
# builds the locked frontend FIRST, so the go:embed dist tree exists
# before any go build. Node is checked with an explicit message (the
# exact .nvmrc version — the repo-root pin nvm itself reads), and the
# build is `npm ci` + `npm run build` from the one committed lockfile —
# no node_modules shortcut, no version drift. A host without the
# pinned node fails loudly, never skips.
WEBUI_NODE := $(shell cat .nvmrc 2>/dev/null)

webui-build:
	@command -v node >/dev/null 2>&1 || { echo "webui-build: node $(WEBUI_NODE) is required (pinned by .nvmrc); install node $(WEBUI_NODE) first"; exit 1; }
	@if [ "$$(node --version)" != "v$(WEBUI_NODE)" ]; then \
		echo "webui-build: node $$(node --version) does not match .nvmrc ($(WEBUI_NODE)); install node $(WEBUI_NODE) first"; exit 1; \
	fi
	cd webui && npm ci && npm run build

build: webui-build
	go build -trimpath -ldflags "$(GO_LDFLAGS)" ./...

vet:
	go vet ./...

# crossbuild is the darwin cross-build gate (polyforge#218): the tree must
# build for both darwin architectures, so pf and aihub binaries can exist on
# macOS. This is part of `check`, ahead of the test gate.
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

fmt:
	@out=$$(gofmt -l .); \
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

check: build vet fmt xrefs crossbuild test

# release builds the six shipped binaries with the git tag injected into
# internal/version.Version via -ldflags -X, into artifacts/release-<tag>/
# (polyforge#218: the matrix is aihub/pf × linux-amd64, darwin-arm64,
# darwin-amd64; release.sh consumes the linux pair under the un-suffixed
# names and the darwin ones under their asset names).
# Pass VERSION=<tag> (the release procedure builds before tagging) or run
# it on the tagged commit itself. Refuses to stamp the un-injected "dev".
release: webui-build
	@if [ "$(VERSION)" = dev ]; then \
		echo "make release: no git tag on HEAD and no VERSION=<tag> given; refusing to stamp 'dev'"; \
		exit 1; \
	fi
	GOWORK=off go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/ ./cmd/aihub ./cmd/pf
	GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/aihub-darwin-arm64 ./cmd/aihub
	GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/pf-darwin-arm64 ./cmd/pf
	GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/aihub-darwin-amd64 ./cmd/aihub
	GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o artifacts/release-$(VERSION)/pf-darwin-amd64 ./cmd/pf
	@echo "release: injected internal/version.Version=$(VERSION) (ldflags -X) into the 6 binaries: aihub/pf linux-amd64 + darwin-arm64 + darwin-amd64"
