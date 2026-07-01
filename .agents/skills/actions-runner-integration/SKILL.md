---
name: actions-runner-integration
description: Verification checklist and maintenance guide for integrating with the official GitHub actions/runner binaries. Use this when updating the official runner version or debugging broken worker spawns.
---

# Actions-Runner Integration Maintenance Guide

The `github-mux` project integrates with the official GitHub Actions runner using the `actions/scaleset` library and JIT (Just-In-Time) configuration tokens. When the official runner version is updated, agents must verify the following integration points remain stable:

### 1. Binary Execution Paths
- We rely on executing the official `/home/runner/run.sh` wrapper script natively inside the Docker container.
- **Verification:** Ensure the official release still ships with `run.sh` and it correctly passes environment variables down to the `Runner.Listener` binary.

### 2. JIT Configuration Command
- **Critical Integration:** The worker-launcher starts the runner by executing `run.sh` with the JIT configuration passed via the `ACTIONS_RUNNER_INPUT_JITCONFIG` environment variable. The official runner extracts this and uses it to automatically provision an ephemeral `.runner` config file.
- **Verification:** Check the official runner source code and release notes for any changes to how JIT config tokens are passed. Do not revert to calling `Runner.Listener` directly, as `run.sh` is required for proper signal trapping and restarts.

### 3. Graceful Shutdown Signals
- The `Multiplexer` handles graceful shutdown via native `context.Context` cancellation passed to the `actions/scaleset` library.
- The container terminates when the inner `Runner.Listener` completes its job.

### 4. Base Image & OS Dependencies
- We use the official `ghcr.io/actions/actions-runner` container as the base image for our ephemeral workers (`Dockerfile.runner`).
- The official image currently builds on **Ubuntu 24.04 (Noble)** via `.NET runtime-deps`.
- We layer our own tooling on top by temporarily switching to `USER root` to install packages, and then switching back to `USER runner`.
- The image uses `dumb-init` as PID 1 for proper zombie reaping.
- **User Setup:** We rely on the official image's `runner` user (UID=1001) and `docker` group (GID=123). We must ensure `sudo` remains passwordless for this user.
- **Docker:** We install Docker CE (docker-ce, docker-ce-cli, docker-buildx-plugin, containerd.io, docker-compose-plugin) from Docker's official APT repository (`https://download.docker.com/linux/ubuntu`), NOT Ubuntu's `docker.io` package.
- **DinD Constraint:** The Orchestrator injects a safe inline bash wrapper as the `Cmd` that executes `sudo service docker start` if `START_DOCKER_SERVICE=true`, before `exec`ing the `worker-launcher`.
- **Installed Tools:** git, git-lfs, build-essential, python3/pip3, nodejs, openssh-client, wget, rsync, zstd, gosu, and development libraries. See the Dockerfile for the full list.

### 5. Bumping Dependencies
The following dependencies must be maintained independently:

#### 5a. GitHub Actions Runner
- **Action:** The GitHub Actions Runner binary is automatically bumped by pulling the latest `ghcr.io/actions/actions-runner:latest` base image in `Dockerfile.runner`. No manual tarball downloads are required.

#### 5b. Docker CE
- Docker CE is installed from Docker's official APT repository. Versions are pinned to the `jammy stable` channel.
- **Action:** If a specific Docker version is needed, pin it with `docker-ce=<version>` in the Dockerfile.
- **Verification:** Ensure the GPG key URL (`https://download.docker.com/linux/ubuntu/gpg`) is still valid.

#### 5c. Git LFS
- Git LFS is downloaded from its GitHub releases page at build time (latest version auto-resolved via GitHub API).
- **Action:** If you need to pin a version, replace the API call with a hardcoded version string.

#### 5d. Ubuntu Base OS
- The proxy (`Dockerfile`) uses `alpine:latest`.
- The worker (`Dockerfile.runner`) inherits its OS (currently Ubuntu 24.04) from the official GitHub Actions Runner image. If you pin a specific tag via `RUNNER_IMAGE_TAG` (e.g. `ubuntu-22.04`), ensure your apt-get packages and Docker GPG keys match the target distro.

### 6. Ephemeral Worker Isolation & Cleanup
- **Cleanup Mechanism:** Ephemeral cleanup is entirely dependent on Docker auto-removing the container (`AutoRemove: true`). The worker container's `_work` directory is generated strictly inside the container's ephemeral filesystem layer.

### 7. Process Management & Signal Routing
- **Zombie Reaping:** The `os/exec` package in `worker-launcher` automatically reaps its direct children when `cmd.Wait()` is called. Reparented orphans are safely and exclusively reaped by `dumb-init` acting as PID 1 in the containers.
