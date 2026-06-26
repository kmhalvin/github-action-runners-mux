FROM golang:1.22 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Build the proxy with CGO disabled
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o proxy .
# Build the shim
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o shim ./cmd/shim

# Use myoung34/github-runner as the base to inherit all dependencies natively
FROM myoung34/github-runner:ubuntu-jammy

# Override their default ENTRYPOINT to just start our proxy.
COPY --from=builder /app/proxy /usr/local/bin/proxy
COPY --from=builder /app/shim /usr/local/bin/shim

# The base image provides /actions-runner which we will use as a template.
# Our proxy will copy this to /opt/runners/* dynamically.

WORKDIR /opt/runners
ENTRYPOINT ["/usr/local/bin/proxy", "/etc/multi-listener/config.yaml"]
