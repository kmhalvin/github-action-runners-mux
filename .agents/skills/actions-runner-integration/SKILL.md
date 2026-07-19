---
name: actions-runner-integration
description: Verification checklist and maintenance guide for integrating with the official GitHub actions/runner binaries. Use this when updating the official runner version or debugging broken worker spawns.
---

# Actions-Runner Integration Maintenance Guide

The `github-mux` project intercepts the internal execution flow of the official GitHub Actions runner by swapping binaries. When the official runner version is updated, agents must verify the following integration points remain stable:

### 1. Binary Interception Paths
- We rely on replacing `/actions-runner/bin/Runner.Listener` and `/actions-runner/bin/Runner.Worker`.
- **Verification:** Ensure the official release still uses these exact binary names. If GitHub renames them (e.g., `Runner.Listener.dll` on .NET changes), the Dockerfile interception steps must be updated.
- **Proxy image (`Dockerfile`):** Downloads the runner tarball, then deletes `bin/Runner.Worker` and `bin/Runner.PluginHost` so the shim can be injected at runtime via `pkg/standalone/env.go`.
- **Worker image (`Dockerfile.runner`):** Downloads the full runner tarball (keeps `Runner.Worker` — the worker-launcher spawns it directly).

### 2. The `spawnclient` Command Signature
- **Critical Integration:** The proxy relies entirely on how `Runner.Listener` invokes `Runner.Worker`.
- We currently intercept: `Runner.Worker spawnclient <pipeHandleOut> <pipeHandleIn>`
- **Verification:** Search the official runner source code (`src/Runner.Worker/Program.cs` and `src/Runner.Listener/JobDispatcher.cs`) for `"spawnclient"`. Verify that the worker still expects exactly 3 arguments and that `args[1]` is the input pipe and `args[2]` is the output pipe.
- **Action:** If arguments change (e.g., new flags or configuration payload), `cmd/worker-shim/main.go` and `cmd/worker-launcher/main.go` must be updated to correctly proxy these new arguments.

### 3. Anonymous Pipes & File Descriptors
- The `worker-launcher` binds its local pipes to File Descriptors `3` and `4` via `cmd.ExtraFiles` when spawning the real `Runner.Worker`. The `worker-shim` merely wraps the file descriptors passed via arguments (`os.Args[2]` and `os.Args[3]`) by the `Runner.Listener` using `os.NewFile()`.
- **Verification:** Ensure the official runner runtime still reads/writes standard unbuffered streams via these descriptors. If the runner switches to named pipes or a different IPC mechanism (like gRPC), the `worker-shim` and `worker-launcher` TCP proxy logic must be completely rewritten.

### 4. Graceful Shutdown Signals
- The `Orchestrator` sends `SIGINT` to the `Runner.Listener` processes to gracefully drain them during shutdown.
- **Verification:** Check official release notes for any changes to how the runner handles `SIGINT` or `SIGTERM`. It must continue to reject new jobs but wait for active jobs to finish.
- **Note:** We set `RUNNER_MANUALLY_TRAP_SIG=1` in both Dockerfiles. This variable is evaluated exclusively by the official `run.sh` bash wrapper (not the `.NET` source code). When set, `run.sh` uses bash job control (`set -m`) to spawn the .NET process in the background and sets a bash trap (`trap 'kill -INT -$PID' INT TERM`) to manually forward signals, bypassing Docker PID 1 quirks. If the official runner modifies `run.sh` signal trapping, this env var's effect must be re-verified.

### 5. Exit Code Propagation
- The **worker-launcher** (not the shim) captures `*exec.ExitError` from the real `Runner.Worker` process and extracts the exit code via `exitError.ExitCode()`.
- The **worker-shim** fetches the exit code from the worker-launcher via HTTP `GET http://<workerIP>:9001/wait` and calls `os.Exit(exitCode)` with the received value.
- **Verification:** Verify that `worker-launcher` accurately captures `*exec.ExitError` from `cmd.Wait()` and that the shim correctly decodes the JSON response from `/wait`.
- **Exit Code Encoding:** The official runner uses `TaskResultUtil` with a return code offset of 100 (exit code 100 = Succeeded, 102 = Failed, 1 = invalid → Failed). The worker-launcher passes through the raw exit code; the shim exits with it. Verify `TranslateFromReturnCode` in `src/Runner.Listener/JobDispatcher.cs` hasn't changed the offset.

