import { BrowserRouter, Routes, Route, Link } from "react-router";
import { Server, Settings as SettingsIcon, LayoutDashboard } from "lucide-react";
import Overview from "./pages/Overview";
import AddRunner from "./pages/AddRunner";
import SettingsPage from "./pages/Settings";
import { Toaster } from "@/components/ui/sonner";

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
		<BrowserRouter>
			<Layout>
				<Routes>
					<Route path="/" element={<Overview />} />
					<Route path="/add" element={<AddRunner />} />
					<Route path="/settings" element={<SettingsPage />} />
				</Routes>
			</Layout>
		</BrowserRouter>
	);
}

export default App;
