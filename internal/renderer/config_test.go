package renderer_test

import (
	"testing"

	"github.com/mache-vermietung/typst-renderer/internal/renderer"
)

func TestLoadConfigFromEnvPaths(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		wantPackages string
		wantFonts    string
	}{
		{name: "defaults", wantPackages: "/packages"},
		{
			name:         "renderer names",
			env:          map[string]string{"RENDERER_PACKAGE_PATH": "/p", "RENDERER_FONT_PATH": "/f"},
			wantPackages: "/p",
			wantFonts:    "/f",
		},
		{
			name:         "document names are accepted as aliases",
			env:          map[string]string{"DOCUMENT_PACKAGE_PATH": "/dp", "DOCUMENT_FONT_PATH": "/df"},
			wantPackages: "/dp",
			wantFonts:    "/df",
		},
		{
			name: "renderer names win over aliases",
			env: map[string]string{
				"RENDERER_PACKAGE_PATH": "/p", "DOCUMENT_PACKAGE_PATH": "/dp",
				"RENDERER_FONT_PATH": "/f", "DOCUMENT_FONT_PATH": "/df",
			},
			wantPackages: "/p",
			wantFonts:    "/f",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{
				"RENDERER_PACKAGE_PATH", "DOCUMENT_PACKAGE_PATH", "RENDERER_FONT_PATH", "DOCUMENT_FONT_PATH",
			} {
				t.Setenv(key, "")
			}

			for key, val := range tc.env {
				t.Setenv(key, val)
			}

			cfg := renderer.LoadConfigFromEnv()
			if cfg.PackagePath != tc.wantPackages {
				t.Errorf("PackagePath = %q, want %q", cfg.PackagePath, tc.wantPackages)
			}

			if cfg.FontPath != tc.wantFonts {
				t.Errorf("FontPath = %q, want %q", cfg.FontPath, tc.wantFonts)
			}
		})
	}
}
