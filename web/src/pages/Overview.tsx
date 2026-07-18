import { useState, useMemo } from "react"
import useSWR from "swr"
import { Link } from "react-router"
import { PlusIcon, Server, Play, Cpu, Activity, Clock, Pencil, Search } from "lucide-react"

import { api } from "@/lib/api"
import type { RunnerStatus, GlobalStatus, AuthStatus } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle, CardFooter } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

type GroupBy = "none" | "host" | "org" | "repo"

/** Returns true if the URL points to a repo (2 path segments), false for org/user-level (1 segment). */
function isRepoLevel(url: string): boolean {
  try {
    const pathParts = new URL(url).pathname.split("/").filter(Boolean)
    return pathParts.length >= 2
  } catch {
    return false
  }
}

/** Extract a grouping key from a runner's URL. */
function getGroupKey(url: string, groupBy: GroupBy): string {
  if (groupBy === "none") return "All Runners"
  try {
    const urlObj = new URL(url)
    if (groupBy === "host") return urlObj.host
    const pathParts = urlObj.pathname.split("/").filter(Boolean)
    if (groupBy === "org") return pathParts[0] || "Unknown"
    if (groupBy === "repo") {
      // Full URL as the repo key (a repo can have up to 2 runners: standalone + scaleset)
      return url
    }
  } catch {
    return "Unknown"
  }
  return "Unknown"
}

