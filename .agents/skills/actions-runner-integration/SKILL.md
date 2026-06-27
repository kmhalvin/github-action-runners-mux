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

### 6. Base Image Dependency
- We currently build on top of `myoung34/github-runner:ubuntu-jammy` to inherit necessary OS dependencies.
- **Verification:** If the base image is changed (e.g., to the official `ghcr.io/actions/actions-runner`), ensure the installation directory is still `/actions-runner/`. Some images install the runner to `/home/runner/actions-runner/` or similar, which would completely break our Dockerfile `mv` and `COPY` interception steps!
- **Entrypoint & DinD Constraint:** The `myoung34` image provides an `entrypoint.sh` that handles DinD via `START_DOCKER_SERVICE=true`, but it also contains rogue runner registration logic. Our ephemeral worker containers MUST NEVER execute this entrypoint natively. Instead, the Orchestrator's spawn logic must manually override the `Entrypoint` with an inline wrapper that implements the `service docker start` logic before exec'ing the shim.
