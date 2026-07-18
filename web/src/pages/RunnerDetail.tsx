import { useState, useMemo } from "react"
import { useNavigate, useParams, Link } from "react-router"
import { ArrowLeft, RefreshCw, Trash2, Clock, Activity, Server } from "lucide-react"
import { api } from "@/lib/api"
import type { RunnerDetail as RunnerDetailData } from "@/lib/api"
import useSWR from "swr"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { PatGuideDialog } from "@/components/pat-guide-dialog"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { toast } from "sonner"

const LIVE_STATES = ["Online", "Busy", "Registering", "Paused", "Draining"]

/** ConfigForm manages its own state, initialized from runner data.
 *  The `key` prop ensures it re-mounts (and re-initializes) when the runner name changes. */
function ConfigForm({
  runner,
  scope,
  onUpdated,
}: {
  runner: RunnerDetailData
  scope: string | null
  onUpdated: () => void
}) {
  const mode = runner.mode
  const isLive = LIVE_STATES.includes(runner.state)
  // For standalone runners that are already registered (have .credentials),
  // the form is read-only — only a retry is possible, no edits or token needed.
  const isStandaloneRegistered = mode === "standalone" && runner.is_registered
  const isReadOnly = isLive || isStandaloneRegistered

  const url = runner.url
  const [pat, setPat] = useState("")
  const [group, setGroup] = useState(runner.runner_group || "")
  const [maxRunners, setMaxRunners] = useState(runner.max_runners || 0)
  const [loading, setLoading] = useState(false)
  const [patType, setPatType] = useState<"classic" | "fine-grained">("classic")
  const [patGuideOwner, setPatGuideOwner] = useState<{
    host: string
    owner: string
    scopeType?: string
  } | null>(null)

  const { host, owner, scopeType } = useMemo(() => {
    if (!url) return { host: "", owner: "", scopeType: "" }
    try {
      const urlObj = new URL(url)
      const pathParts = urlObj.pathname.split("/").filter(Boolean)
      const h = urlObj.host
      if (pathParts.length === 1)
        return { host: h, owner: pathParts[0], scopeType: "organization" }
      if (pathParts.length >= 2)
        return { host: h, owner: pathParts[0], scopeType: "repository" }
      return { host: h, owner: "", scopeType: "" }
    } catch {
      return { host: "", owner: "", scopeType: "" }
    }
  }, [url])

  // Scope types for the PAT guide dialog — single runner, so just its scope type
  const patGuideScopeTypes = useMemo(() => {
    if (!patGuideOwner || !scopeType) return new Set<string>()
    return new Set<string>([scopeType])
  }, [patGuideOwner, scopeType])

  const handleSubmit = async (e: React.SubmitEvent<HTMLFormElement>) => {
    e.preventDefault()
    setLoading(true)

    try {
      await api.updateRunner(runner.name, {
        pat: mode === "scaleset" && pat ? pat : undefined,
        max_runners: mode === "scaleset" && maxRunners > 0 ? maxRunners : undefined,
        // Runner group only for org-scope runners
        runner_group: scope === "Organization" ? (group || undefined) : undefined,
      })

      toast.success(isStandaloneRegistered ? "Runner retry requested" : "Runner updated and registration retried")
      onUpdated()
    } catch (err: unknown) {
      const error = err as Error & { runnerName?: string }
      toast.error(error.message || "Unknown error")
    } finally {
      setLoading(false)
    }
  }

  return (
    <>
    <Card>
      <CardHeader>
        <CardTitle>Configuration</CardTitle>
        <CardDescription>
          {isLive
            ? "Runner is currently active. Fields are read-only."
            : isStandaloneRegistered
              ? "Runner is already registered. Click retry to restart the listener."
              : "Update configuration and retry registration."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="flex flex-col gap-6">
          {/* Runner Name */}
          <div className="flex flex-col gap-3">
            <Label htmlFor="name">Runner Name</Label>
            <Input
              id="name"
              value={runner.name}
              disabled
            />
            <p className="text-xs text-muted-foreground">
              Runner name cannot be changed after creation.
            </p>
          </div>

          {/* Max Runners — scaleset only, placed here for simplicity */}
          {mode === "scaleset" && (
            <div className="flex flex-col gap-3">
              <Label htmlFor="maxRunners">Max Runners</Label>
              <Input
                id="maxRunners"
                type="number"
                min="0"
                placeholder="0 for unlimited (uses global limit)"
                value={maxRunners || ""}
                onChange={(e) =>
                  setMaxRunners(parseInt(e.target.value) || 0)
                }
                disabled={isReadOnly}
              />
            </div>
          )}

          {/* Mode-Specific Fields */}
          {mode === "standalone" ? (
            <div className="flex flex-col gap-3 rounded-lg border bg-secondary/30 p-4">
              <p className="text-sm text-muted-foreground">
                {isStandaloneRegistered
                  ? "Runner is already registered. No configuration changes needed to retry."
                  : "Registration token is automatically generated using your OAuth session. No manual token entry required."}
              </p>
            </div>
          ) : (
            <div className="flex flex-col gap-4 rounded-lg border bg-secondary/30 p-4">
              {/* PAT Type Toggle: Classic vs Fine-grained */}
              <div className="flex flex-col gap-2">
                <Label>PAT Type</Label>
                <p className="text-xs text-muted-foreground">
                  PAT type depends on your organization's token policy — some organizations disable classic tokens, while others may not support fine-grained tokens.
                </p>
                <ToggleGroup
                  value={[patType]}
                  onValueChange={(val: string[]) =>
                    val.length > 0 && setPatType(val[0] as "classic" | "fine-grained")
                  }
                  className="justify-start"
                  aria-label="PAT Type"
                >
                  <ToggleGroupItem value="classic" className="w-28">
                    Classic
                  </ToggleGroupItem>
                  <ToggleGroupItem value="fine-grained" className="w-28">
                    Fine-grained
                  </ToggleGroupItem>
                </ToggleGroup>
                <p className="text-xs text-muted-foreground">
                  {patType === "classic"
                    ? "Classic tokens are not scoped per resource owner — one token works for all orgs/repos on a host. Separate tokens per scope type (org/repo) are supported. Some organizations may disable classic tokens or require fine-grained tokens depending on their security policy."
                    : "Fine-grained tokens are scoped per resource owner. One token is required for each organization or user account."}
                </p>
              </div>

              <div className="flex flex-col gap-3">
                <div className="flex items-center justify-between">
                  <Label htmlFor="pat">Personal Access Token (PAT)</Label>
                  <Button
                    type="button"
                    variant="link"
                    size="sm"
                    className="h-7 p-0 text-xs"
                    onClick={() =>
                      setPatGuideOwner({
                        host,
                        owner: patType === "classic" ? "" : owner,
                        scopeType,
                      })
                    }
                  >
                    How to create a PAT
                  </Button>
                </div>
                {runner.has_pat && (
                  <p className="text-xs text-muted-foreground">
                    PAT is configured. Enter a new token only to replace it.
                  </p>
                )}
                <Input
                  id="pat"
                  type="password"
                  placeholder={
                    runner.has_pat
                      ? "••••••••••••"
                      : patType === "classic"
                        ? "ghp_..."
                        : "github_pat_..."
                  }
                  value={pat}
                  onChange={(e) => setPat(e.target.value)}
                  required={!runner.has_pat}
                  disabled={isReadOnly}
                />
              </div>
            </div>
          )}

          {/* Runner Group — only available for organization-level runners */}
          {scope === "Organization" ? (
            <div className="flex flex-col gap-3">
              <Label htmlFor="group">Runner Group</Label>
              <Input
                id="group"
                placeholder="default"
                value={group}
                onChange={(e) => setGroup(e.target.value)}
                disabled={isReadOnly}
              />
              <p className="text-xs text-muted-foreground">
                Applied only to organization-level runners. Repository-level runners use the default group.
              </p>
            </div>
          ) : scope === "Repository" ? (
            <p className="text-sm text-muted-foreground">
              Runner groups are only available for organization-level runners.
            </p>
          ) : null}

          {!isLive && (
            <Button type="submit" disabled={loading} className="mt-4 w-full">
              {loading ? (
                <>
                  <RefreshCw className="mr-2 size-4 animate-spin" />
                  Retrying...
                </>
              ) : (
                <>
                  <RefreshCw className="mr-2 size-4" />
                  {isStandaloneRegistered ? "Retry Registration" : "Update and Retry"}
                </>
              )}
            </Button>
          )}
        </form>
      </CardContent>
    </Card>

      <PatGuideDialog
        owner={patGuideOwner}
        scopeTypes={patGuideScopeTypes}
        patType={patType}
        onOpenChange={(open) => !open && setPatGuideOwner(null)}
      />
    </>
  )
}

export default function RunnerDetail() {
  const navigate = useNavigate()
  const { name } = useParams<{ name: string }>()

  const { data: runner, mutate, isLoading } = useSWR<RunnerDetailData>(
    name ? `runner:${name}` : null,
    () => api.getRunner(name!),
    { refreshInterval: 5000 }
  )

  // Compute scope from runner URL — used in status card badge and passed to ConfigForm
  const scope = useMemo(() => {
    if (!runner?.url) return null
    try {
      const pathParts = new URL(runner.url).pathname.split("/").filter(Boolean)
      if (pathParts.length === 1) return "Organization"
      if (pathParts.length >= 2) return "Repository"
      return null
    } catch {
      return null
    }
  }, [runner?.url])

  const removeRunner = async (force: boolean) => {
    if (!name) return
    try {
      await api.removeRunner(name, force)
      toast.success(`Runner ${name} removal requested`)
      navigate("/")
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Unknown error")
    }
  }

  if (isLoading || !runner) {
    return (
      <div className="mx-auto flex max-w-2xl flex-col gap-8">
        <div className="flex items-center gap-4">
          <Link to="/">
            <Button variant="ghost" size="icon">
              <ArrowLeft className="size-5" />
            </Button>
          </Link>
          <Skeleton className="h-8 w-48" />
        </div>
        <Card>
          <CardHeader>
            <Skeleton className="h-5 w-3/4" />
            <Skeleton className="h-4 w-1/2" />
          </CardHeader>
          <CardContent>
            <Skeleton className="h-40 w-full" />
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-8">
      {/* Header */}
      <div className="flex items-center gap-4">
        <Link to="/">
          <Button variant="ghost" size="icon">
            <ArrowLeft className="size-5" />
          </Button>
        </Link>
        <div className="flex-1 min-w-0">
          <h1 className="text-3xl font-bold tracking-tight truncate">
            {runner.name}
          </h1>
        </div>

      </div>

      {/* Status Section */}
      <Card>
        <CardHeader>
          <CardTitle>Status</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground truncate mb-3" title={runner.url}>
            {runner.url}
          </p>
          <div className="flex flex-wrap gap-4 text-sm">
            {scope && (
              <Badge variant="secondary">
                {scope}
              </Badge>
            )}
            <Badge
              variant={
                runner.state === "Online" ? "default" :
                runner.state === "Busy" ? "secondary" :
                runner.state === "Offline" || runner.state === "Failed" ? "destructive" : "outline"
              }
            >
              {runner.state === "Busy" && runner.mode === "scaleset"
                ? `Busy (${runner.active_workers})`
                : runner.state}
            </Badge>
            <div className="flex items-center gap-2 text-muted-foreground">
              <Server className="size-4" />
              <Badge variant="outline">
                {runner.mode === "standalone" ? "Standalone" : "Scale Set"}
              </Badge>
            </div>

            {runner.runner_group && (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Badge variant="outline">Group: {runner.runner_group}</Badge>
              </div>
            )}
            <div className="flex items-center gap-2 text-muted-foreground">
              <Clock className="size-4" />
              {runner.jobs_completed} jobs completed
            </div>
            {runner.active_workers > 0 && (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Activity className="size-4" />
                {runner.active_workers} active workers
              </div>
            )}
          </div>
          {runner.state === "Failed" && runner.error && (
            <p className="mt-3 text-sm text-destructive" title={runner.error}>
              {runner.error}
            </p>
          )}
        </CardContent>
      </Card>

      {/* Configuration Form — only shown if user can manage this runner */}
      {runner.can_manage ? (
        <ConfigForm
          key={runner.name}
          runner={runner}
          scope={scope}
          onUpdated={() => mutate()}
        />
      ) : (
        <div className="py-12 text-center text-muted-foreground border rounded-lg border-dashed">
          You don't have admin access to this repository or organization.
          <br />
          Contact an admin to modify this runner's configuration.
        </div>
      )}

      {/* Delete Section — only shown if user can manage this runner */}
      {runner.can_manage && (
      <Card className="border-destructive/30">
        <CardHeader>
          <CardTitle className="text-destructive">Danger Zone</CardTitle>
          <CardDescription>
            Remove this runner from the mux and deregister it from GitHub.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <AlertDialog>
            <AlertDialogTrigger
              render={
                <Button variant="destructive" className="gap-2">
                  <Trash2 className="size-4" />
                  Remove Runner
                </Button>
              }
            />
              <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Remove Runner?</AlertDialogTitle>
                <AlertDialogDescription>
                  Are you sure you want to remove <strong>{runner.name}</strong>?
                  {runner.mode === "standalone" && (
                    <>
                      {" "}Deregistration will use your OAuth session token
                      automatically.
                    </>
                  )}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction onClick={() => removeRunner(false)}>
                  Drain & Remove
                </AlertDialogAction>
                <AlertDialogAction
                  className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  onClick={() => removeRunner(true)}
                >
                  Force Remove
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </CardContent>
      </Card>
      )}
    </div>
  )
}
