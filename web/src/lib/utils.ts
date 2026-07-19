import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Returns the base path under which the app is served.
 * The Go backend injects `window.__BASE_PATH__` into index.html at runtime
 * (e.g. "/runner"). Defaults to "" (root) when not set.
 */
export function getBasePath(): string {
  return (window as unknown as { __BASE_PATH__?: string }).__BASE_PATH__ || ""
}
