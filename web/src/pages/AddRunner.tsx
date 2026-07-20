import { useState, useMemo } from "react"
import { useNavigate } from "react-router"
import { Save, RefreshCw, ListChecks } from "lucide-react"
import useSWR from "swr"
import { api } from "@/lib/api"
import type { AdminRepo, RunnerStatus } from "@/lib/api"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Badge } from "@/components/ui/badge"
import { RepoSelectorDialog } from "@/components/repo-selector-dialog"
import { PatGuideDialog } from "@/components/pat-guide-dialog"
import { toast } from "sonner"

function sanitizeName(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
}

function isValidName(name: string): boolean {
  return /^[a-z0-9_-]+$/.test(name)
}

function generateName(repo: AdminRepo): string {
  const randomId = Math.random().toString(36).substring(2, 8)
  if (repo.repo) {
    return `${sanitizeName(repo.owner)}-${sanitizeName(repo.repo)}-${randomId}`
  }
  return `${sanitizeName(repo.owner)}-${randomId}`
}

function getScope(url: string): string | null {
  try {
    const urlObj = new URL(url)
    const pathParts = urlObj.pathname.split("/").filter(Boolean)
    if (pathParts.length === 1) return "Organization"
    if (pathParts.length >= 2) return "Repository"
    return "Unknown"
  } catch {
    return null
  }
}

const OTHER_MODE: Record<string, string> = {
  standalone: "scaleset",
  scaleset: "standalone",
}

