---
name: actions-runner-integration
description: Verification checklist and maintenance guide for integrating with the official GitHub actions/runner binaries. Use this when updating the official runner version or debugging broken worker spawns.
---

# Actions-Runner Integration Maintenance Guide

The `multi-listener-runner` project intercepts the internal execution flow of the official GitHub Actions runner by swapping binaries. When the official runner version is updated, agents must verify the following integration points remain stable:

### 1. Binary Interception Paths
- We rely on replacing `/actions-runner/bin/Runner.Listener` and `/actions-runner/bin/Runner.Worker`.
- **Verification:** Ensure the official release still uses these exact binary names. If GitHub renames them (e.g., `Runner.Listener.dll` on .NET changes), the Dockerfile interception steps must be updated.

### 2. The `spawnclient` Command Signature
- **Critical Integration:** The proxy relies entirely on how `Runner.Listener` invokes `Runner.Worker`.
- We currently intercept: `Runner.Worker spawnclient <pipeHandleOut> <pipeHandleIn>`
- **Verification:** Search the official runner source code (`src/Runner.Worker/Program.cs` and `src/Runner.Listener/JobDispatcher.cs`) for `"spawnclient"`. Verify that the worker still expects exactly 3 arguments and that `args[1]` is the input pipe and `args[2]` is the output pipe.
- **Action:** If arguments change (e.g., new flags or configuration payload), `cmd/shim/main.go` and `cmd/worker-shim/main.go` must be updated to correctly proxy these new arguments.

### 3. Anonymous Pipes & File Descriptors
- The `worker-shim` binds its local pipes to File Descriptors `3` and `4` via `ExtraFiles`.
- **Verification:** Ensure the official runner runtime still reads/writes standard unbuffered streams via these descriptors. If the runner switches to named pipes or a different IPC mechanism (like gRPC), the `worker-shim` TCP proxy logic must be completely rewritten.

### 4. Graceful Shutdown Signals
- The `Orchestrator` sends `SIGINT` to the `Runner.Listener` processes to gracefully drain them during shutdown.
- **Verification:** Check official release notes for any changes to how the runner handles `SIGINT` or `SIGTERM`. It must continue to reject new jobs but wait for active jobs to finish.

### 5. Exit Code Propagation
- The proxy depends on `Runner.Worker` exiting with a `0` code for success, and non-zero for failure, which the `worker-shim` proxies back over the HTTP `/wait` endpoint.
- **Verification:** Verify that `worker-shim` accurately captures `*exec.ExitError` from the real worker and propagates the exact int value.

### 6. Base Image & OS Dependencies
- We build our own image from `ubuntu:22.04` (jammy). There is **no third-party base image dependency**.
- The image uses `dumb-init` as PID 1 for proper zombie reaping, and configures `en_US.UTF-8` locale.
- **User Setup:** runner UID=1001, GID=121, docker GID=500. These must stay consistent for volume permission compatibility.
- **Docker:** We install Docker CE (docker-ce, docker-ce-cli, docker-buildx-plugin, containerd.io, docker-compose-plugin) from Docker's official APT repository (`https://download.docker.com/linux/ubuntu`), NOT Ubuntu's `docker.io` package.
- **DinD Constraint:** Ephemeral worker containers MUST NOT use any third-party entrypoint for Docker startup. The Orchestrator injects a safe inline bash wrapper as the `Entrypoint` that executes `sudo service docker start` if `START_DOCKER_SERVICE=true`, before `exec`ing the shim.
- **Installed Tools:** git, git-lfs, build-essential, python3/pip3, nodejs, openssh-client, wget, rsync, zstd, gosu, and development libraries. See the Dockerfile for the full list.

### 7. Bumping Dependencies
The following dependencies must be maintained independently:

#### 7a. GitHub Actions Runner
- **Action:** Check the [actions/runner releases page](https://github.com/actions/runner/releases) for the latest version.
- **Action:** Update `ARG GH_RUNNER_VERSION="X.XXX.X"` in the `Dockerfile`.
- **Verification:** Review release notes to ensure `installdependencies.sh` and `spawnclient` arguments haven't changed.

#### 7b. Docker CE
- Docker CE is installed from Docker's official APT repository. Versions are pinned to the `jammy stable` channel.
- **Action:** If a specific Docker version is needed, pin it with `docker-ce=<version>` in the Dockerfile.
- **Verification:** Ensure the GPG key URL (`https://download.docker.com/linux/ubuntu/gpg`) is still valid.

#### 7c. Git LFS
- Git LFS is downloaded from its GitHub releases page at build time (latest version auto-resolved via GitHub API).
- **Action:** If you need to pin a version, replace the API call with a hardcoded version string.

#### 7d. Ubuntu Base OS
- We use `ubuntu:22.04` (jammy, LTS until April 2027).
- **Action:** When migrating to a newer Ubuntu LTS (e.g., 24.04 noble), update the Docker CE APT source line and verify all package names still exist.

