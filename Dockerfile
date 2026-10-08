# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
WORKDIR /workspace

# Declared without defaults on purpose. These are BuildKit's predefined platform
# args, and giving them a default suppresses the value BuildKit injects, which
# silently pins every cross-build to the default and ships, for example, an
# x86-64 binary inside a linux/arm64 image.
ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o manager ./cmd

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
