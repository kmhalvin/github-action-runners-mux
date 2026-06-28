FROM golang:latest AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Build multiplexer and worker-launcher
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o proxy .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o worker-launcher ./cmd/worker-launcher

# Runtime image built on Ubuntu 22.04 (jammy)
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive
ENV RUNNER_ALLOW_RUNASROOT=1
ENV PATH=$PATH:/actions-runner
ENV LANG=en_US.UTF-8
ENV LANGUAGE=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8
ENV AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# ── Essential OS packages ────────────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    jq \
    gnupg \
    gpg-agent \
    tar \
    unzip \
    zip \
    apt-transport-https \
    sudo \
    dirmngr \
    locales \
    gosu \
    dumb-init \
    libc-bin \
    && echo "en_US.UTF-8 UTF-8" >> /etc/locale.gen \
    && locale-gen \
    && rm -rf /var/lib/apt/lists/*

# ── Docker CE from Docker's official repository ─────────────────────────────
RUN mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
       | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu jammy stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
       docker-ce \
       docker-ce-cli \
       docker-buildx-plugin \
       containerd.io \
       docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/* \
    && sed -i 's/ulimit -Hn/# ulimit -Hn/g' /etc/init.d/docker \
    && echo -e '#!/bin/sh\ndocker compose --compatibility "$@"' > /usr/local/bin/docker-compose \
    && chmod +x /usr/local/bin/docker-compose

# ── Development & workflow tools ─────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    build-essential \
    lsb-release \
    zlib1g-dev \
    zstd \
    gettext \
    libcurl4-openssl-dev \
    libpq-dev \
    pkg-config \
    libyaml-dev \
    python3 \
    python3-pip \
    python3-setuptools \
    python3-venv \
    nodejs \
    inetutils-ping \
    wget \
    openssh-client \
    rsync \
    && rm -rf /var/lib/apt/lists/*

# ── Git LFS ──────────────────────────────────────────────────────────────────
RUN DPKG_ARCH="$(dpkg --print-architecture)" \
    && GIT_LFS_VERSION=$(curl -sL -H "Accept: application/vnd.github+json" \
       https://api.github.com/repos/git-lfs/git-lfs/releases/latest \
       | jq -r '.tag_name' | sed 's/^v//g') \
    && curl -sL "https://github.com/git-lfs/git-lfs/releases/download/v${GIT_LFS_VERSION}/git-lfs-linux-${DPKG_ARCH}-v${GIT_LFS_VERSION}.tar.gz" \
       -o /tmp/lfs.tar.gz \
    && tar -xzf /tmp/lfs.tar.gz -C /tmp \
    && "/tmp/git-lfs-${GIT_LFS_VERSION}/install.sh" \
    && rm -rf /tmp/lfs.tar.gz "/tmp/git-lfs-${GIT_LFS_VERSION}"

# ── User & group setup (matching myoung34 IDs for volume compat) ─────────────
RUN groupadd -g 500 docker || : \
    && sed -e 's/Defaults.*env_reset/Defaults env_keep = "HTTP_PROXY HTTPS_PROXY NO_PROXY FTP_PROXY http_proxy https_proxy no_proxy ftp_proxy"/' -i /etc/sudoers \
    && echo '%sudo ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers \
    && groupadd -g 121 runner \
    && useradd -mr -d /home/runner -u 1001 -g 121 runner \
    && usermod -aG sudo runner \
    && usermod -aG docker runner

# ── GitHub Actions Runner ────────────────────────────────────────────────────
ARG GH_RUNNER_VERSION="2.335.1"
ARG TARGET_ARCH="x64"

RUN mkdir -p /opt/hostedtoolcache

WORKDIR /actions-runner
RUN curl -L -o actions.tar.gz \
      "https://github.com/actions/runner/releases/download/v${GH_RUNNER_VERSION}/actions-runner-linux-${TARGET_ARCH}-${GH_RUNNER_VERSION}.tar.gz" \
    && tar -zxf actions.tar.gz \
    && rm -f actions.tar.gz \
    && ./bin/installdependencies.sh \
    && mkdir -p /_work \
    && chown -R runner /_work /actions-runner /opt/hostedtoolcache

# ── Copy our proxy and launcher ─────────────────────────────────────────────────
COPY --from=builder /app/proxy /usr/local/bin/proxy
COPY --from=builder /app/worker-launcher /usr/local/bin/worker-launcher

WORKDIR /opt/runners
ENTRYPOINT ["/usr/bin/dumb-init", "--", "/usr/local/bin/proxy", "/etc/multi-listener/config.yaml"]
