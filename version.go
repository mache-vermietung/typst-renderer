// Package typstrenderer exposes build-time facts about the renderer that consumers of the Go
// module need, most importantly the Typst version the daemon (and its base image) is built for.
package typstrenderer

import (
	_ "embed"
	"strings"
)

//go:embed build/typst.version
var typstVersion string

// TypstVersion is the Typst release the renderer image bundles, e.g. "0.15.1". Consumers that
// compile templates locally (golden-file tests, local render adapters) should use the same
// version so that their output matches the daemon.
func TypstVersion() string {
	return strings.TrimSpace(typstVersion)
}