export default function AddRunner() {
  const navigate = useNavigate()

  const [mode, setMode] = useState<string>("standalone")
  const [selectedRepos, setSelectedRepos] = useState<AdminRepo[]>([])
  const [pats, setPats] = useState<Record<string, string>>({})
  const [name, setName] = useState("")
  const [group, setGroup] = useState("")
  const [scaleSetName, setScaleSetName] = useState("")
  const [labels, setLabels] = useState("")
  const [maxRunners, setMaxRunners] = useState(0)
  const [loading, setLoading] = useState(false)
  const [nameManuallyEdited, setNameManuallyEdited] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [patType, setPatType] = useState<"classic" | "fine-grained">("classic")
  const [patGuideOwner, setPatGuideOwner] = useState<{ host: string; owner: string; scopeType?: string } | null>(null)
  const [randomId] = useState(() =>
    Math.random().toString(36).substring(2, 8)
  )

  // Fetch existing runners to map scope -> modes
  const { data: existingRunners, mutate: mutateRunners } = useSWR<RunnerStatus[]>(
    "runners",
    api.getRunners,
    { refreshInterval: 0 }
  )

  // Map: runner URL -> Set of existing modes ("standalone", "scaleset")
  const scopeModeMap = useMemo(() => {
    const map = new Map<string, Set<string>>()
    if (!existingRunners) return map
    for (const r of existingRunners) {
      if (!map.has(r.url)) map.set(r.url, new Set())
      map.get(r.url)!.add(r.mode)
    }
    return map
  }, [existingRunners])

  const isBatch = selectedRepos.length > 1
  const singleRepo = selectedRepos.length === 1 ? selectedRepos[0] : null

  // Auto-generate runner name from single selection URL
  const nameBase = useMemo(() => {
    if (!singleRepo) return null
    try {
      const urlObj = new URL(singleRepo.url)
      const pathParts = urlObj.pathname.split("/").filter(Boolean)
      if (pathParts.length === 1) {
        return sanitizeName(pathParts[0])
      } else if (pathParts.length >= 2) {
        return sanitizeName(`${pathParts[0]}-${pathParts[1]}`)
      }
      return null
    } catch {
      return null
    }
  }, [singleRepo])

  // Derive the display name during render
  const displayName = isBatch
    ? ""
    : nameManuallyEdited
      ? name
      : nameBase
        ? `${nameBase}-${randomId}`
        : name

  // Live validation for manually-edited names (single mode only)
  const isNameInvalid =
    !isBatch && nameManuallyEdited && name !== "" && !isValidName(name)

  // Compute PAT input entries based on token type.
  const patInputs = useMemo(() => {
    const entries: { key: string; host: string; label: string; scopeType?: string }[] = []
    const seen = new Set<string>()

    if (patType === "classic") {
      for (const r of selectedRepos) {
        const key = `${r.host}/${r.type}`
        if (seen.has(key)) continue
        seen.add(key)
        const scopeLabel = r.type === "organization" ? "Organization" : "Repository"
        entries.push({
          key,
          host: r.host,
          label: `${r.host} (${scopeLabel} scopes)`,
          scopeType: r.type,
        })
      }
    } else {
      for (const r of selectedRepos) {
        const key = `${r.host}/${r.owner}`
        if (seen.has(key)) continue
        seen.add(key)
        entries.push({
          key,
          host: r.host,
          label: `${r.owner} (${r.host})`,
        })
      }
    }

    return entries
  }, [selectedRepos, patType])

  // Get the PAT key for a given repo based on the current patType
  function getPatKey(r: AdminRepo): string {
    return patType === "classic"
      ? `${r.host}/${r.type}`
      : `${r.host}/${r.owner}`
  }

  // Scope types for the PAT guide dialog.
  const patGuideScopeTypes = useMemo(() => {
    if (!patGuideOwner) return new Set<string>()
    const types = new Set<string>()
    if (patType === "classic") {
      if (patGuideOwner.scopeType) {
        types.add(patGuideOwner.scopeType)
      }
    } else {
      for (const r of selectedRepos) {
        if (r.host === patGuideOwner.host && r.owner === patGuideOwner.owner) {
          types.add(r.type)
        }
      }
    }
    return types
  }, [patGuideOwner, selectedRepos, patType])

  // Runner groups are only available for organization-level runners.
  const someOrgScope =
    selectedRepos.length > 0 && selectedRepos.some((r) => !r.repo)

  // --- Scope status: which modes exist for the single selected scope ---
  const singleScopeModes = useMemo(() => {
    if (!singleRepo) return new Set<string>()
    return scopeModeMap.get(singleRepo.url) ?? new Set<string>()
  }, [singleRepo, scopeModeMap])

  const isSingleMaximized = !isBatch && singleScopeModes.size === 2

  const isSingleModeDisabled = !isBatch && singleScopeModes.has(mode) && singleScopeModes.size < 2
  const effectiveMode = isSingleModeDisabled ? OTHER_MODE[mode] : mode
  const displayMode = isBatch ? mode : effectiveMode

  // Which scopes in batch will be ignored (conflict with current mode)
  const ignoredScopes = useMemo(() => {
    if (!isBatch) return []
    return selectedRepos.filter((r) => {
      const modes = scopeModeMap.get(r.url)
      return modes?.has(mode) ?? false
    })
  }, [isBatch, selectedRepos, scopeModeMap, mode])

  // Scopes that will actually be submitted (non-conflicting)
  const submitRepos = useMemo(() => {
    if (!isBatch) return selectedRepos
    return selectedRepos.filter((r) => !ignoredScopes.includes(r))
  }, [selectedRepos, isBatch, ignoredScopes])

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    if (selectedRepos.length === 0) {
      toast.error("Please select at least one repository or organization")
      return
    }

    if (isSingleMaximized) {
      toast.error("This scope already has both standalone and scaleset runners — remove one first")
      return
    }

    if (isBatch && ignoredScopes.length > 0) {
      if (submitRepos.length === 0) {
        toast.error(`All ${selectedRepos.length} scopes already have a ${mode} runner — nothing to add`)
        return
      }
      toast.info(`${ignoredScopes.length} scope(s) ignored (already have ${mode})`)
    }

    const submitMode = isBatch ? mode : effectiveMode

    const reposToSubmit = isBatch ? submitRepos : selectedRepos
    if (submitMode === "scaleset") {
      if (!scaleSetName.trim()) {
        toast.error("Scale Set Name is required")
        return
      }
      for (const input of patInputs) {
        if (!pats[input.key]) {
          toast.error(`PAT is required for ${input.label}`)
          return
        }
      }
    }

    setLoading(true)

    const promises = reposToSubmit.map((repo) => {
      const runnerName = isBatch
        ? generateName(repo)
        : name || generateName(repo)

      return api.addRunner({
        name: runnerName,
        mode: submitMode,
        url: repo.url,
        pat: submitMode === "scaleset" ? pats[getPatKey(repo)] : undefined,
        scale_set_name: submitMode === "scaleset" ? scaleSetName : undefined,
        max_runners: maxRunners > 0 ? maxRunners : undefined,
        labels: submitMode === "standalone" && labels ? labels.split(",").map(l => l.trim()) : undefined,
        runner_group: !repo.repo && group ? group : undefined,
      })
    })

    const results = await Promise.allSettled(promises)
    const succeeded = results.filter((r) => r.status === "fulfilled").length
    const failed = results.filter((r) => r.status === "rejected").length

    if (failed === 0) {
      toast.success(
        `${succeeded} runner${succeeded > 1 ? "s" : ""} added successfully`
      )
      navigate("/")
    } else if (succeeded > 0) {
      toast.error(
        `${succeeded} succeeded, ${failed} failed. Check the overview for details.`
      )
      navigate("/")
    } else {
      const firstError = results[0] as PromiseRejectedResult
      const reason = firstError.reason as Error & { runnerName?: string }
      const errorMsg =
        reason instanceof Error ? reason.message : "Unknown error"
      toast.error(`All ${failed} runner(s) failed: ${errorMsg}`)

      if (reason?.runnerName) {
        navigate(`/runner/${reason.runnerName}`)
      }
    }

    setLoading(false)
  }

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-8">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Add Runner</h1>
        <p className="text-muted-foreground">
          Configure a new GitHub Action runner.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Configuration</CardTitle>
          <CardDescription>
            Select one or more repositories/organizations, then choose mode.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-6">
            {/* Repo/Org Selector */}
            <div className="flex flex-col gap-3">
              <div className="flex items-center justify-between">
                <Label>Repositories or Organizations</Label>
                {selectedRepos.length > 0 && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => mutateRunners()}
                    title="Refresh scope status"
                  >
                    <RefreshCw className="size-4" />
                  </Button>
                )}
              </div>
              <Button
                type="button"
                variant="outline"
                className="justify-start"
                onClick={() => setDialogOpen(true)}
              >
                <ListChecks className="size-4" />
                {selectedRepos.length > 0
                  ? `${selectedRepos.length} selected — click to modify`
                  : "Select repositories/organizations…"}
              </Button>
              <p className="text-xs text-muted-foreground">
                Only repositories and organizations where you have admin or owner access are listed.
              </p>

              {selectedRepos.length > 0 && (
                <div className="flex flex-col gap-2 rounded-lg border p-3">
                  {selectedRepos.map((r) => {
                    const scope = getScope(r.url)
                    const existingModes = scopeModeMap.get(r.url)
                    const hasStandalone = existingModes?.has("standalone") ?? false
                    const hasScaleset = existingModes?.has("scaleset") ?? false
                    const isMaximized = hasStandalone && hasScaleset
                    const willBeIgnored = isBatch && (existingModes?.has(mode) ?? false)

                    return (
                      <div
                        key={`${r.host}-${r.owner}-${r.repo}`}
                        className={`flex items-center gap-2 flex-wrap ${willBeIgnored ? "opacity-50" : ""}`}
                      >
                        {scope && (
                          <Badge variant="secondary" className="shrink-0">
                            {scope}
                          </Badge>
                        )}
                        <span className="text-sm text-muted-foreground truncate flex-1 min-w-0">
                          {r.url}
                        </span>
                        {hasStandalone && (
                          <Badge variant="outline" className="shrink-0 text-xs">
                            has standalone
                          </Badge>
                        )}
                        {hasScaleset && (
                          <Badge variant="outline" className="shrink-0 text-xs">
                            has scaleset
                          </Badge>
                        )}
                        {isMaximized && (
                          <Badge variant="destructive" className="shrink-0 text-xs">
                            maximized
                          </Badge>
                        )}
                        {willBeIgnored && (
                          <Badge variant="secondary" className="shrink-0 text-xs">
                            will be ignored
                          </Badge>
                        )}
                      </div>
                    )
                  })}
                  {isBatch && ignoredScopes.length > 0 && (
                    <p className="text-xs text-muted-foreground mt-1">
                      {ignoredScopes.length} of {selectedRepos.length} scope(s) will be ignored (already have {mode}).
                    </p>
                  )}
                </div>
              )}
            </div>

            {/* Mode Toggle */}
            <div className="flex flex-col gap-3">
              <Label>Architecture Mode</Label>
              <ToggleGroup
                value={[displayMode]}
                onValueChange={(val: string[]) =>
                  val.length > 0 && setMode(val[0])
                }
                className="justify-start"
                aria-label="Architecture Mode"
              >
                <ToggleGroupItem
                  value="standalone"
                  className="w-32"
                  disabled={!isBatch && singleScopeModes.has("standalone")}
                >
                  Standalone
                </ToggleGroupItem>
                <ToggleGroupItem
                  value="scaleset"
                  className="w-32"
                  disabled={!isBatch && singleScopeModes.has("scaleset")}
                >
                  Scale Set
                </ToggleGroupItem>
              </ToggleGroup>
              <p className="text-sm text-muted-foreground">
                {isSingleMaximized
                  ? "This scope already has both standalone and scaleset runners. Remove one before adding a new runner."
                  : displayMode === "standalone"
                    ? "Uses short-lived registration tokens (auto-generated via OAuth). Best for isolation and strict compliance."
                    : "Uses long-lived PATs. Scales runner dynamically based on job demands."}
              </p>
            </div>

            {/* Runner Name */}
            <div className="flex flex-col gap-3">
              <div className="flex items-center justify-between">
                <Label htmlFor="name">Runner Name</Label>
                {!isBatch && nameManuallyEdited && nameBase && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 text-xs"
                    onClick={() => {
                      setName("")
                      setNameManuallyEdited(false)
                    }}
                  >
                    Reset to auto
                  </Button>
                )}
              </div>
              <Input
                id="name"
                placeholder={
                  isBatch
                    ? "Auto-generated (batch mode)"
                    : "Auto-generated from URL"
                }
                value={isBatch ? "" : displayName}
                onChange={(e) => {
                  setName(e.target.value)
                  setNameManuallyEdited(true)
                }}
                disabled={isBatch || selectedRepos.length === 0}
              />
              {isBatch && (
                <p className="text-xs text-muted-foreground">
                  Runner names are auto-generated in batch mode ({`{owner}-{repo}-{randomId}`}).
                </p>
              )}
              {isNameInvalid && (
                <div className="flex items-center gap-2 text-sm text-destructive">
                  <span>
                    Name must be lowercase with only letters, numbers, hyphens, and underscores
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setName(sanitizeName(name))}
                  >
                    Fix format
                  </Button>
                </div>
              )}
            </div>

            {/* Optional Metadata (Labels / Scale Set Name) */}
            {displayMode === "standalone" ? (
              <div className="flex flex-col gap-3">
                <Label htmlFor="labels">Labels</Label>
                <Input
                  id="labels"
                  placeholder="ubuntu-latest, gpu, x64"
                  value={labels}
                  onChange={(e) => setLabels(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Comma-separated list of custom labels.
                </p>
              </div>
            ) : (
              <>
                <div className="flex flex-col gap-3">
                  <Label htmlFor="scaleSetName">Scale Set Name</Label>
                  <Input
                    id="scaleSetName"
                    placeholder="my-scale-set"
                    value={scaleSetName}
                    onChange={(e) => setScaleSetName(e.target.value)}
                    required
                  />
                  <p className="text-xs text-muted-foreground">
                    The name of the runner scale set created on GitHub.
                  </p>
                </div>
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
                  />
                </div>
              </>
            )}

            {/* Mode-Specific Fields */}
            {displayMode === "standalone" ? (
              <div className="flex flex-col gap-3 rounded-lg border bg-secondary/30 p-4">
                <p className="text-sm text-muted-foreground">
                  Registration token is automatically generated using your OAuth
                  session. No manual token entry required.
                </p>
              </div>
            ) : (
              <div className="flex flex-col gap-4 rounded-lg border bg-secondary/30 p-4">
                {/* PAT Type Toggle: Classic vs Fine-grained */}
                <div className="flex flex-col gap-2">
                  <Label>PAT Type</Label>
                  <p className="text-xs text-muted-foreground">
                    PAT type depends on your organization's token policy.
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
                      ? "Classic tokens are not scoped per resource owner. Some organizations may disable them."
                      : "Fine-grained tokens are scoped per resource owner. One token is required for each organization or user account."}
                  </p>
                </div>

                {/* Dynamic PAT inputs based on token type */}
                {patInputs.map((input) => (
                  <div key={input.key} className="flex flex-col gap-3">
                    <div className="flex items-center justify-between">
                      <Label htmlFor={`pat-${input.key}`}>
                        Personal Access Token (PAT) for {input.label}
                      </Label>
                      <Button
                        type="button"
                        variant="link"
                        size="sm"
                        className="h-7 p-0 text-xs"
                        onClick={() =>
                          setPatGuideOwner({
                            host: input.host,
                            owner: patType === "classic" ? "" : input.label.split(" (")[0],
                            scopeType: input.scopeType,
                          })
                        }
                      >
                        How to create a PAT
                      </Button>
                    </div>
                    <Input
                      id={`pat-${input.key}`}
                      type="password"
                      placeholder={patType === "classic" ? "ghp_..." : "github_pat_..."}
                      value={pats[input.key] || ""}
                      onChange={(e) =>
                        setPats((prev) => ({ ...prev, [input.key]: e.target.value }))
                      }
                      required
                    />
                  </div>
                ))}
              </div>
            )}

            {/* Runner Group — only available for organization-level runners */}
            {someOrgScope ? (
              <div className="flex flex-col gap-3">
                <Label htmlFor="group">Runner Group</Label>
                <Input
                  id="group"
                  placeholder="default"
                  value={group}
                  onChange={(e) => setGroup(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Applied only to organization-level runners. Repository-level runners use the default group.
                </p>
              </div>
            ) : selectedRepos.length > 0 ? (
              <p className="text-sm text-muted-foreground">
                Runner groups are only available for organization-level runners.
              </p>
            ) : null}

            <Button
              type="submit"
              disabled={
                loading ||
                selectedRepos.length === 0 ||
                isNameInvalid ||
                isSingleMaximized ||
                (isBatch && submitRepos.length === 0)
              }
              className="mt-4 w-full"
            >
              {loading ? (
                <>
                  <RefreshCw className="mr-2 size-4 animate-spin" />
                  Starting Runner(s)...
                </>
              ) : (
                <>
                  <Save className="mr-2 size-4" />
                  Save and Start Runner{isBatch ? "s" : ""}
                </>
              )}
            </Button>
          </form>
        </CardContent>
      </Card>

      <RepoSelectorDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        selected={selectedRepos}
        onConfirm={setSelectedRepos}
      />

      <PatGuideDialog
        owner={patGuideOwner}
        scopeTypes={patGuideScopeTypes}
        patType={patType}
        onOpenChange={(open) => !open && setPatGuideOwner(null)}
      />
    </div>
  )
}