export default function Overview() {
  const { data: status } = useSWR<GlobalStatus>("status", api.getStatus, { refreshInterval: 5000 })
  const { data: runners } = useSWR<RunnerStatus[]>("runners", api.getRunners, { refreshInterval: 5000 })
  const { data: authStatus } = useSWR<AuthStatus[]>("auth-status", api.getAuthStatus, { refreshInterval: 30000 })
  const canEdit = !authStatus || authStatus.length === 0 || authStatus.some((s) => s.logged_in)

  const [searchQuery, setSearchQuery] = useState("")
  const [groupBy, setGroupBy] = useState<GroupBy>("none")

  // Filter runners by URL substring (case-insensitive)
  // When grouping by "repo", exclude org/user-level runners (1 path segment)
  const filteredRunners = useMemo(() => {
    if (!runners) return []
    let result = runners
    if (groupBy === "repo") {
      result = result.filter((r) => isRepoLevel(r.url))
    }
    if (!searchQuery.trim()) return result
    const q = searchQuery.toLowerCase()
    return result.filter((r) => r.url.toLowerCase().includes(q))
  }, [runners, searchQuery, groupBy])

  // Group filtered runners by the selected key
  const groupedRunners = useMemo(() => {
    const groups = new Map<string, RunnerStatus[]>()
    for (const runner of filteredRunners) {
      const key = getGroupKey(runner.url, groupBy)
      if (!groups.has(key)) groups.set(key, [])
      groups.get(key)!.push(runner)
    }
    // Sort keys alphabetically for stable ordering
    return Array.from(groups.entries()).sort((a, b) => a[0].localeCompare(b[0]))
  }, [filteredRunners, groupBy])

  /** Render a runner card. */
  const renderRunnerCard = (runner: RunnerStatus) => (
    <Card key={runner.name} className="flex flex-col">
      <CardHeader className="pb-2">
        <div className="flex justify-between items-start gap-2 min-w-0">
          <CardTitle className="text-lg truncate min-w-0" title={runner.name}>
            {runner.name}
          </CardTitle>
          <Badge variant={
            runner.state === 'Online' ? 'default' :
            runner.state === 'Busy' ? 'secondary' :
            runner.state === 'Offline' || runner.state === 'Failed' ? 'destructive' : 'outline'
          } className="shrink-0">
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
        {runner.state === 'Failed' && runner.error && (
          <p className="mt-2 text-xs text-destructive line-clamp-2" title={runner.error}>
            {runner.error}
          </p>
        )}
      </CardContent>
      <CardFooter className="pt-2 flex justify-end gap-1">
        {canEdit && (
          <Link to={`/runner/${runner.name}`}>
            <Button variant="ghost" size="icon" aria-label="View runner details">
              <Pencil className="size-4" />
            </Button>
          </Link>
        )}
      </CardFooter>
    </Card>
  )

  /** Render the card grid (either flat or grouped). */
  const renderRunnerGrid = () => {
    if (!runners) {
      return (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Card key={i}>
              <CardHeader>
                <Skeleton className="h-5 w-3/4" />
                <Skeleton className="h-4 w-1/2" />
              </CardHeader>
              <CardContent>
                <Skeleton className="h-20 w-full" />
              </CardContent>
            </Card>
          ))}
        </div>
      )
    }

    if (runners.length === 0) {
      return (
        <div className="py-12 text-center text-muted-foreground border rounded-lg border-dashed">
          No runners configured. Click "Add Runner" to get started.
        </div>
      )
    }

    if (filteredRunners.length === 0) {
      // Distinguish between "By Repo" filtering out all org/user-level runners
      // vs. a search query with no matches.
      const isRepoFilterEmpty =
        groupBy === "repo" &&
        runners.length > 0 &&
        runners.every((r) => !isRepoLevel(r.url))

      return (
        <div className="py-12 text-center text-muted-foreground border rounded-lg border-dashed">
          {isRepoFilterEmpty
            ? "No repo-level runners found. Org/user-level runners are hidden in this view."
            : `No runners match "${searchQuery}".`}
        </div>
      )
    }

    if (groupBy === "none") {
      return (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {filteredRunners.map(renderRunnerCard)}
        </div>
      )
    }

    // Grouped view
    return (
      <div className="flex flex-col gap-6">
        {groupedRunners.map(([groupKey, groupRunners]) => (
          <div key={groupKey}>
            <div className="flex items-center gap-2 mb-3">
              <h3 className="text-sm font-semibold text-muted-foreground">
                {groupKey}
              </h3>
              <Badge variant="secondary" className="text-xs">
                {groupRunners.length}
              </Badge>
            </div>
            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
              {groupRunners.map(renderRunnerCard)}
            </div>
          </div>
        ))}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-8 max-w-6xl mx-auto">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Overview</h1>
          <p className="text-muted-foreground">Monitor and manage your GitHub Action runners.</p>
        </div>
        {canEdit && (
          <Link to="/add">
            <Button>
              <PlusIcon data-icon="inline-start" />
              Add Runner
            </Button>
          </Link>
        )}
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

      {/* Runners List with Filter + Group Toggle */}
      <div>
        <div className="flex flex-col gap-4 mb-4">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
            <h2 className="text-xl font-semibold">Runners</h2>
            <div className="flex flex-col sm:flex-row gap-2 sm:items-center">
              {/* Search by URL */}
              <div className="relative">
                <Search className="absolute left-2 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
                <Input
                  placeholder="Search by URL…"
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="pl-8 sm:w-64"
                />
              </div>
              {/* Group Toggle */}
              <ToggleGroup
                value={[groupBy]}
                onValueChange={(val: string[]) =>
                  val.length > 0 && setGroupBy(val[0] as GroupBy)
                }
                className="justify-start"
                aria-label="Group by"
              >
                <ToggleGroupItem value="none" className="text-xs">
                  None
                </ToggleGroupItem>
                <ToggleGroupItem value="host" className="text-xs">
                  Host
                </ToggleGroupItem>
                <ToggleGroupItem value="org" className="text-xs">
                  Org
                </ToggleGroupItem>
                <ToggleGroupItem value="repo" className="text-xs">
                  Repo
                </ToggleGroupItem>
              </ToggleGroup>
            </div>
          </div>
        </div>

        {renderRunnerGrid()}
      </div>
    </div>
  )
}
