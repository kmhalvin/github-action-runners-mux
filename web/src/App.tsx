import { BrowserRouter, Routes, Route, Link } from "react-router";
import { Server, Settings as SettingsIcon, LayoutDashboard } from "lucide-react";
import Overview from "./pages/Overview";
import AddRunner from "./pages/AddRunner";
import SettingsPage from "./pages/Settings";
import { Toaster } from "@/components/ui/sonner";
import React from "react";

class ErrorBoundary extends React.Component<{ children: React.ReactNode }, { hasError: boolean; error: Error | null }> {
	constructor(props: { children: React.ReactNode }) {
		super(props);
		this.state = { hasError: false, error: null };
	}

	static getDerivedStateFromError(error: Error) {
		return { hasError: true, error };
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
			);
		}
		return this.props.children;
	}
}

function NotFound() {
	return (
		<div className="flex flex-col items-center justify-center min-h-[50vh]">
			<h2 className="text-3xl font-bold mb-4">404 - Not Found</h2>
			<p className="text-muted-foreground">The page you're looking for doesn't exist.</p>
		</div>
	);
}

function Layout({ children }: { children: React.ReactNode }) {
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
						<Link
							to="/settings"
							className="text-muted-foreground transition-colors hover:text-foreground"
						>
							<div className="flex items-center gap-2">
								<SettingsIcon className="size-4" />
								Settings
							</div>
						</Link>
					</nav>
				</div>
			</header>
			<main className="p-4 md:p-8">
				{children}
			</main>
			<Toaster />
		</div>
	);
}

function App() {
	return (
		<ErrorBoundary>
			<BrowserRouter>
				<Layout>
					<Routes>
						<Route path="/" element={<Overview />} />
						<Route path="/add" element={<AddRunner />} />
						<Route path="/settings" element={<SettingsPage />} />
						<Route path="*" element={<NotFound />} />
					</Routes>
				</Layout>
			</BrowserRouter>
		</ErrorBoundary>
	);
}

export default App;
