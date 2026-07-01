FROM golang:latest AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Build proxy and worker-shim
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o proxy .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o worker-shim ./cmd/worker-shim

# ── GitHub Actions Runner ────────────────────────────────────────────────────
ARG GH_RUNNER_VERSION="2.335.1"
ARG TARGET_ARCH="x64"

# Download and extract the runner in the builder stage
RUN apt-get update && apt-get install -y curl tar \
    && mkdir -p /actions-runner \
    && cd /actions-runner \
    && curl -L -o actions.tar.gz "https://github.com/actions/runner/releases/download/v${GH_RUNNER_VERSION}/actions-runner-linux-${TARGET_ARCH}-${GH_RUNNER_VERSION}.tar.gz" \
    && tar -zxf actions.tar.gz \
    && rm -f actions.tar.gz bin/Runner.Worker bin/Runner.PluginHost

# Runtime image built on Ubuntu 24.04 (noble)
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV RUNNER_ALLOW_RUNASROOT=1
ENV PATH=$PATH:/actions-runner
ENV LANG=en_US.UTF-8
ENV LANGUAGE=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8
ENV AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# ── Truly Minimal packages ────────────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    dumb-init \
    && rm -rf /var/lib/apt/lists/*

# ── Copy Runner & Dependencies ────────────────────────────────────────────────
COPY --from=builder /actions-runner /actions-runner
WORKDIR /actions-runner
RUN ./bin/installdependencies.sh && mkdir -p /_work

# ── Copy proxy and shim ─────────────────────────────────────────────────
COPY --from=builder /app/proxy /usr/local/bin/proxy
COPY --from=builder /app/worker-shim /usr/local/bin/worker-shim

WORKDIR /opt/runners
ENTRYPOINT ["/usr/bin/dumb-init", "--", "/usr/local/bin/proxy", "/etc/multi-listener/config.yaml"]
