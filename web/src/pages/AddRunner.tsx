import { useState, useEffect } from "react";
import { useNavigate } from "react-router";
import useSWR from "swr";
import { Save, AlertTriangle, RefreshCw } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";

export default function AddRunner() {
	const navigate = useNavigate();
	const { data: domains } = useSWR("domains", api.getDomains);
	const [mode, setMode] = useState<string>("standalone");
	const [url, setUrl] = useState("");
	const [token, setToken] = useState("");
	const [pat, setPat] = useState("");
	const [scaleSetName, setScaleSetName] = useState("");
	const [name, setName] = useState("");
	const [labels, setLabels] = useState("");
	const [group, setGroup] = useState("");
	const [maxRunners, setMaxRunners] = useState(0);
	const [loading, setLoading] = useState(false);

	// Scope detection
	const [scope, setScope] = useState<string | null>(null);

	useEffect(() => {
		if (!url) {
			setScope(null);
			return;
		}
		
		try {
			const urlObj = new URL(url);
			const pathParts = urlObj.pathname.split('/').filter(Boolean);
			
			// Check if enterprise domain
			const isEnterprise = domains?.some(d => urlObj.hostname === d.domain) || urlObj.hostname !== 'github.com';
			
			if (isEnterprise && pathParts[0] === 'enterprises') {
				setScope("Enterprise");
			} else if (pathParts.length === 1) {
				setScope("Organization");
			} else if (pathParts.length >= 2) {
				setScope("Repository");
			} else {
				setScope("Unknown");
			}
		} catch (e) {
			setScope(null);
		}
	}, [url, domains]);

	const handleSubmit = async (e: React.FormEvent) => {
		e.preventDefault();
		setLoading(true);
		try {
			await api.addRunner({
				name: name || `runner-${Math.random().toString(36).substring(7)}`,
				mode,
				url,
				token: mode === "standalone" ? token : undefined,
				pat: mode === "scaleset" ? pat : undefined,
				scale_set_name: mode === "scaleset" ? scaleSetName : undefined,
				max_runners: maxRunners > 0 ? maxRunners : undefined,
				labels: labels ? labels.split(",").map(l => l.trim()) : undefined,
				group: group || undefined,
				dir: mode === "standalone" ? `/opt/runners/${name || 'temp'}` : undefined,
			});
			toast.success("Runner added successfully");
			navigate("/");
		} catch (err: any) {
			toast.error(err.message);
		} finally {
			setLoading(false);
		}
	};

	return (
		<div className="max-w-2xl mx-auto flex flex-col gap-8">
			<div>
				<h1 className="text-3xl font-bold tracking-tight">Add Runner</h1>
				<p className="text-muted-foreground">Configure a new GitHub Action runner.</p>
			</div>

			<Card>
				<CardHeader>
					<CardTitle>Configuration</CardTitle>
					<CardDescription>Select mode and provide connection details.</CardDescription>
				</CardHeader>
				<CardContent>
					<form onSubmit={handleSubmit} className="flex flex-col gap-6">
						
						{/* Mode Toggle */}
						<div className="flex flex-col gap-3">
							<Label>Architecture Mode</Label>
							<ToggleGroup 
								value={[mode]} 
								onValueChange={(val: string[]) => val.length > 0 && setMode(val[0])}
								className="justify-start"
							>
								<ToggleGroupItem value="standalone" className="w-32">Standalone</ToggleGroupItem>
								<ToggleGroupItem value="scaleset" className="w-32">Scale Set</ToggleGroupItem>
							</ToggleGroup>
							<p className="text-sm text-muted-foreground">
								{mode === "standalone" 
									? "Uses short-lived registration tokens. Best for isolation and strict compliance."
									: "Uses long-lived PATs. Multiplexes jobs dynamically across a pool of containers."}
							</p>
						</div>

						{/* Common Fields */}
						<div className="flex flex-col gap-3">
							<div className="flex justify-between">
								<Label htmlFor="url">Repository, Organization, or Enterprise URL</Label>
								{scope && <Badge variant="secondary" className="h-5">{scope}</Badge>}
							</div>
							<Input 
								id="url" 
								type="url" 
								placeholder="https://github.com/my-org/my-repo" 
								value={url}
								onChange={(e) => setUrl(e.target.value)}
								required
							/>
						</div>

						<div className="flex flex-col gap-3">
							<Label htmlFor="name">Runner Name (Optional)</Label>
							<Input 
								id="name" 
								placeholder="Auto-generated if left blank" 
								value={name}
								onChange={(e) => setName(e.target.value)}
							/>
						</div>

						{/* Mode-Specific Fields */}
						{mode === "standalone" ? (
							<div className="flex flex-col gap-3 p-4 bg-secondary/30 rounded-lg border">
								<div className="flex justify-between items-center">
									<Label htmlFor="token">Registration Token</Label>
									<Badge variant="destructive" className="h-5 gap-1">
										<AlertTriangle className="size-3" />
										1-hour expiry
									</Badge>
								</div>
								<Input 
									id="token" 
									type="password"
									value={token}
									onChange={(e) => setToken(e.target.value)}
									required
								/>
								<p className="text-xs text-muted-foreground">
									Obtain this from GitHub Settings &gt; Actions &gt; Runners.
								</p>
							</div>
						) : (
							<div className="flex flex-col gap-4 p-4 bg-secondary/30 rounded-lg border">
								<div className="flex flex-col gap-3">
									<Label htmlFor="pat">Personal Access Token (PAT)</Label>
									<Input 
										id="pat" 
										type="password"
										value={pat}
										onChange={(e) => setPat(e.target.value)}
										required
									/>
								</div>
								<div className="flex flex-col gap-3">
									<Label htmlFor="scaleSetName">Scale Set Name</Label>
									<Input 
										id="scaleSetName" 
										placeholder="my-scale-set"
										value={scaleSetName}
										onChange={(e) => setScaleSetName(e.target.value)}
										required
									/>
								</div>
								<div className="flex flex-col gap-3">
									<Label htmlFor="maxRunners">Max Runners for this Scale Set</Label>
									<Input 
										id="maxRunners" 
										type="number"
										min="0"
										placeholder="0 for unlimited (uses global limit)"
										value={maxRunners || ''}
										onChange={(e) => setMaxRunners(parseInt(e.target.value) || 0)}
									/>
								</div>
							</div>
						)}

						{/* Optional Metadata */}
						<div className="grid grid-cols-2 gap-4">
							<div className="flex flex-col gap-3">
								<Label htmlFor="labels">Labels</Label>
								<Input 
									id="labels" 
									placeholder="ubuntu-latest, gpu, x64"
									value={labels}
									onChange={(e) => setLabels(e.target.value)}
								/>
							</div>
							<div className="flex flex-col gap-3">
								<Label htmlFor="group">Runner Group</Label>
								<Input 
									id="group" 
									placeholder="Default"
									value={group}
									onChange={(e) => setGroup(e.target.value)}
								/>
							</div>
						</div>

						<Button type="submit" disabled={loading} className="w-full mt-4">
							{loading ? (
								<>
									<RefreshCw className="mr-2 size-4 animate-spin" />
									Starting Runner...
								</>
							) : (
								<>
									<Save className="mr-2 size-4" />
									Save and Start Runner
								</>
							)}
						</Button>
					</form>
				</CardContent>
			</Card>
		</div>
	);
}
