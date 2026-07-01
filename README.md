# GitHub Mux

> An ephemeral, Docker-based GitHub Actions runner orchestrator that concurrently multiplexes jobs across multiple repositories, organizations, and GitHub Enterprise hosts—powered by the official [`actions/scaleset`](https://github.com/actions/scaleset) Go library.

The official GitHub Actions runner forces you to run a 1:1 mapping of runner processes to repositories or organizations. This orchestrator changes the paradigm: it registers **Scale Sets** for an unlimited number of scopes simultaneously, polls the GitHub message queue using long-polling, and dynamically provisions perfectly isolated, ephemeral Docker containers the exact moment a job arrives.

## Features

- **Concurrent Multiplexing:** Register Scale Sets across multiple Repositories, Organizations, and Enterprise instances concurrently from a single lightweight container.
- **True Ephemeral Workers:** Every job executes inside a brand new, isolated Docker container that is immediately destroyed upon completion.
- **Zero-Latency Warm Pool:** Pre-boots warm worker containers so jobs start instantly without waiting for container startup.
- **Docker as Source of Truth:** Container state is tracked via Docker labels and container names. On restart, the orchestrator recovers state from existing containers—no more orphaned workers.
- **Native Autoscaling & Capacity Management:** Enforces a strict maximum worker limit derived from container state. When capacity is full, allocation blocks cleanly until a slot frees up.
- **Simple Authentication:** Authenticate using Personal Access Tokens (PATs)—one per runner scope.

## Architecture

The project uses the official `actions/scaleset` Go library as the control plane, paired with a Docker-based warm pool for instant job execution:

1. **The Multiplexer**: Initializes `actions/scaleset` clients for each configured runner. Each client opens a long-polling message session with GitHub.
2. **The Orchestrator**: Maintains a warm pool of pre-booted Docker containers. Uses Docker as the source of truth—container state is tracked via labels (`github-mux.managed`, `github-mux.runner`) and container names (`github-mux-warm-*`, `github-mux-active-<runner>-*`). On restart, existing containers are discovered and recovered. A single Docker Events stream monitors all container lifecycle events.
3. **The Worker Launcher**: Inside each ephemeral container, a lightweight HTTP server (`worker-launcher`) listens on port `9001`. The Multiplexer generates a JIT (Just-In-Time) runner config token from GitHub and POSTs it to the container. The launcher then executes the official `Runner.Listener` binary in ephemeral mode, which authenticates, runs the job, and exits cleanly.

## Getting Started

### 1. Configure

Copy the sample configuration file and add your repositories/organizations:
```bash
cp config.sample.yaml config.yaml
```

Edit `config.yaml` to specify:
- **`name`**: A unique identifier for the runner scope
- **`url`**: The GitHub repository, organization, or enterprise URL
- **`pat`**: A Personal Access Token with runner management permissions
- **`scale_set_name`**: The name of the Scale Set (also used as the workflow `runs-on` label)
- **`labels`** *(optional)*: Additional labels for workflow targeting
- **`group`** *(optional)*: Runner group name (defaults to `"Default"`)

### 2. Deploy

Run the orchestrator using Docker Compose:
```bash
docker compose up -d --build
```
> **Note:** The orchestrator mounts `/var/run/docker.sock` to dynamically spawn ephemeral worker containers alongside it.

### 3. Target from Workflows

Use your Scale Set name as the `runs-on` label in your GitHub Actions workflows:
```yaml
jobs:
  build:
    runs-on: my-scale-set
    steps:
      - uses: actions/checkout@v4
      - run: echo "Running on a multiplexed ephemeral runner!"
```

### 4. Docker-in-Docker (Optional)

To allow workflows to run Docker commands natively inside the ephemeral workers, add the following environment variable to the service in your `docker-compose.yml`:
```yaml
environment:
  - WORKER_START_DOCKER_SERVICE=true
```

## Configuration Reference

| Field | Required | Description |
|-------|----------|-------------|
| `max_workers` | No | Maximum concurrent worker containers (default: `5`) |
| `warm_workers` | No | Number of pre-booted warm containers (default: `0`) |
| `runners[].name` | Yes | Unique identifier for the runner scope |
| `runners[].url` | Yes | GitHub repository, org, or enterprise URL |
| `runners[].pat` | Yes | Personal Access Token |
| `runners[].scale_set_name` | Yes | Scale Set name (used as workflow `runs-on` label) |
| `runners[].max_runners` | No | Limit the max concurrent jobs for this specific listener. Defaults to global `max_workers`. |
| `runners[].labels` | No | Additional labels for workflow targeting |
| `runners[].group` | No | Runner group name (default: `"Default"`) |

## Contributing

Please see the architectural logic and integration constraints detailed in the repository if you intend to upgrade the base `actions/runner` dependency. Strict adherence to the [Integration Maintenance Guide](.agents/skills/actions-runner-integration/SKILL.md) is required.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
