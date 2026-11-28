# syntax=docker/dockerfile:1

# One Dockerfile for both commands: --build-arg APP=server|gateway.
FROM golang:1.26 AS build
ARG APP=server
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/app ./cmd/${APP} \
 && mkdir /out/data

# Static binary, no shell, runs as uid 65532. Time zones come from the
# embedded time/tzdata, CA certificates from the base image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
# Writable directory for the gateway buffer (FILE_STORAGE_PATH).
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
ENTRYPOINT ["/app"]
