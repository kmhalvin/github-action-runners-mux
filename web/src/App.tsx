import { BrowserRouter, Routes, Route, Link, Navigate, useNavigate } from "react-router"
import { Server, Settings as SettingsIcon, LayoutDashboard, LogIn, LogOut, Check } from "lucide-react"
import useSWR, { mutate } from "swr"
import Overview from "./pages/Overview"
import AddRunner from "./pages/AddRunner"
import RunnerDetail from "./pages/RunnerDetail"
import SettingsPage from "./pages/Settings"
import Callback from "./pages/Callback"
import { Toaster } from "@/components/ui/sonner"
import { ThemeToggle } from "@/components/theme-toggle"
import { api } from "@/lib/api"
import type { AuthHost, AuthStatus } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { toast } from "sonner"
import React from "react"

class ErrorBoundary extends React.Component<{ children: React.ReactNode }, { hasError: boolean; error: Error | null }> {
  constructor(props: { children: React.ReactNode }) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error }
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="flex flex-col items-center justify-center min-h-screen p-4">
          <h2 className="text-2xl font-bold mb-4">Something went wrong</h2>
          <p className="text-muted-foreground mb-4">{this.state.error?.message}</p>
          <button
            className="px-4 py-2 bg-primary text-primary-foreground rounded-md"
            onClick={() => window.location.reload()}
          >
            Reload page
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center min-h-[50vh]">
      <h2 className="text-3xl font-bold mb-4">404 - Not Found</h2>
      <p className="text-muted-foreground">The page you're looking for doesn't exist.</p>
    </div>
  )
}

/** HostLoginButtons renders one sign-in button per configured GitHub host. */
function HostLoginButtons() {
  const { data: hosts } = useSWR<AuthHost[]>("auth-hosts", api.getAuthHosts, {
    refreshInterval: 30000,
  })
  const { data: statuses } = useSWR<AuthStatus[]>("auth-status", api.getAuthStatus, {
    refreshInterval: 30000,
  })

  if (!hosts || hosts.length === 0) return null

  const isLoggedInTo = (host: string) =>
    statuses?.some((s) => s.host === host && s.logged_in) ?? false

  const handleLogin = (host: string, clientId: string) => {
    const redirectUri = `${window.location.origin}/callback?host=${encodeURIComponent(host)}`
    const authUrl = `https://${host}/login/oauth/authorize?client_id=${clientId}&redirect_uri=${encodeURIComponent(redirectUri)}&scope=repo%20admin:org`
    window.location.assign(authUrl)
  }

  const handleLogout = async (host: string) => {
    await api.logout(host)
    mutate("auth-status")
    mutate("auth-guard")
    toast.success(`Signed out of ${host}`)
  }

  return (
    <div className="flex items-center gap-2">
      {hosts.map((h) => {
        const loggedIn = isLoggedInTo(h.host)
        if (loggedIn) {
          return (
            <div key={h.host} className="flex items-center gap-1">
              <Button
                variant="outline"
                size="sm"
                className="gap-2"
                onClick={() => handleLogin(h.host, h.client_id)}
              >
                <Check className="size-4 text-green-500" />
                <span className="hidden sm:inline">{h.host}</span>
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                onClick={() => handleLogout(h.host)}
                title={`Sign out of ${h.host}`}
              >
                <LogOut className="size-4" />
              </Button>
            </div>
          )
        }
        return (
          <Button
            key={h.host}
            variant="default"
            size="sm"
            className="gap-2"
            onClick={() => handleLogin(h.host, h.client_id)}
          >
            <LogIn className="size-4" />
            <span className="hidden sm:inline">Sign in to {h.host}</span>
          </Button>
        )
      })}
    </div>
  )
}

/** RequireAuth checks if the user is logged in to at least one host. */
function RequireAuth({ children }: { children: React.ReactNode }) {
  const { data: statuses, isLoading } = useSWR<AuthStatus[]>(
    "auth-guard",
    api.getAuthStatus
  )
  const navigate = useNavigate()

  if (isLoading) {
    return (
      <div className="flex min-h-[50vh] items-center justify-center">
        <div className="size-8 animate-spin rounded-full border-2 border-muted border-t-foreground" />
      </div>
    )
  }

  // No auth configured — open access
  if (!statuses || statuses.length === 0) {
    return <>{children}</>
  }

  const anyLoggedIn = statuses.some((s) => s.logged_in)
  if (!anyLoggedIn) {
    toast.error("Sign in required to access that page")
    navigate("/")
    return null
  }

  return <>{children}</>
}

function Layout({ children }: { children: React.ReactNode }) {
  const { data: authStatus } = useSWR<AuthStatus[]>("auth-status", api.getAuthStatus, {
    refreshInterval: 30000,
  })
  const isAdmin = authStatus?.some((s) => s.is_admin) ?? false

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b">
        <div className="flex h-16 items-center px-4 md:px-6">
          <div className="flex items-center gap-2 font-semibold">
            <Server className="size-6" />
            <span>GitHub Mux</span>
          </div>
          <nav className="ml-6 flex items-center gap-4 text-sm lg:gap-6">
            <Link
              to="/"
              className="text-muted-foreground transition-colors hover:text-foreground"
            >
              <div className="flex items-center gap-2">
                <LayoutDashboard className="size-4" />
                Overview
              </div>
            </Link>
            {isAdmin && (
              <Link
                to="/settings"
                className="text-muted-foreground transition-colors hover:text-foreground"
              >
                <div className="flex items-center gap-2">
                  <SettingsIcon className="size-4" />
                  Settings
                </div>
              </Link>
            )}
          </nav>
          <div className="ml-auto flex items-center gap-2">
            <ThemeToggle />
            <HostLoginButtons />
          </div>
        </div>
      </header>
      <main className="p-4 md:p-8">
        {children}
      </main>
      <Toaster />
    </div>
  )
}

function App() {
  return (
    <ErrorBoundary>
      <BrowserRouter>
        <Routes>
          <Route path="/callback" element={<Callback />} />
          <Route path="/login" element={<Navigate to="/" replace />} />
          <Route
            path="/*"
            element={
              <Layout>
                <Routes>
                  <Route path="/" element={<Overview />} />
                  <Route path="/add" element={<RequireAuth><AddRunner /></RequireAuth>} />
                  <Route path="/runner/:name" element={<RequireAuth><RunnerDetail /></RequireAuth>} />
                  <Route path="/settings" element={<RequireAuth><SettingsPage /></RequireAuth>} />
                  <Route path="*" element={<NotFound />} />
                </Routes>
              </Layout>
            }
          />
        </Routes>
      </BrowserRouter>
    </ErrorBoundary>
  )
}

export default App
