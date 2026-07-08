import useSWR from "swr";
import { Link } from "react-router";
import { PlusIcon, Server, Play, Trash2, Cpu, Activity, Clock } from "lucide-react";
import { api, API_BASE } from "@/lib/api";
import type { RunnerStatus, GlobalStatus } from "@/lib/api";
import { useEffect, useCallback } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle, CardFooter } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { toast } from "sonner";

export default function Overview() {
	const { data: status, mutate: mutateStatus } = useSWR<GlobalStatus>("status", api.getStatus, { refreshInterval: 5000 });
	const { data: runners, mutate: mutateRunners } = useSWR<RunnerStatus[]>("runners", api.getRunners, { refreshInterval: 5000 });

	useEffect(() => {
		const sse = new EventSource(`${API_BASE}/events`);
		sse.onmessage = () => {
			mutateStatus();
			mutateRunners();
		};
		return () => sse.close();
	}, [mutateStatus, mutateRunners]);

	const removeRunner = useCallback(async (name: string, force: boolean) => {
		try {
			await api.removeRunner(name, force);
			toast.success(`Runner ${name} removal requested`);
			mutateRunners();
		} catch (err: unknown) {
			toast.error(err instanceof Error ? err.message : 'Unknown error');
		}
	}, [mutateRunners]);

	return (
		<div className="flex flex-col gap-8 max-w-6xl mx-auto">
			<div className="flex items-center justify-between">
				<div>
					<h1 className="text-3xl font-bold tracking-tight">Overview</h1>
					<p className="text-muted-foreground">Monitor and manage your GitHub Action runners.</p>
				</div>
				<Link to="/add">
					<Button>
						<PlusIcon data-icon="inline-start" />
						Add Runner
					</Button>
				</Link>
			</div>

			{/* Stats Overview */}
			<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
				<Card>
					<CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
						<CardTitle className="text-sm font-medium">Total Runners</CardTitle>
						<Server className="size-4 text-muted-foreground" />
					</CardHeader>
					<CardContent>
						<div className="text-2xl font-bold">{runners ? runners.length : <Skeleton className="h-8 w-16" />}</div>
					</CardContent>
				</Card>
				<Card>
					<CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
						<CardTitle className="text-sm font-medium">Active Workers</CardTitle>
						<Activity className="size-4 text-muted-foreground" />
					</CardHeader>
					<CardContent>
						<div className="text-2xl font-bold">{status ? status.active_workers : <Skeleton className="h-8 w-16" />}</div>
					</CardContent>
				</Card>
				<Card>
					<CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
						<CardTitle className="text-sm font-medium">Warm Pool</CardTitle>
						<Play className="size-4 text-muted-foreground" />
					</CardHeader>
					<CardContent>
						<div className="text-2xl font-bold">{status ? status.warm_pool_size : <Skeleton className="h-8 w-16" />}</div>
					</CardContent>
				</Card>
				<Card>
					<CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
						<CardTitle className="text-sm font-medium">Capacity Used</CardTitle>
						<Cpu className="size-4 text-muted-foreground" />
					</CardHeader>
					<CardContent>
						<div className="text-2xl font-bold">
							{status ? (
								<>{Math.round((status.active_workers / (status.max_workers || 1)) * 100)}%</>
							) : (
								<Skeleton className="h-8 w-16" />
							)}
						</div>
						<Progress 
							value={status ? (status.active_workers / (status.max_workers || 1)) * 100 : 0} 
							className="mt-2"
						/>
					</CardContent>
				</Card>
			</div>

			{/* Runners List */}
			<div>
				<h2 className="text-xl font-semibold mb-4">Runners</h2>
				<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
					{!runners ? (
						Array.from({ length: 3 }).map((_, i) => (
							<Card key={i}>
								<CardHeader>
									<Skeleton className="h-5 w-3/4" />
									<Skeleton className="h-4 w-1/2" />
								</CardHeader>
								<CardContent>
									<Skeleton className="h-20 w-full" />
								</CardContent>
							</Card>
						))
					) : runners.length === 0 ? (
						<div className="col-span-full py-12 text-center text-muted-foreground border rounded-lg border-dashed">
							No runners configured. Click "Add Runner" to get started.
						</div>
					) : (
						runners.map((runner) => (
							<Card key={runner.name} className="flex flex-col">
								<CardHeader className="pb-2">
									<div className="flex justify-between items-start">
										<CardTitle className="text-lg truncate pr-2" title={runner.name}>
											{runner.name}
										</CardTitle>
										<Badge variant={
											runner.state === 'Online' ? 'default' : 
											runner.state === 'Busy' ? 'secondary' : 
											runner.state === 'Offline' ? 'destructive' : 'outline'
										}>
											{runner.state === 'Busy' && runner.mode === 'scaleset' 
												? `Busy (${runner.active_workers})`
												: runner.state}
										</Badge>
									</div>
									<CardDescription className="truncate" title={runner.url}>
										{runner.url}
									</CardDescription>
								</CardHeader>
								<CardContent className="flex-1 pb-2">
									<div className="flex flex-wrap gap-2 mb-4">
										<Badge variant="outline">{runner.mode === 'standalone' ? 'Standalone' : 'Scale Set'}</Badge>
										{runner.runner_group && <Badge variant="outline">Group: {runner.runner_group}</Badge>}
									</div>
									<div className="flex items-center gap-2 text-sm text-muted-foreground">
										<Clock className="size-4" />
										{runner.jobs_completed} jobs completed
									</div>
								</CardContent>
								<CardFooter className="pt-2 flex justify-end">
									<AlertDialog>
										<AlertDialogTrigger
											render={
												<Button variant="ghost" size="icon" className="text-destructive hover:bg-destructive/10" aria-label="Remove runner">
													<Trash2 className="size-4" />
												</Button>
											}
										/>
										<AlertDialogContent>
											<AlertDialogHeader>
												<AlertDialogTitle>Remove Runner?</AlertDialogTitle>
												<AlertDialogDescription>
													Are you sure you want to remove <strong>{runner.name}</strong>?
												</AlertDialogDescription>
											</AlertDialogHeader>
											<AlertDialogFooter>
												<AlertDialogCancel>Cancel</AlertDialogCancel>
												<AlertDialogAction onClick={() => removeRunner(runner.name, false)}>
													Drain & Remove
												</AlertDialogAction>
												<AlertDialogAction 
													className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
													onClick={() => removeRunner(runner.name, true)}
												>
													Force Remove
												</AlertDialogAction>
											</AlertDialogFooter>
										</AlertDialogContent>
									</AlertDialog>
								</CardFooter>
							</Card>
						))
					)}
				</div>
			</div>
		</div>
	);
}
