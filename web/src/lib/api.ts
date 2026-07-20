export const API_BASE = "/api/v1"

export interface RunnerStatus {
  id: number
  name: string
  mode: string
  url: string
  dir: string
  has_pat: boolean
  max_runners: number
  labels: string
  runner_group: string
  jobs_completed: number
  created_at: string
  state: string
  active_workers: number
  error?: string
}

export interface GlobalStatus {
  max_workers: number
  warm_workers: number
  warm_pool_size: number
  active_workers: number
  booting_count: number
  is_paused: boolean
}

export interface AddRunnerPayload {
  name: string
  mode: string
  url: string
  pat?: string
  max_runners?: number
  labels?: string[]
	runner_group?: string
}

export interface UpdateRunnerPayload {
  pat?: string
  max_runners?: number
  labels?: string[]
  runner_group?: string
}

export interface RunnerDetail {
  id: number
  name: string
  mode: string
  url: string
  dir: string
  has_pat: boolean
  max_runners: number
  labels: string
  runner_group: string
  jobs_completed: number
  created_at: string
  state: string
  active_workers: number
  error?: string
  can_manage: boolean
  is_registered: boolean
}

export interface RunnerErrorResponse {
  error: string
  runner_name?: string
}

export interface Settings {
  max_workers: number
  warm_workers: number
}

// --- Auth types ---

export interface AuthHost {
  host: string
  client_id: string
}

export interface AuthStatus {
  host: string
  logged_in: boolean
  is_admin: boolean
}

export interface AdminRepo {
  host: string
  owner: string
  repo: string // empty for org-level
  url: string
  type: string // "organization" or "repository"
}

export const api = {
  // --- Runner endpoints ---

  async getRunners(): Promise<RunnerStatus[]> {
    const res = await fetch(`${API_BASE}/runners`)
    if (!res.ok) throw new Error("Failed to fetch runners")
    const data = await res.json()
    return data ?? []
  },
  async addRunner(data: AddRunnerPayload): Promise<void> {
    const res = await fetch(`${API_BASE}/runners`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      const err = new Error(body.error || "Failed to add runner") as Error & {
        runnerName?: string
      }
      err.runnerName = body.runner_name
      throw err
    }
  },
  async getRunner(name: string): Promise<RunnerDetail> {
    const res = await fetch(`${API_BASE}/runners/${name}`)
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || "Failed to fetch runner")
    }
    return res.json()
  },
  async updateRunner(name: string, data: UpdateRunnerPayload): Promise<void> {
    const res = await fetch(`${API_BASE}/runners/${name}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      const err = new Error(
        body.error || "Failed to update runner"
      ) as Error & { runnerName?: string }
      err.runnerName = body.runner_name
      throw err
    }
  },
  async removeRunner(name: string, force: boolean = false): Promise<void> {
    const params = new URLSearchParams({ force: String(force) })
    const res = await fetch(`${API_BASE}/runners/${name}?${params}`, {
      method: "DELETE",
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || "Failed to remove runner")
    }
  },

  // --- Status & Settings ---

  async getStatus(): Promise<GlobalStatus> {
    const res = await fetch(`${API_BASE}/status`)
    if (!res.ok) throw new Error("Failed to fetch status")
    return res.json()
  },
  async getSettings(): Promise<Settings> {
    const res = await fetch(`${API_BASE}/settings`)
    if (!res.ok) throw new Error("Failed to fetch settings")
    return res.json()
  },
  async updateSettings(settings: Settings): Promise<void> {
    const res = await fetch(`${API_BASE}/settings`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || "Failed to update settings")
    }
  },

  // --- Auth endpoints ---

  async getAuthHosts(): Promise<AuthHost[]> {
    const res = await fetch(`${API_BASE}/auth/hosts`)
    if (!res.ok) throw new Error("Failed to fetch auth hosts")
    return res.json()
  },
  async exchangeCode(code: string, host: string): Promise<{ access_token: string; host: string }> {
    const res = await fetch(`${API_BASE}/auth/token`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code, host }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || "Failed to exchange code")
    }
    return res.json()
  },
  async getAuthStatus(): Promise<AuthStatus[]> {
    const res = await fetch(`${API_BASE}/auth/status`)
    if (!res.ok) throw new Error("Failed to fetch auth status")
    return res.json()
  },
  async logout(host?: string): Promise<void> {
    const url = host
      ? `${API_BASE}/auth/logout?host=${encodeURIComponent(host)}`
      : `${API_BASE}/auth/logout`
    await fetch(url, { method: "POST" })
  },
  async getGitHubRepos(): Promise<AdminRepo[]> {
    const res = await fetch(`${API_BASE}/github/repos`)
    if (!res.ok) throw new Error("Failed to fetch repos")
    return res.json()
  },
}
