# GitHub Action Runners Multiplexer

> An ephemeral, Docker-based GitHub Actions runner orchestrator designed to concurrently multiplex jobs across multiple repositories and organizations. Works with public GitHub and GitHub Enterprise hosts. **Supports both Standalone (Registration Token) and Scale Set (JIT/PAT) modes natively and simultaneously!**

The official GitHub Actions runner typically forces you to run a 1:1 mapping of runner processes to repositories or organizations. This orchestrator changes the paradigm: it acts as a central control plane that listens to an infinite number of scopes simultaneously, and dynamically provisions perfectly isolated, ephemeral Docker containers from a universal warm pool the exact moment a job arrives.

## Features

- **Hybrid Concurrency:** Run both traditional Standalone runners (using Registration Tokens) and modern Scale Set runners (using JIT configuration via PAT) side-by-side in the same configuration.
- **Docker as Source of Truth:** Container state is tracked via Docker labels and container names. On restart, the Orchestrator instantly recovers state from existing containers—no more orphaned workers or lost jobs.
- **Universal Warm Pool:** The Orchestrator maintains a centralized pool of pre-booted `ubuntu:24.04` containers. When a job arrives on *any* listener, a container is instantly claimed and converted into either a Standalone proxy or a native JIT executor on the fly.
- **Dual-Boot Worker Launcher:** The worker containers utilize a secure `sync.Once` dual-boot proxy that listens on both TCP (`:9000`) and HTTP (`:9001`). Depending on the mode, they seamlessly adapt their execution environment.
- **Native Autoscaling & Capacity Management:** Enforces strict global limits (`max_workers`). When capacity is hit, Standalone listeners are safely frozen (`SIGSTOP`) to prevent queuing, while Scale Set jobs remain pending until capacity frees up.
- **True Ephemeral Workers:** Every job executes inside a brand new, isolated Docker container that is immediately destroyed upon completion.

## Architecture

The project intercepts and orchestrates the official GitHub `actions/runner` ecosystem:
1. **The Standalone Manager (`pkg/standalone`)**: Boots native `Runner.Listener` processes using standard GitHub Registration Tokens. Upon job receipt, it allocates a worker via a local Unix socket and proxies traffic over TCP.
2. **The Scale Set Manager (`pkg/scaleset`)**: Uses GitHub's `scaleset` Go SDK and a Personal Access Token (PAT) to monitor scopes without maintaining heavy listener processes. It requests JIT payloads and pushes them directly to worker containers via HTTP.
3. **The Orchestrator (`pkg/orchestrator`)**: The brain of the operation. Manages the global Docker lifecycle, maintains the universal warm pool, tracks active assignments, and enforces global capacity constraints across all managers.

## Getting Started

### 1. Deploy
Run the Orchestrator proxy using Docker Compose:
```bash
docker-compose up -d --build
```
*Note: The proxy mounts `/var/run/docker.sock` to dynamically spawn the ephemeral worker containers alongside it.*

### 2. Configure via Dashboard
All configuration is managed through the web dashboard — there is no config file. After first boot:
- Open `http://localhost:8080` in your browser.
- Go to **Settings** to set `max_workers` (max concurrent worker containers) and `warm_workers` (pre-booted warm pool size).
- Go to **Add Runner** to register runner scopes. You can mix and match Standalone (Registration Token, auto-generated via OAuth) and Scale Set (PAT) modes.
- For Standalone runners, sign in via OAuth to the target GitHub host — the registration token is generated automatically.

### 3. Target from Workflows

**For Scale Set Mode:**
Use your Scale Set name as the `runs-on` label in your GitHub Actions workflows:
```yaml
jobs:
  build:
    runs-on: my-scale-set
    steps:
      - run: echo "Running on a JIT ephemeral runner!"
```

**For Standalone Mode:**
Use the labels you assigned to the runner (e.g., `self-hosted`, `linux`, `frontend`):
```yaml
jobs:
  build:
    runs-on: [self-hosted, linux, frontend]
    steps:
      - run: echo "Running on a standalone proxy ephemeral runner!"
```

### 4. Docker-in-Docker (Optional)
To allow workflows to run Docker commands natively inside the ephemeral workers, add the following environment variable to the `github-mux-proxy` service in your `docker-compose.yml`:
```yaml
environment:
  - WORKER_START_DOCKER_SERVICE=true
```

## Contributing
Please see the architectural logic and integration constraints detailed in the repository if you intend to upgrade the base `actions/runner` dependency. Because this orchestrator heavily instruments the runner execution flow (especially in Standalone mode), strict adherence to the [Integration Maintenance Guide](.agents/skills/actions-runner-integration/SKILL.md) is required.

## License
This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