### 6. Base Image & OS Dependencies
- We build our own image from `ubuntu:24.04` (noble). There is **no third-party base image dependency**.
- The image uses `dumb-init` as PID 1 for proper zombie reaping, and configures `en_US.UTF-8` locale.
- **User Setup:** runner UID=1001, GID=121, docker GID=500. These must stay consistent for volume permission compatibility.
- **Non-Root Worker:** `Dockerfile.runner` ends with `USER runner` — worker containers run CI jobs as the non-root `runner` user. `sudo` is configured (NOPASSWD) so the inline entrypoint can run `sudo service docker start` for Docker-in-Docker. The proxy `Dockerfile` does NOT use `USER runner` (it needs root to manage runner processes in `/opt/runners/`).
- **Required Env Vars (both Dockerfiles):**
  - `RUNNER_MANUALLY_TRAP_SIG=1` — tells the runner to manually trap signals.
  - `ImageOS=ubuntu24` — tells the runner to report `ubuntu24` as the OS label (matches our Ubuntu 24.04 base; required for `runs-on: ubuntu-24.04` label matching).
  - `ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=true` — set by the orchestrator in `pool.go` for worker containers. Without this, runner trace logs only go to the `_diag/` folder inside the container (invisible after `AutoRemove: true`).
- **Docker:** We install Docker CE (docker-ce, docker-ce-cli, docker-buildx-plugin, containerd.io, docker-compose-plugin) from Docker's official APT repository (`https://download.docker.com/linux/ubuntu`), NOT Ubuntu's `docker.io` package.
- **DinD Constraint:** Ephemeral worker containers MUST NOT use any third-party entrypoint for Docker startup. The Orchestrator injects a safe inline bash wrapper as the `Entrypoint` that executes `sudo service docker start` if `START_DOCKER_SERVICE=true`, before `exec`-ing the **worker-launcher** (not the shim). The shim runs on the proxy side; the worker-launcher runs inside the worker container.
- **Installed Tools:** git, git-lfs, build-essential, python3/pip3, nodejs, openssh-client, wget, rsync, zstd, gosu, and development libraries. See the Dockerfile for the full list.

### 7. Bumping Dependencies
The following dependencies must be maintained independently:

