// Package api holds the types and the HTTP server interface generated from
// api/openapi.yaml. Never edit api.gen.go by hand: change the contract and
// run `go generate ./...` in backend/.
package api

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2 -config oapi-codegen.yaml ../../../api/openapi.yaml
