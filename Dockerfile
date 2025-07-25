# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset@sha256:564c50dbad93a50dfe1439b295f021dae0bdc8c2aef3ad8be7b2a4dde52f0e2f AS builder
ARG TARGETOS
ARG TARGETARCH

ENV GOTOOLCHAIN=auto
WORKDIR /opt/app-root/src
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY internal/ internal/
COPY pkg/ pkg/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go


FROM registry.access.redhat.com/ubi9-micro@sha256:955512628a9104d74f7b3b0a91db27a6bbecdd6a1975ce0f1b2658d3cd060b98
WORKDIR /
COPY --from=builder /opt/app-root/src/manager .
COPY LICENSE /licenses/
USER 65532:65532

# It is mandatory to set these labels
LABEL name="Kueue External Admission Controller"
LABEL description="Kueue External Admission Controller"
LABEL com.redhat.component="Kueue External Admission Controller"
LABEL io.k8s.description="Kueue External Admission Controller"
LABEL io.k8s.display-name="Kueue External Admission Controller"

ENTRYPOINT ["/manager"]
