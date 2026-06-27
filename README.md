# GitHub Action Runners Multiplexer

> An ephemeral, Docker-based GitHub Actions runner orchestrator designed to concurrently multiplex jobs across multiple repositories, organizations, and GitHub Enterprise hosts—all managed via simple registration tokens.

The official GitHub Actions runner forces you to run a 1:1 mapping of runner processes to repositories or organizations. This orchestrator changes the paradigm: it acts as a central proxy that listens to an infinite number of scopes simultaneously, and dynamically provisions perfectly isolated, ephemeral Docker containers the exact moment a job arrives.

## Features

- **Concurrent Multiplexing:** Listen to multiple Repositories, Organizations, and Enterprise instances concurrently from a single lightweight proxy container.
- **True Ephemeral Workers:** Every job executes inside a brand new, isolated Docker container that is immediately destroyed upon completion.
- **Native Autoscaling & Capacity Management:** Enforces a strict maximum worker limit using in-memory counting semaphores. When capacity is hit, the proxy instantly freezes all idle listeners to prevent aggressive job queuing and timeouts.
- **Simple Authentication:** No need to manage complex GitHub App credentials; authenticate easily using standard GitHub Registration Tokens or PATs.

## Architecture

The project intercepts the internal execution flow of the official GitHub `actions/runner` by separating the Listener from the Worker:
1. **The Orchestrator (Proxy)**: A single Go process that boots and manages multiple lightweight `Runner.Listener` processes.
2. **The Worker Shim**: When a job arrives, the Orchestrator spawns an ephemeral Docker container injecting a `worker-shim`. This shim creates anonymous pipes, launches the real `Runner.Worker` payload, and proxies the execution stream back to the Listener over a local TCP socket.

## Getting Started

### 1. Configure
Copy the sample configuration file and add your repositories/organizations:
```bash
cp config.sample.yaml config.yaml
```

Edit `config.yaml` to specify the scopes you want to listen to and provide their registration tokens.

### 2. Deploy
Run the Orchestrator proxy using Docker Compose:
```bash
docker-compose up -d --build
```
*Note: The proxy mounts `/var/run/docker.sock` to dynamically spawn the ephemeral worker containers alongside it.*

### 3. Docker-in-Docker (Optional)
To allow workflows to run Docker commands natively inside the ephemeral workers, add the following environment variable to the `multi-runner-proxy` service in your `docker-compose.yml`:
```yaml
environment:
  - WORKER_START_DOCKER_SERVICE=true
```

## Contributing
Please see the architectural logic and integration constraints detailed in the repository if you intend to upgrade the base `actions/runner` dependency. Because this orchestrator intercepts specific binary inputs (`spawnclient`) and relies on base image entrypoint overrides, strict adherence to the [Integration Maintenance Guide](.agents/skills/actions-runner-integration/SKILL.md) is required.

## License
This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
