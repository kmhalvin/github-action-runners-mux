import useSWR from "swr"
import { useState, useEffect } from "react"
import type { Settings } from "@/lib/api"
import { api } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle, CardFooter } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Save } from "lucide-react"
import { toast } from "sonner"
import { Skeleton } from "@/components/ui/skeleton"

export default function SettingsPage() {
  const { data: settings, mutate: mutateSettings } = useSWR<Settings>("settings", api.getSettings)

  const [maxWorkers, setMaxWorkers] = useState<number>(0)
  const [warmWorkers, setWarmWorkers] = useState<number>(0)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (settings) {
      setMaxWorkers(settings.max_workers)
      setWarmWorkers(settings.warm_workers)
    }
  }, [settings])

  const saveSettings = async () => {
    setSaving(true)
    try {
      await api.updateSettings({
        max_workers: maxWorkers,
        warm_workers: warmWorkers,
      })
      toast.success("Settings saved successfully")
      mutateSettings()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Unknown error")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="max-w-4xl mx-auto flex flex-col gap-8">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Settings</h1>
        <p className="text-muted-foreground">Manage global capacity.</p>
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
    </div>
  )
}
