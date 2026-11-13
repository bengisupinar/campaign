# syntax=docker/dockerfile:1

# Backend image. Every command under ./cmd is built into /app, so future binaries
# (seed, mock provider, worker) run from this same image with a different `command`.

FROM golang:1.27-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM alpine:3.24
# busybox wget (used by the compose healthcheck) ships with alpine; the CA bundle is
# already present for outbound TLS calls.
RUN addgroup -S app && adduser -S -G app -H -u 10001 app
COPY --from=build /out/ /app/
USER app
EXPOSE 8080
CMD ["/app/api"]
