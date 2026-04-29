// app/ is a Next.js subproject; this stub module exists solely so
// Go tooling (`go build ./...`, `go test ./...`, `golangci-lint run
// ./...`) stops at the boundary. Without it, every recursive Go
// command descends into app/node_modules and lints third-party
// vendored Go files (e.g. flatted/golang/pkg/flatted) shipped inside
// JS packages — which we don't own and can't fix.
module github.com/openotters/openotters/app

go 1.26
