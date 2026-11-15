# typst-renderer

A small, isolated gRPC daemon that compiles [Typst](https://typst.app) bundles to PDF.

It exists to keep template compilation away from the application that owns the data: the renderer
needs no database, no secrets and no network. Clients send a bundle (template files plus JSON data)
and get a PDF back.

- **Image:** `ghcr.io/mache-vermietung/typst-renderer:<version>` (`linux/amd64`)
- **Go module:** `github.com/mache-vermietung/typst-renderer` — the gRPC client code lives in
  `proto/renderer/v1`
- **License:** [MIT](./LICENSE)

## Security model

Typst downloads unvendored packages from the network by default, and template content (for example
Markdown rendered through a package) is a code-injection surface. The daemon is built to be run so
that a malicious template cannot reach anything valuable:

1. **No egress.** Typst tries to download unvendored packages from the network; the daemon relies
   on the missing network access to make that fail (the request is rejected with `COMPILE_FAILED`).
   Run it on a network without outbound access (`--network none`, a Docker network with
   `internal: true`, or a Kubernetes `NetworkPolicy`). Vendored packages are read from the
   configured package directory; the package cache itself is empty.
2. **No secrets, no data stores.** Bundles and data are processed in memory / `tmpfs` only.
3. **Least privilege.** The image is distroless, runs as `nonroot` (UID 65532) and works with a
   read-only root file system (`--read-only --tmpfs /tmp`).
4. **Resource limits.** Request size, compile time, output size and concurrency are bounded (see
   [Configuration](#configuration)); excess concurrency is rejected immediately instead of queued.

Report vulnerabilities as described in [SECURITY.md](./SECURITY.md).

## Quick start

```bash
docker run -d --name typst-renderer --read-only --tmpfs /tmp:rw,noexec,nosuid,size=256m \
  --network none ghcr.io/mache-vermietung/typst-renderer:v1.0.0
docker exec typst-renderer /bin/typst-renderer healthcheck
```

With `--network none` the port is not reachable from the host; attach the container to an internal
network shared with your client instead. The daemon listens on `:9100` by default.

## gRPC API

Defined in [`proto/renderer/v1/renderer.proto`](./proto/renderer/v1/renderer.proto) (package
`renderer.v1`, service `RendererService`).

- **`Render(RenderRequest) returns (RenderResponse)`** compiles the bundle (`entrypoint`, source
  `files` map, `data_json`) to a PDF. The optional `pdf_standard` enforces a PDF standard such as
  `a-2b` (PDF/A-2b); several values are comma-separated. Only values accepted by
  `typst compile --pdf-standard` are allowed.
- **`Query(QueryRequest) returns (QueryResponse)`** runs a Typst query (for example to extract
  metadata in golden-file tests) and returns the matches as JSON.

### Errors

Every error status carries a `google.rpc.ErrorInfo` (domain `typst-renderer`) with an
`ErrorReason` from the proto, so clients can tell apart errors that share a gRPC code. The helpers
`rendererv1.NewStatusError` and `rendererv1.ReasonOf` build and read it.

| Code | `ErrorReason` | Cause |
| :--- | :--- | :--- |
| `InvalidArgument` | `INVALID_BUNDLE` | Missing entrypoint, path outside the bundle root, unknown PDF standard, empty selector |
| `InvalidArgument` | `COMPILE_FAILED` | Typst compile error; the message carries the diagnostics (also: unvendored package) |
| `DeadlineExceeded` | `TIMEOUT` | min(gRPC deadline, `RENDERER_MAX_TIMEOUT_SECONDS`) exceeded |
| `ResourceExhausted` | `REQUEST_TOO_LARGE` | Request larger than `RENDERER_MAX_REQUEST_BYTES` |
| `ResourceExhausted` | `OUTPUT_TOO_LARGE` | Output larger than the 64 MiB limit |
| `ResourceExhausted` | `CONCURRENCY_LIMIT` | All `RENDERER_MAX_CONCURRENCY` slots busy — retrying later can succeed |
| `Canceled` | – | The caller cancelled the request |

Message sizes are coupled to the limits: the server accepts requests up to
`RENDERER_MAX_REQUEST_BYTES` + 1 MiB of protobuf overhead and sends responses up to 64 MiB + 1 MiB.
Clients must raise their receive limit accordingly (gRPC's default is 4 MiB).

### Health

The daemon implements `grpc.health.v1`: `SERVING` for the whole server (`""`) and for
`renderer.v1.RendererService`, `NOT_SERVING` while shutting down. Because the image has no shell,
the binary probes itself: `/bin/typst-renderer healthcheck` (exit code `0`/`1`, target
`127.0.0.1:<port of RENDERER_LISTEN_ADDR>`). The image declares it as `HEALTHCHECK`.

## Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `RENDERER_LISTEN_ADDR` | gRPC listen address | `:9100` |
| `RENDERER_MAX_REQUEST_BYTES` | Maximum request size in bytes | `20971520` (20 MiB) |
| `RENDERER_MAX_CONCURRENCY` | Maximum concurrent compilations | number of CPUs |
| `RENDERER_MAX_TIMEOUT_SECONDS` | Maximum compile time per request | `20` |
| `TYPST_BINARY_PATH` | Path to the Typst binary | `typst` (`/bin/typst` in the image) |
| `RENDERER_PACKAGE_PATH` | Directory with vendored Typst packages (`DOCUMENT_PACKAGE_PATH` is accepted as an alias) | `/packages` |
| `RENDERER_FONT_PATH` | Directory with additional fonts (`DOCUMENT_FONT_PATH` is accepted as an alias) | unset (`/fonts` in the image) |
| `LOG_LEVEL`, `LOG_FORMAT` | `debug`/`info`/`warn`/`error`; `text` for human-readable output | `info`, JSON |
| `OTEL_EXPORTER_OTLP_ENDPOINT` (and related `OTEL_*`) | OTLP/HTTP export of traces and metrics; without an endpoint metrics are a no-op | unset |

## Derived images and vendored packages

The base image ships empty `/packages` and `/fonts` directories. Templates that import packages
need them vendored there:

```dockerfile
FROM ghcr.io/mache-vermietung/typst-renderer:v1.0.0
COPY packages /packages
```

`/packages` uses Typst's local package layout (`<namespace>/<name>/<version>/`).

## Using the Go module

```go
import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rendererv1 "github.com/mache-vermietung/typst-renderer/proto/renderer/v1"
)

conn, err := grpc.NewClient("typst-renderer:9100",
	grpc.WithTransportCredentials(insecure.NewCredentials()),
	grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(65<<20)))
// handle err
client := rendererv1.NewRendererServiceClient(conn)
resp, err := client.Render(ctx, &rendererv1.RenderRequest{Entrypoint: "main.typ", Files: files, DataJson: data})
```

`typstrenderer.TypstVersion()` (package `github.com/mache-vermietung/typst-renderer`) returns the
Typst version this release bundles. Consumers that also compile locally — golden-file tests, a
local render adapter — should use the same version so their output matches the daemon.

## Logging and telemetry

Typst diagnostics can quote template source and data, which may be personal. They appear only in
the status message returned to the caller and as an event on the span, never in logs or in the
span status. Log lines contain method, code, `ErrorReason`, request/response size and duration.

Traces and metrics are exported over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. A daemon
without egress cannot reach a collector directly; put an OTLP proxy on the internal network.

## Development

```bash
scripts/install-typst.sh "$HOME/.local/bin"   # Typst version from build/typst.version, checksum-verified
go build ./... && go test -race ./...
golangci-lint run                             # config: .golangci.yml
scripts/generate.sh                           # regenerate proto code (tool versions pinned in tools.mod)
docker build -t typst-renderer .
```

### Upgrading Typst

The version lives in `build/typst.version`; the checksums of the release archives in
`build/typst.sha256`. To upgrade:

1. Set the new version in `build/typst.version`.
2. Copy the SHA-256 sums of `typst-x86_64-unknown-linux-musl.tar.xz` and
   `typst-aarch64-unknown-linux-musl.tar.xz` from the Typst release page into
   `build/typst.sha256`.
3. Read the Typst changelog for breaking changes and run the tests.
4. Release a new version (below). Consumers pin the module and image version, so they upgrade
   deliberately.

### Releases

Tags are created on commits of `main`: `vX.Y.Z` for a release, `vX.Y.Z-rc.N` for a pre-release. The
tag is the version of the Go module **and** of the image. The release workflow builds the image,
fails on fixable critical vulnerabilities (Trivy), pushes it to `ghcr.io` and creates the GitHub
release. Proto changes are checked for breaking changes against the base branch (`buf breaking`).

## Lizenzen

Der Code dieses Repositories steht unter der MIT-Lizenz (`LICENSE`). Das Image enthält zusätzlich
Fremdsoftware; die Lizenztexte liegen unter `/licenses`:

- `/licenses/typst`: Typst (Apache-2.0) inkl. `NOTICE`; Typst bringt seine Standardschriften selbst mit.
- `/licenses/go`: alle in das Binary gelinkten Go-Abhängigkeiten. Nach Änderungen an den
  Abhängigkeiten neu erzeugen: `scripts/licenses.sh` (Ergebnis in `third_party/go-licenses` committen).
- `/licenses/typst-renderer`: die MIT-Lizenz dieses Repositories.

Abgeleitete Images (z. B. mit zusätzlichen Typst-Paketen unter `/packages`) müssen die Lizenzen der
dort ergänzten Pakete selbst beilegen.
