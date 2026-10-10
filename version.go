// Package chiselproxy holds the project VERSION file. It sits at the module root
// because go:embed cannot reach parent directories, so only a root-level
// package can embed it.
package chiselproxy

import _ "embed"

//go:embed VERSION
var VersionFile string
