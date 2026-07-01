FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Build multiplexer proxy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o proxy .

# Runtime image built on Alpine
FROM alpine:latest

# Install CA certificates for HTTPS requests (GitHub API)
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /opt/github-mux

# Copy our proxy
COPY --from=builder /app/proxy /usr/local/bin/proxy

ENTRYPOINT ["/usr/local/bin/proxy", "/etc/github-mux/config.yaml"]
