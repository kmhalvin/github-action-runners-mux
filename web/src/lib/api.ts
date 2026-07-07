export const API_BASE = '/api/v1';

export interface RunnerStatus {
	name: string;
	mode: string;
	status: string;
	url: string;
	jobs_completed: number;
	group?: string;
}

export interface GlobalStatus {
	total_runners: number;
	active_workers: number;
	warm_workers: number;
	max_workers: number;
	uptime: string;
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
	async addRunner(data: any): Promise<void> {
		const res = await fetch(`${API_BASE}/runners`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(data),
		});
		if (!res.ok) {
			const text = await res.text();
			throw new Error(text || 'Failed to add runner');
		}
	},
	async removeRunner(name: string, force: boolean = false): Promise<void> {
		const res = await fetch(`${API_BASE}/runners/${name}?force=${force}`, {
			method: 'DELETE',
		});
		if (!res.ok) {
			const text = await res.text();
			throw new Error(text || 'Failed to remove runner');
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
			const text = await res.text();
			throw new Error(text || 'Failed to update settings');
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
			const text = await res.text();
			throw new Error(text || 'Failed to add domain');
		}
	},
	async removeDomain(id: number): Promise<void> {
		const res = await fetch(`${API_BASE}/settings/domains/${id}`, {
			method: 'DELETE',
		});
		if (!res.ok) {
			const text = await res.text();
			throw new Error(text || 'Failed to remove domain');
		}
	}
};
