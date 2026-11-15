package typstrenderer_test

import (
	"regexp"
	"testing"

	typstrenderer "github.com/mache-vermietung/typst-renderer"
)

func TestTypstVersionIsSemver(t *testing.T) {
	t.Parallel()

	if got := typstrenderer.TypstVersion(); !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(got) {
		t.Fatalf("TypstVersion() = %q, want MAJOR.MINOR.PATCH", got)
	}
}
