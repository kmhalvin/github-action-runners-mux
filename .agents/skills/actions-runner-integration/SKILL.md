---
name: actions-runner-integration
description: Verification checklist and maintenance guide for integrating with the official GitHub actions/runner binaries. Use this when updating the official runner version or debugging broken worker spawns.
---

# Actions-Runner Integration Maintenance Guide

The `multi-listener-runner` project integrates with the official GitHub Actions runner using the `actions/scaleset` library and JIT (Just-In-Time) configuration tokens. When the official runner version is updated, agents must verify the following integration points remain stable:

### 1. Binary Execution Paths
- We rely on executing `/actions-runner/bin/Runner.Listener` natively inside the Docker container.
- **Verification:** Ensure the official release still uses this exact binary name. If GitHub renames it (e.g., `Runner.Listener.dll` on .NET changes), the `worker-launcher` execution steps must be updated.

### 2. JIT Configuration Command
- **Critical Integration:** The worker-launcher starts the runner using the following command signature:
  `Runner.Listener run --startuptype service --jitconfig <TOKEN>`
- **Verification:** Check the official runner source code and release notes for any changes to how `--jitconfig` tokens are passed. 
- **Action:** If arguments change (e.g., new flags or configuration payload), `cmd/worker-launcher/main.go` must be updated to correctly pass the new arguments.

### 3. Graceful Shutdown Signals
- The `Multiplexer` handles graceful shutdown via native `context.Context` cancellation passed to the `actions/scaleset` library.
- The container terminates when the inner `Runner.Listener` completes its job.

### 4. Base Image & OS Dependencies
- We build our own image from `ubuntu:22.04` (jammy). There is **no third-party base image dependency**.
- The image uses `dumb-init` as PID 1 for proper zombie reaping, and configures `en_US.UTF-8` locale.
- **User Setup:** runner UID=1001, GID=121, docker GID=500. These must stay consistent for volume permission compatibility.
- **Docker:** We install Docker CE (docker-ce, docker-ce-cli, docker-buildx-plugin, containerd.io, docker-compose-plugin) from Docker's official APT repository (`https://download.docker.com/linux/ubuntu`), NOT Ubuntu's `docker.io` package.
- **DinD Constraint:** Ephemeral worker containers MUST NOT use any third-party entrypoint for Docker startup. The Orchestrator injects a safe inline bash wrapper as the `Entrypoint` that executes `sudo service docker start` if `START_DOCKER_SERVICE=true`, before `exec`ing the `worker-launcher`.
- **Installed Tools:** git, git-lfs, build-essential, python3/pip3, nodejs, openssh-client, wget, rsync, zstd, gosu, and development libraries. See the Dockerfile for the full list.

### 5. Bumping Dependencies
The following dependencies must be maintained independently:

#### 5a. GitHub Actions Runner
- **Action:** Check the [actions/runner releases page](https://github.com/actions/runner/releases) for the latest version.
- **Action:** Update `ARG GH_RUNNER_VERSION="X.XXX.X"` in the `Dockerfile`.

#### 5b. Docker CE
- Docker CE is installed from Docker's official APT repository. Versions are pinned to the `jammy stable` channel.
- **Action:** If a specific Docker version is needed, pin it with `docker-ce=<version>` in the Dockerfile.
- **Verification:** Ensure the GPG key URL (`https://download.docker.com/linux/ubuntu/gpg`) is still valid.

#### 5c. Git LFS
- Git LFS is downloaded from its GitHub releases page at build time (latest version auto-resolved via GitHub API).
- **Action:** If you need to pin a version, replace the API call with a hardcoded version string.

#### 5d. Ubuntu Base OS
- We use `ubuntu:22.04` (jammy, LTS until April 2027).
- **Action:** When migrating to a newer Ubuntu LTS (e.g., 24.04 noble), update the Docker CE APT source line and verify all package names still exist.

### 6. Ephemeral Worker Isolation & Cleanup
- **Cleanup Mechanism:** Ephemeral cleanup is entirely dependent on Docker auto-removing the container (`AutoRemove: true`). The worker container's `_work` directory is generated strictly inside the container's ephemeral filesystem layer.

### 7. Process Management & Signal Routing
- **Zombie Reaping:** The `os/exec` package in `worker-launcher` automatically reaps its direct children when `cmd.Wait()` is called. Reparented orphans are safely and exclusively reaped by `dumb-init` acting as PID 1 in the containers.
