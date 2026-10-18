//go:build tools

// Package tools pins the scanner built by the Lint & Security job so
// `go mod tidy` keeps its dependency graph in this module's go.sum.
package tools

import _ "github.com/securego/gosec/v2/cmd/gosec"
