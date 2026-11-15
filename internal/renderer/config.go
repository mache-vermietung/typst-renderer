// Package renderer provides the gRPC service and standalone daemon for isolated Typst document rendering.
package renderer

import (
	"os"
	"runtime"
	"strconv"
)

const (
	// DefaultListenAddr is the default gRPC listen address.
	DefaultListenAddr = ":9100"
	// DefaultMaxRequestBytes is the default maximum request size in bytes (20 MiB).
	DefaultMaxRequestBytes int64 = 20 << 20
	// DefaultMaxTimeoutSeconds is the default compilation timeout in seconds.
	DefaultMaxTimeoutSeconds = 20
	// DefaultTypstBinaryPath is the default Typst binary path.
	DefaultTypstBinaryPath = "typst"
	// DefaultPackagePath is the default location of vendored Typst packages. The base image ships
	// it empty; derived images add the packages their templates import.
	DefaultPackagePath = "/packages"
)

// Config configures the renderer gRPC service.
type Config struct {
	ListenAddr        string
	MaxRequestBytes   int64
	MaxConcurrency    int
	MaxTimeoutSeconds int
	TypstBinaryPath   string
	PackagePath       string
	FontPath          string
}

// LoadConfigFromEnv reads renderer configuration from environment variables with safe defaults.
func LoadConfigFromEnv() Config {
	listenAddr := os.Getenv("RENDERER_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = DefaultListenAddr
	}

	typstBinary := os.Getenv("TYPST_BINARY_PATH")
	if typstBinary == "" {
		typstBinary = DefaultTypstBinaryPath
	}

	fontPath := firstEnv("RENDERER_FONT_PATH", "DOCUMENT_FONT_PATH")

	return Config{
		ListenAddr:        listenAddr,
		MaxRequestBytes:   parseEnvInt64("RENDERER_MAX_REQUEST_BYTES", DefaultMaxRequestBytes),
		MaxConcurrency:    parseEnvInt("RENDERER_MAX_CONCURRENCY", runtime.NumCPU()),
		MaxTimeoutSeconds: parseEnvInt("RENDERER_MAX_TIMEOUT_SECONDS", DefaultMaxTimeoutSeconds),
		TypstBinaryPath:   typstBinary,
		PackagePath:       resolvePackagePath(),
		FontPath:          fontPath,
	}
}

func parseEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultVal
}

func parseEnvInt64(key string, defaultVal int64) int64 {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.ParseInt(val, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultVal
}

func resolvePackagePath() string {
	if path := firstEnv("RENDERER_PACKAGE_PATH", "DOCUMENT_PACKAGE_PATH"); path != "" {
		return path
	}

	return DefaultPackagePath
}

// firstEnv returns the value of the first non-empty variable. The DOCUMENT_* names are accepted as
// aliases of the RENDERER_* names for existing deployments.
func firstEnv(keys ...string) string {
	for _, key := range keys {
		if val := os.Getenv(key); val != "" {
			return val
		}
	}

	return ""
}
