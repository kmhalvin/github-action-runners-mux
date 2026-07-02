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
- **Action:** If arguments change (e.g., new flags or configuration payload), `cmd/worker-shim/main.go` and `cmd/worker-launcher/main.go` must be updated to correctly proxy these new arguments.

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
- We build our own image from `ubuntu:24.04` (noble). There is **no third-party base image dependency**.
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
- We use `ubuntu:24.04` (noble, LTS).
- **Action:** When migrating to a newer Ubuntu LTS, update the Docker CE APT source line and verify all package names still exist.

### 8. Ephemeral Worker Isolation & Cleanup
- **Constraint:** Ephemeral worker containers MUST NEVER mount the host's `/opt/runners` volume. Doing so exposes the `.credentials` files of all registered runners to the CI job, creating a severe vulnerability.
- **Cleanup Mechanism:** The worker container's `_work` directory is generated strictly inside the container's ephemeral filesystem layer (even though the path is `/opt/runners/.../_work`). Ephemeral cleanup is entirely dependent on Docker auto-removing the container (`AutoRemove: true`). Do not implement manual host-side cleanup scripts for `_work`.

### 9. Process Management & Signal Routing
- **Graceful Shutdown:** To gracefully drain the runners, `SIGINT` MUST be sent directly to the `Runner.Listener` process ID (`Cmd.Process.Pid`), NEVER to the process group (`-PGID`). Killing the process group instantly kills the `shim` proxy, which severs the TCP connection and immediately aborts any active CI jobs.
- **Zombie Reaping:** The `os/exec` package automatically reaps its direct children when `cmd.Wait()` is called. Never implement background `syscall.Wait4(-1)` loops in Go, as this introduces severe race conditions with `cmd.Wait()`. Reparented orphans are safely and exclusively reaped by `dumb-init` acting as PID 1 in the containers.

### 10. Config Sync & Deregistration
- **Constraint:** The official runner does not automatically deregister itself when removed from a local config file.
- **Mechanism:** The proxy caches registration tokens in `.mux-meta.json` inside the runner's directory upon registration. When a runner is removed from `config.yaml`, the orchestrator uses this cached token to execute `./config.sh remove --token <token>` before physically deleting the directory. This synchronization logic must be preserved to prevent ghost runners in GitHub.

### 11. Dual-Boot Worker Launcher (Hybrid Architecture)
- **Constraint:** The `github-mux-worker` image uses a single `cmd/worker-launcher` that acts as a dual-boot proxy. It listens on TCP `:9000` (for Standalone proxy streams) and HTTP `:9001` (for Scale Set JIT payloads via `/start`).
- **Mechanism:** A strict `sync.Once` block MUST protect the execution of `startContainer()`. This guarantees that if both ports are hit simultaneously (or consecutively), the container only ever executes the runner payload once, locking itself permanently into either Standalone or Scale Set mode.
- **Shutdown Safety:** The `/wait` endpoint MUST implement a graceful timeout (e.g., 5 seconds) after `waitFetched` is closed. This ensures the container exits cleanly even if the Orchestrator crashes or fails to scrape the final exit code.

### 12. Universal Warm Pool
- **Constraint:** The Orchestrator maintains a centralized warm pool that serves both Standalone and Scale Set managers.
- **Mechanism:** Containers in the warm pool (`github-mux-warm-*`) are completely unconfigured and mode-agnostic. The Orchestrator does not differentiate them until it claims one using `AllocateStandalone` (for TCP) or `AllocateJIT` (for HTTP). The Orchestrator is purely responsible for container lifecycle and capacity, NOT runner authentication.
