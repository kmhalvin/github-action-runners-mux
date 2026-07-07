import useSWR from "swr";
import { useState, useEffect } from "react";
import type { Settings, EnterpriseDomain } from "@/lib/api";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle, CardFooter } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Trash2, Plus, Save } from "lucide-react";
import { toast } from "sonner";
import { Skeleton } from "@/components/ui/skeleton";

export default function SettingsPage() {
	const { data: settings, mutate: mutateSettings } = useSWR<Settings>("settings", api.getSettings);
	const { data: domains, mutate: mutateDomains } = useSWR<EnterpriseDomain[]>("domains", api.getDomains);

	const [maxWorkers, setMaxWorkers] = useState<number>(0);
	const [warmWorkers, setWarmWorkers] = useState<number>(0);
	const [newDomain, setNewDomain] = useState("");
	const [saving, setSaving] = useState(false);

	useEffect(() => {
		if (settings) {
			setMaxWorkers(settings.max_workers);
			setWarmWorkers(settings.warm_workers);
		}
	}, [settings]);

	const saveSettings = async () => {
		setSaving(true);
		try {
			await api.updateSettings({
				max_workers: maxWorkers,
				warm_workers: warmWorkers
			});
			toast.success("Settings saved successfully");
			mutateSettings();
		} catch (err: any) {
			toast.error(err.message);
		} finally {
			setSaving(false);
		}
	};

	const addDomain = async (e: React.FormEvent) => {
		e.preventDefault();
		if (!newDomain.trim()) return;
		
		try {
			await api.addDomain(newDomain.trim());
			setNewDomain("");
			mutateDomains();
			toast.success("Domain added");
		} catch (err: any) {
			toast.error(err.message);
		}
	};

	const removeDomain = async (id: number) => {
		try {
			await api.removeDomain(id);
			mutateDomains();
			toast.success("Domain removed");
		} catch (err: any) {
			toast.error(err.message);
		}
	};

	return (
		<div className="max-w-4xl mx-auto flex flex-col gap-8">
			<div>
				<h1 className="text-3xl font-bold tracking-tight">Settings</h1>
				<p className="text-muted-foreground">Manage global capacity and enterprise domains.</p>
			</div>

			<Card>
				<CardHeader>
					<CardTitle>Container Capacity</CardTitle>
					<CardDescription>Configure the underlying worker container pool limits.</CardDescription>
				</CardHeader>
				<CardContent className="flex flex-col gap-6">
					{!settings ? (
						<div className="space-y-4">
							<Skeleton className="h-10 w-full" />
							<Skeleton className="h-10 w-full" />
						</div>
					) : (
						<>
							<div className="grid gap-3">
								<Label htmlFor="maxWorkers">Max Workers</Label>
								<Input 
									id="maxWorkers" 
									type="number" 
									min="1"
									value={maxWorkers} 
									onChange={(e) => setMaxWorkers(parseInt(e.target.value) || 1)}
								/>
								<p className="text-sm text-muted-foreground">
									Maximum number of concurrent Docker containers to run.
								</p>
							</div>
							
							<div className="grid gap-3">
								<Label htmlFor="warmWorkers">Warm Workers</Label>
								<Input 
									id="warmWorkers" 
									type="number" 
									min="0"
									max={maxWorkers}
									value={warmWorkers} 
									onChange={(e) => setWarmWorkers(parseInt(e.target.value) || 0)}
								/>
								<p className="text-sm text-muted-foreground">
									Number of idle containers to keep running for faster job startup.
								</p>
							</div>
						</>
					)}
				</CardContent>
				<CardFooter className="border-t px-6 py-4">
					<Button onClick={saveSettings} disabled={saving || !settings}>
						<Save className="mr-2 size-4" />
						Save Settings
					</Button>
				</CardFooter>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle>Enterprise Domains</CardTitle>
					<CardDescription>Register custom domains for GitHub Enterprise Server.</CardDescription>
				</CardHeader>
				<CardContent>
					<form onSubmit={addDomain} className="flex gap-4 mb-6">
						<div className="flex-1">
							<Input 
								placeholder="github.company.com" 
								value={newDomain}
								onChange={(e) => setNewDomain(e.target.value)}
							/>
						</div>
						<Button type="submit" variant="secondary">
							<Plus className="mr-2 size-4" />
							Add Domain
						</Button>
					</form>

					<div className="border rounded-md">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>Domain</TableHead>
									<TableHead className="w-[100px] text-right">Actions</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{!domains ? (
									<TableRow>
										<TableCell colSpan={2}><Skeleton className="h-6 w-full" /></TableCell>
									</TableRow>
								) : domains.length === 0 ? (
									<TableRow>
										<TableCell colSpan={2} className="text-center py-6 text-muted-foreground">
											No custom domains registered.
										</TableCell>
									</TableRow>
								) : (
									domains.map((domain) => (
										<TableRow key={domain.id}>
											<TableCell className="font-medium">{domain.domain}</TableCell>
											<TableCell className="text-right">
												<Button 
													variant="ghost" 
													size="icon" 
													onClick={() => removeDomain(domain.id)}
													disabled={domain.domain === 'github.com'}
													className="text-destructive hover:bg-destructive/10"
												>
													<Trash2 className="size-4" />
												</Button>
											</TableCell>
										</TableRow>
									))
								)}
							</TableBody>
						</Table>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
