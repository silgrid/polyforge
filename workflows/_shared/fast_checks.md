the project's own build, its lint/format check, and the unit tests of the
packages/modules the unit touched (the changed files' packages/modules and their
tests, no more) — as the repo itself defines them (Makefile, package.json scripts,
CI config). For a Go repo that is `go build ./... && go vet ./... && gofmt -l .`
plus `go test` of the touched packages. A formatter listing any file (non-empty
`gofmt -l` output, or the repo's equivalent) counts as red, exactly like a non-zero
exit.
