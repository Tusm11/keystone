# Multi-stage build: compile with the Go toolchain, ship a tiny static image.
# Final image is ~15MB — faster to pull, smaller attack surface than a
# base-image approach. Standard Go production pattern.

FROM golang:1.23-alpine AS build
WORKDIR /src
# Deps first so the module layer caches across source changes.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=0 → fully static binary, works in scratch/alpine without libc.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/clickworker ./cmd/clickworker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 1001 keystone
COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/clickworker /usr/local/bin/clickworker
USER keystone
EXPOSE 8080
# Default to the server binary; override with `command: ["clickworker"]` for the worker.
ENTRYPOINT ["/usr/local/bin/server"]
