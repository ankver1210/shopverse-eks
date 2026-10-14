# syntax=docker/dockerfile:1
# ---------------------------------------------------------------------------
# Multi-stage build:
#   build   -> compiles a static binary (cross-compiles natively, so building
#              amd64 images on an Apple Silicon Mac is fast)
#   test    -> go vet + unit tests (CI builds this with: docker build --target test .)
#   runtime -> distroless "static" image: no shell, no package manager, non-root
# ---------------------------------------------------------------------------
ARG GO_IMAGE=golang:1.26-alpine

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/app .

FROM build AS test
RUN go vet ./... && go test ./...

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
ARG APP_VERSION=dev
ENV APP_VERSION=${APP_VERSION} \
    PORT=8080
COPY --from=build /out/app /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]
