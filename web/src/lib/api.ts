export const API_BASE = '/api/v1';

export interface RunnerStatus {
	id: number;
	name: string;
	mode: string;
	url: string;
	token?: string;
	dir: string;
	pat?: string;
	scale_set_name: string;
	max_runners: number;
	labels: string;
	runner_group: string;
	jobs_completed: number;
	created_at: string;
	state: string;
	active_workers: number;
	error?: string;
}

export interface GlobalStatus {
	max_workers: number;
	warm_workers: number;
	warm_pool_size: number;
	active_workers: number;
	booting_count: number;
	is_paused: boolean;
}

export interface AddRunnerPayload {
	name: string;
	mode: string;
	url: string;
	token?: string;
	pat?: string;
	scale_set_name?: string;
	max_runners?: number;
	labels?: string[];
	runner_group?: string;
	dir?: string;
}

export interface Settings {
	max_workers: number;
	warm_workers: number;
}

export interface EnterpriseDomain {
	id: number;
	domain: string;
}

export const api = {
	async getRunners(): Promise<RunnerStatus[]> {
		const res = await fetch(`${API_BASE}/runners`);
		if (!res.ok) throw new Error('Failed to fetch runners');
		return res.json();
	},
	async addRunner(data: AddRunnerPayload): Promise<void> {
		const res = await fetch(`${API_BASE}/runners`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(data),
		});
		if (!res.ok) {
			const body = await res.json().catch(() => ({}));
			throw new Error(body.error || 'Failed to add runner');
		}
	},
	async removeRunner(name: string, force: boolean = false): Promise<void> {
		const res = await fetch(`${API_BASE}/runners/${name}?force=${force}`, {
			method: 'DELETE',
		});
		if (!res.ok) {
			const body = await res.json().catch(() => ({}));
			throw new Error(body.error || 'Failed to remove runner');
		}
	},
	async getStatus(): Promise<GlobalStatus> {
		const res = await fetch(`${API_BASE}/status`);
		if (!res.ok) throw new Error('Failed to fetch status');
		return res.json();
	},
	async getSettings(): Promise<Settings> {
		const res = await fetch(`${API_BASE}/settings`);
		if (!res.ok) throw new Error('Failed to fetch settings');
		return res.json();
	},
	async updateSettings(settings: Settings): Promise<void> {
		const res = await fetch(`${API_BASE}/settings`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(settings),
		});
		if (!res.ok) {
			const body = await res.json().catch(() => ({}));
			throw new Error(body.error || 'Failed to update settings');
		}
	},
	async getDomains(): Promise<EnterpriseDomain[]> {
		const res = await fetch(`${API_BASE}/settings/domains`);
		if (!res.ok) throw new Error('Failed to fetch domains');
		return res.json();
	},
	async addDomain(domain: string): Promise<void> {
		const res = await fetch(`${API_BASE}/settings/domains`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ domain }),
		});
		if (!res.ok) {
			const body = await res.json().catch(() => ({}));
			throw new Error(body.error || 'Failed to add domain');
		}
	},
	async removeDomain(id: number): Promise<void> {
		const res = await fetch(`${API_BASE}/settings/domains/${id}`, {
			method: 'DELETE',
		});
		if (!res.ok) {
			const body = await res.json().catch(() => ({}));
			throw new Error(body.error || 'Failed to remove domain');
		}
	}
};