#### 7a. GitHub Actions Runner
- **Action:** Check the [actions/runner releases page](https://github.com/actions/runner/releases) for the latest version.
- **Action:** Update `ARG GH_RUNNER_VERSION="X.XXX.X"` in **both** `Dockerfile` and `Dockerfile.runner`.
- **Verification:** Review release notes to ensure `installdependencies.sh` and `spawnclient` arguments haven't changed.
- **Auto-Update Disabled:** We pass `--disableupdate` to `config.sh` in `pkg/standalone/env.go` to prevent the runner from self-mutating mid-flight. This must be preserved when bumping versions.

#### 7b. Docker CE
- Docker CE is installed from Docker's official APT repository. The APT source line uses the `noble stable` channel (matching our Ubuntu 24.04 base).
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
- **Cleanup Mechanism:** The worker container's `_work` directory is generated strictly inside the container's ephemeral filesystem layer at `/_work` (created in `Dockerfile.runner`) or `/actions-runner/_work` (relative to `cmd.Dir = "/actions-runner"` in the worker-launcher). Ephemeral cleanup is entirely dependent on Docker auto-removing the container (`AutoRemove: true`). Do not implement manual host-side cleanup scripts for `_work`.

### 9. Process Management & Signal Routing
- **Graceful Shutdown:** To gracefully drain the runners, `SIGINT` MUST be sent directly to the `Runner.Listener` process ID (`Cmd.Process.Pid`), NEVER to the process group (`-PGID`). Killing the process group instantly kills the `shim` proxy, which severs the TCP connection and immediately aborts any active CI jobs.
- **Zombie Reaping:** The `os/exec` package automatically reaps its direct children when `cmd.Wait()` is called. Never implement background `syscall.Wait4(-1)` loops in Go, as this introduces severe race conditions with `cmd.Wait()`. Reparented orphans are safely and exclusively reaped by `dumb-init` acting as PID 1 in the containers.

### 10. Config Sync & Deregistration
- **Constraint:** The official runner does not automatically deregister itself when removed from a local config file.
- **Mechanism:** The proxy caches registration tokens in `.mux-meta.json` inside the runner's directory upon registration. When a runner is removed from `config.yaml`, the orchestrator uses this cached token to execute `./config.sh remove --token <token>` before physically deleting the directory. This synchronization logic must be preserved to prevent ghost runners in GitHub.

### 11. Dual-Boot Worker Launcher (Hybrid Architecture)
- **Constraint:** The `github-mux-worker` image uses a single `cmd/worker-launcher` that acts as a dual-boot proxy. It listens on TCP `:9000` (for Standalone proxy streams) and HTTP `:9001` (for Scale Set JIT payloads via `/start`).
- **Mechanism:** A strict `sync.Once` field (`wl.startOnce`) MUST protect the execution of `runStandaloneWorker()` and `runJITWorker()`. This guarantees that if both ports are hit simultaneously (or consecutively), the container only ever executes the runner payload once, locking itself permanently into either Standalone or Scale Set mode.
- **Shutdown Safety:** The `/wait` endpoint MUST implement a graceful timeout (5 seconds) after `waitFetched` is closed. This ensures the container exits cleanly even if the Orchestrator crashes or fails to scrape the final exit code.

### 12. Config File Injection via TCP Header (Framed Protocol)
- **Critical Integration:** Worker containers do NOT mount the shared runner-data volume (security constraint — see Section 8). Instead, the runner's config files (`.runner`, `.credentials`) are injected at allocation time via a framed TCP header.
- **Protocol:** The worker-shim sends a 4-byte big-endian length prefix followed by JSON payload (`{"config_files": {".runner": "<base64>", ".credentials": "<base64>"}}`) as the first bytes on the TCP connection, BEFORE the raw pipe bridge begins. The worker-launcher reads this header, decodes the base64 data, and writes the files to `/actions-runner/` before spawning `Runner.Worker`.
- **Why both files:** `ConfigurationStore.GetSettings()` reads only the `.runner` file for settings. However, `.runner` references `.credentials` for the auth credential, so both must be present. Without them, `Runner.Worker` crashes with `ArgumentNullException: 'configuredSettings'`.
- **Verification:** If the framed header protocol changes, `cmd/worker-shim/main.go` (`writeFramedHeader`) and `cmd/worker-launcher/main.go` (`readFramedHeader`, `writeConfigFiles`) must be updated in sync.
- **Runner Dir Resolution:** The shim derives the runner directory from its own executable path (`filepath.Dir(filepath.Dir(os.Executable()))`), since the shim is installed at `<cfg.Dir>/bin/Runner.Worker`. It sends this directory path to the orchestrator so the correct config files can be read.

### 13. Pipe Streaming Invariants (Deadlock Prevention)
- **Worker-Launcher — close `workerWrite` + `wg.Wait()`:** After `Runner.Worker` exits (`cmd.Wait()` returns), the worker-launcher MUST close `workerWrite` (the child's write-end of the pipe) so that `io.Copy(conn, shimRead)` gets EOF and finishes flushing remaining data to the shim. Then `wg.Wait()` ensures all pipe data is fully flushed to TCP before reporting the exit code. Without this, the shim deadlocks waiting for the exit code, the 5s timeout fires, and the wrong exit code (1) is reported instead of the real one.
- **Worker-Shim — `<-errChan` (wait for ONE stream, not both):** The shim must wait for only ONE of two `io.Copy` goroutines to finish (`<-errChan`), NOT both. Waiting for both deadlocks because Stream 1 (listener→TCP) blocks on the listener's pipe, which only closes when the shim process exits. Stream 2 (TCP→listener) finishes when the worker-launcher closes the connection after flushing. Once Stream 2 finishes, the shim fetches the exit code and exits, which kills Stream 1 via `os.Exit`.
- **Verification:** If anyone refactors the streaming logic in either `cmd/worker-shim/main.go` or `cmd/worker-launcher/main.go`, these ordering invariants MUST be preserved.

### 14. `actions/scaleset` Go Library Integration
- **Dependency:** We rely on the official `github.com/actions/scaleset` library to manage Scale Set configurations and retrieve JIT config payloads.
- **Labeling Mechanism:** We construct a slice of `scaleset.Label` objects and pass them to `client.CreateRunnerScaleSet`. This ensures workflows targeting specific custom labels correctly route to our multiplexer.
- **JIT Retrieval:** JIT payloads are strictly generated and retrieved via `client.GenerateJitRunnerConfig` in `pkg/scaleset/scaler.go` on a per-job basis. The Orchestrator injects this JIT payload directly into the warm pool container via HTTP (`/start`).
- **Verification:** When bumping the `actions/scaleset` dependency version in `go.mod`, verify that the `CreateRunnerScaleSet` struct and the `GenerateJitRunnerConfig` response signature haven't introduced breaking changes.
- **Known GHES Limitation (Historical Context):** Be aware that older GitHub Enterprise Server (GHES) versions (like 3.18.x) have a strict ASP.NET model binder that rejects the serialization of the `type: "System"` JSON field sent by the library for labels (yielding a 400 Bad Request). If support for older GHES versions is ever required again, a custom HTTP request bypass (bypassing the library) will be necessary.

### 15. Upgrading Standalone Runners & Symlink Constraints
- **Constraint:** The .NET Core runtime (used by `Runner.Listener`) resolves symlinks to physical paths when locating its own executable (`Assembly.GetEntryAssembly().Location`). Consequently, it uses this physical path's parent folder as the root directory to search for configuration files (`.credentials`, `.runner`).
- **Mechanism:** Because of this, we **cannot** use symlinks to map `bin/` from the proxy base image into a standalone runner's persistent volume (`/opt/runners/...`). If we did, the listener would follow the symlink and erroneously look for `.credentials` in `/actions-runner/` instead of the runner's specific volume directory.
- **Upgrade Solution:** Upgrading standalone runner binaries is handled via an auto-sync mechanism in `pkg/standalone/env.go`. On every proxy startup, it executes `rsync -a --delete` to overwrite `bin/` and `externals/` in the runner's volume with the upgraded versions from the base image (`/actions-runner/`), while safely leaving the `.credentials` file intact.
