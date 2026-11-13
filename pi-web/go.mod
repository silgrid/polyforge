// Gate boundary marker, not a Go module anyone builds: it
// holds no packages. Its presence makes pi-web/ a nested module, so the
// repository root's `go build ./...`, `go vet ./...` and `go list ./...`
// stop at this directory and never walk pi-web/node_modules (npm packages
// may ship stray .go files). The root gofmt gate lists tracked .go files
// only, for the same reason.
module github.com/silgrid/polyforge/pi-web

go 1.26
