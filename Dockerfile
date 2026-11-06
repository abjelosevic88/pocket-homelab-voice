# syntax=docker/dockerfile:1.7
# ---------- 1. build the web UI ----------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 2. build the Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pocket-core ./third_party/pocket-core
RUN cd third_party/pocket-core && sha256sum -c SHA256SUMS
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pocket-voice ./cmd/pocket-voice

# ---------- 3. runtime ----------
# ffmpeg reads any recording; whisper.cpp writes it out on this machine.
# Alpine packages whisper.cpp for amd64 and arm64; on armv7 memos are written
# out through an API instead (Settings → Writing out). python3 with numpy and
# onnxruntime runs the speaker models that tell the voices of a meeting apart
# (amd64 and arm64 too; sympy, which onnxruntime only needs to quantise
# models, and the test suites are left out: ~60 MB).
FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata curl ffmpeg \
 && (apk add --no-cache whisper.cpp || echo "whisper.cpp is not packaged for this architecture") \
 && (apk add --no-cache python3 py3-numpy py3-onnxruntime \
     && rm -rf /usr/lib/python3*/site-packages/sympy /usr/lib/python3*/site-packages/mpmath \
     && find /usr/lib/python3*/site-packages -type d -name tests -prune -exec rm -rf {} + \
     && python3 -c "import numpy, onnxruntime" \
     || echo "onnxruntime is not packaged for this architecture: no speaker finding") \
 && addgroup -g 1000 app && adduser -u 1000 -G app -D -h /data app
COPY --from=build /out/pocket-voice /usr/local/bin/pocket-voice
ENV DATA_DIR=/data PORT=8080 HOST=0.0.0.0
VOLUME ["/data"]
EXPOSE 8080
USER app
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD curl -fsS http://localhost:8080/healthz || exit 1
LABEL org.opencontainers.image.title="Pocket Voice" \
      org.opencontainers.image.description="Voice memos written out on your own server with whisper.cpp, tidied up by Pocket AI and filed into notes, tasks and your calendar. One Go binary, one SQLite file, one volume." \
      org.opencontainers.image.source="https://github.com/abjelosevic88/pocket-homelab-voice" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["pocket-voice"]
