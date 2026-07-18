import { useState, useEffect } from "react"
import { useNavigate, useSearchParams } from "react-router"
import { RefreshCw, AlertTriangle } from "lucide-react"
import { api } from "@/lib/api"

export default function Callback() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const code = params.get("code")
  const host = params.get("host")
  const [error, setError] = useState<string | null>(
    !code || !host ? "Missing code or host parameter in callback URL." : null
  )

  useEffect(() => {
    if (!code || !host) return

    api
      .exchangeCode(code, host)
      .then(() => {
        // The backend sets the HttpOnly auth cookie via Set-Cookie header.
        navigate("/")
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : "Failed to exchange code")
      })
  }, [code, host, navigate])

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <div className="flex flex-col items-center gap-4">
        {error ? (
          <>
            <AlertTriangle className="size-12 text-destructive" />
            <h2 className="text-xl font-bold">Authentication Failed</h2>
            <p className="text-muted-foreground text-center max-w-md">{error}</p>
            <button
              className="px-4 py-2 bg-primary text-primary-foreground rounded-md"
              onClick={() => navigate("/")}
            >
              Back to Dashboard
            </button>
          </>
        ) : (
          <>
            <RefreshCw className="size-8 animate-spin text-muted-foreground" />
            <p className="text-muted-foreground">Completing sign in…</p>
          </>
        )}
      </div>
    </div>
  )
}
