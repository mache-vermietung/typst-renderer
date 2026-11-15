# Stage 1: Build the static Go binary (on the build platform, cross-compiled for the target)
FROM --platform=$BUILDPLATFORM golang:1.26-trixie AS go-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /bin/typst-renderer ./cmd/typst-renderer

# Stage 2: Fetch and verify the pinned Typst binary (checksums: build/typst.sha256)
FROM debian:trixie-slim AS typst-fetcher

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    xz-utils \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY build/typst.version build/typst.sha256 /src/build/
COPY scripts/install-typst.sh /src/scripts/install-typst.sh

ARG TARGETARCH
RUN TYPST_ARCH=${TARGETARCH:-$(dpkg --print-architecture)} /src/scripts/install-typst.sh /out/bin \
    && mkdir -p /out/packages /out/fonts

# Stage 3: Minimal, isolated runtime container
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/mache-vermietung/typst-renderer" \
      org.opencontainers.image.description="Isolated Typst-to-PDF renderer daemon (gRPC, no network access required)" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /
COPY --from=go-builder /bin/typst-renderer /bin/typst-renderer
COPY --from=typst-fetcher /out/bin/typst /bin/typst

# Empty package and font directories: derived images add vendored Typst packages to /packages
# and fonts to /fonts (typst runs with --package-path / --font-path and never downloads).
COPY --from=typst-fetcher /out/packages /packages
COPY --from=typst-fetcher /out/fonts /fonts
USER nonroot:nonroot

ENV RENDERER_LISTEN_ADDR=":9100" \
    TYPST_BINARY_PATH="/bin/typst" \
    DOCUMENT_PACKAGE_PATH="/packages" \
    DOCUMENT_FONT_PATH="/fonts"

EXPOSE 9100

# Distroless has no shell: the daemon probes grpc.health.v1 on itself.
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/bin/typst-renderer", "healthcheck"]

ENTRYPOINT ["/bin/typst-renderer"]
