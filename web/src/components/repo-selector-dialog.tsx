import { useState, useMemo, useEffect } from "react"
import useSWR from "swr"
import { api } from "@/lib/api"
import type { AdminRepo } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Dialog,
  DialogContent as DialogContentUI,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog"
import { ChevronRight, ChevronLeft, Search, RefreshCw } from "lucide-react"

interface RepoSelectorDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  selected: AdminRepo[]
  onConfirm: (selected: AdminRepo[]) => void
}

function repoKey(r: AdminRepo) {
  return `${r.host}:${r.owner}:${r.repo}`
}

function repoLabel(r: AdminRepo) {
  return r.repo ? `${r.owner}/${r.repo}` : r.owner
}

/** Inner content — remounts via key prop when dialog opens, so useState
 *  initializer re-runs with fresh selected prop. */
function SelectorPanel({
  selected,
  onConfirm,
  onCancel,
}: {
  selected: AdminRepo[]
  onConfirm: (selected: AdminRepo[]) => void
  onCancel: () => void
}) {
  const [searchQuery, setSearchQuery] = useState("")
  const [debouncedQuery, setDebouncedQuery] = useState("")
  const [leftSelected, setLeftSelected] = useState<Set<string>>(new Set())
  const [rightSelected, setRightSelected] = useState<Set<string>>(new Set())
  const [rightPanel, setRightPanel] = useState<AdminRepo[]>(() => [...selected])

  // Debounce search input to avoid filtering the entire repo list on every keystroke
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(searchQuery), 150)
    return () => clearTimeout(timer)
  }, [searchQuery])

  const { data: repos, isLoading, isValidating, mutate } = useSWR<AdminRepo[]>(
    "github-repos",
    api.getGitHubRepos,
    {
      revalidateIfStale: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
    }
  )

  const rightKeys = useMemo(
    () => new Set(rightPanel.map(repoKey)),
    [rightPanel]
  )

  const leftItems = useMemo(() => {
    if (!repos) return []
    const q = debouncedQuery.toLowerCase()
    return repos.filter((r) => {
      if (rightKeys.has(repoKey(r))) return false
      if (!q) return true
      const fullRepo = r.repo ? `${r.owner}/${r.repo}` : r.owner
      return (
        fullRepo.toLowerCase().includes(q) ||
        r.owner.toLowerCase().includes(q) ||
        r.repo.toLowerCase().includes(q) ||
        r.host.toLowerCase().includes(q) ||
        r.url.toLowerCase().includes(q)
      )
    })
  }, [repos, debouncedQuery, rightKeys])

  const leftOrgs = useMemo(
    () => leftItems.filter((r) => r.type === "organization"),
    [leftItems]
  )
  const leftRepos = useMemo(
    () => leftItems.filter((r) => r.type === "repository"),
    [leftItems]
  )

  const moveRight = () => {
    const toMove = leftItems.filter((r) => leftSelected.has(repoKey(r)))
    setRightPanel((prev) => [...prev, ...toMove])
    setLeftSelected(new Set())
  }

  const moveLeft = () => {
    const toRemove = new Set(
      rightPanel
        .filter((r) => rightSelected.has(repoKey(r)))
        .map(repoKey)
    )
    setRightPanel((prev) => prev.filter((r) => !toRemove.has(repoKey(r))))
    setRightSelected(new Set())
  }

  const handleConfirm = () => {
    onConfirm(rightPanel)
  }

  return (
    <>
      <div className="flex gap-4 min-h-[400px]">
        {/* Left Panel — Available */}
        <div className="flex-1 flex flex-col gap-2 min-w-0">
          <div className="text-sm font-medium">Available</div>
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-2 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
              <Input
                placeholder="Search repos or organizations…"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="pl-8"
              />
            </div>
            <Button
              variant="outline"
              size="icon"
              onClick={() => mutate()}
              disabled={isValidating}
              title="Refresh"
            >
              <RefreshCw className={`size-4 ${isValidating ? "animate-spin" : ""}`} />
            </Button>
          </div>
          <div className="flex-1 overflow-y-auto rounded-lg border max-h-[400px]">
            {isLoading ? (
              <div className="p-2 space-y-2">
                {Array.from({ length: 4 }).map((_, i) => (
                  <Skeleton key={i} className="h-8 w-full" />
                ))}
              </div>
            ) : leftItems.length === 0 ? (
              <div className="p-4 text-center text-sm text-muted-foreground">
                {repos ? "No results found." : "Loading…"}
              </div>
            ) : (
              <div className="p-1">
                {leftOrgs.length > 0 && (
                  <div className="mb-2">
                    <div className="px-2 py-1.5 text-xs text-muted-foreground font-medium">
                      Organizations
                    </div>
                    {leftOrgs.map((r) => {
                      const key = repoKey(r)
                      return (
                        <button
                          key={key}
                          type="button"
                          onClick={() =>
                            setLeftSelected((prev) => {
                              const next = new Set(prev)
                              if (next.has(key)) next.delete(key)
                              else next.add(key)
                              return next
                            })
                          }
                          className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-left hover:bg-accent ${
                            leftSelected.has(key) ? "bg-accent" : ""
                          }`}
                          title={`${r.owner} (${r.host})`}
                        >
                          <span className="break-all">{r.owner}</span>
                          <span className="text-xs text-muted-foreground ml-auto shrink-0">
                            ({r.host})
                          </span>
                        </button>
                      )
                    })}
                  </div>
                )}
                {leftRepos.length > 0 && (
                  <div>
                    <div className="px-2 py-1.5 text-xs text-muted-foreground font-medium">
                      Repositories
                    </div>
                    {leftRepos.map((r) => {
                      const key = repoKey(r)
                      return (
                        <button
                          key={key}
                          type="button"
                          onClick={() =>
                            setLeftSelected((prev) => {
                              const next = new Set(prev)
                              if (next.has(key)) next.delete(key)
                              else next.add(key)
                              return next
                            })
                          }
                          className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-left hover:bg-accent ${
                            leftSelected.has(key) ? "bg-accent" : ""
                          }`}
                          title={`${r.owner}/${r.repo} (${r.host})`}
                        >
                          <span className="break-all">
                            {r.owner}/{r.repo}
                          </span>
                          <span className="text-xs text-muted-foreground ml-auto shrink-0">
                            ({r.host})
                          </span>
                        </button>
                      )
                    })}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        {/* Middle — Arrows */}
        <div className="flex flex-col items-center justify-center gap-2">
          <Button
            variant="outline"
            size="icon"
            onClick={moveRight}
            disabled={leftSelected.size === 0}
            title="Move to selected"
          >
            <ChevronRight className="size-4" />
          </Button>
          <Button
            variant="outline"
            size="icon"
            onClick={moveLeft}
            disabled={rightSelected.size === 0}
            title="Remove from selected"
          >
            <ChevronLeft className="size-4" />
          </Button>
        </div>

        {/* Right Panel — Selected */}
        <div className="flex-1 flex flex-col gap-2 min-w-0">
          <div className="text-sm font-medium">
            Selected ({rightPanel.length})
          </div>
          <div className="flex-1 overflow-y-auto rounded-lg border max-h-[400px]">
            {rightPanel.length === 0 ? (
              <div className="p-4 text-center text-sm text-muted-foreground">
                No items selected.
              </div>
            ) : (
              <div className="p-1">
                {rightPanel.map((r) => {
                  const key = repoKey(r)
                  return (
                    <button
                      key={key}
                      type="button"
                      onClick={() =>
                        setRightSelected((prev) => {
                          const next = new Set(prev)
                          if (next.has(key)) next.delete(key)
                          else next.add(key)
                          return next
                        })
                      }
                      className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-left hover:bg-accent ${
                        rightSelected.has(key) ? "bg-accent" : ""
                      }`}
                      title={`${repoLabel(r)} (${r.host})`}
                    >
                      <span className="break-all">{repoLabel(r)}</span>
                      <span className="text-xs text-muted-foreground ml-auto shrink-0">
                        ({r.host})
                      </span>
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        </div>
      </div>

      <DialogFooter>
        <Button variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button onClick={handleConfirm} disabled={rightPanel.length === 0}>
          Confirm Selection ({rightPanel.length})
        </Button>
      </DialogFooter>
    </>
  )
}

export function RepoSelectorDialog({
  open,
  onOpenChange,
  selected,
  onConfirm,
}: RepoSelectorDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContentUI className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Select Repositories/Organizations</DialogTitle>
        </DialogHeader>
        {open && (
          <SelectorPanel
            key="selector"
            selected={selected}
            onConfirm={(repos) => {
              onConfirm(repos)
              onOpenChange(false)
            }}
            onCancel={() => onOpenChange(false)}
          />
        )}
      </DialogContentUI>
    </Dialog>
  )
}
