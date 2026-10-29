# Stage 1: Build the static Go binary (on the build platform, cross-compiled for the target)
FROM --platform=$BUILDPLATFORM golang:1.26-trixie AS go-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /bin/renderer ./cmd/renderer

# Stage 2: Fetch and verify pinned Typst binary
FROM debian:trixie-slim AS typst-fetcher

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    xz-utils \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG TARGETARCH
COPY build/typst.version /tmp/typst.version

RUN TYPST_VERSION=$(cat /tmp/typst.version | tr -d '\r\n') && \
    ARCH=${TARGETARCH:-$(dpkg --print-architecture)} && \
    case "$ARCH" in \
        amd64|x86_64) \
            TAR_NAME="typst-x86_64-unknown-linux-musl.tar.xz" && \
            EXPECTED_SHA="a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c" \
            ;; \
        arm64|aarch64) \
            TAR_NAME="typst-aarch64-unknown-linux-musl.tar.xz" && \
            EXPECTED_SHA="5aa8d74a3d906e60ea12a66ac2f37f8eef1b14cbad7182a745e393a10c23dcee" \
            ;; \
        *) \
            echo "Unsupported architecture: $ARCH" && exit 1 \
            ;; \
    esac && \
    curl -fsSL "https://github.com/typst/typst/releases/download/v${TYPST_VERSION}/${TAR_NAME}" -o "/tmp/${TAR_NAME}" && \
    ACTUAL_SHA=$(sha256sum "/tmp/${TAR_NAME}" | awk '{print $1}') && \
    if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then \
        echo "Checksum mismatch! Expected $EXPECTED_SHA, got $ACTUAL_SHA" && exit 1; \
    fi && \
    tar -xf "/tmp/${TAR_NAME}" -C /tmp && \
    DIR_NAME="${TAR_NAME%.tar.xz}" && \
    mv "/tmp/${DIR_NAME}/typst" /bin/typst && \
    chmod +x /bin/typst && \
    mkdir -p /fonts

# Stage 3: Minimal, isolated runtime container
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /
COPY --from=go-builder /bin/renderer /bin/renderer
COPY --from=typst-fetcher /bin/typst /bin/typst

# Vendored packages and the (still empty) font directory for --font-path
COPY internal/document/packages /packages
COPY --from=typst-fetcher /fonts /fonts
USER nonroot:nonroot

ENV RENDERER_LISTEN_ADDR=":9100" \
    TYPST_BINARY_PATH="/bin/typst" \
    DOCUMENT_PACKAGE_PATH="/packages" \
    DOCUMENT_FONT_PATH="/fonts"

EXPOSE 9100

ENTRYPOINT ["/bin/renderer"]
