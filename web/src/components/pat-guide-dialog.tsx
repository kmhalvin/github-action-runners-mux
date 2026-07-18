import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"
import { buttonVariants } from "@/components/ui/button"
import { ExternalLink } from "lucide-react"
import { cn } from "@/lib/utils"

interface PatGuideDialogProps {
  /** When non-null, the dialog is open for this owner. When null, dialog is closed. */
  owner: { host: string; owner: string } | null
  /** Which scope types are selected — controls which permission tables to show */
  scopeTypes: Set<string>
  /** Token type: classic or fine-grained */
  patType: "classic" | "fine-grained"
  onOpenChange: (open: boolean) => void
}

export function PatGuideDialog({ owner, onOpenChange, scopeTypes, patType }: PatGuideDialogProps) {
  const showOrg = scopeTypes.has("organization")
  const showRepo = scopeTypes.has("repository")
  const open = owner !== null
  const host = owner?.host ?? ""
  const ownerName = owner?.owner ?? ""

  const isClassic = patType === "classic"

  const patUrl = isClassic
    ? host
      ? `https://${host}/settings/tokens/new`
      : "#"
    : host
      ? `https://${host}/settings/personal-access-tokens/new`
      : "#"

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            How to Create a {isClassic ? "Classic" : "Fine-Grained"} PAT{ownerName && !isClassic ? ` for ${ownerName}` : ""}
          </DialogTitle>
          <DialogDescription>
            Follow these steps to create a personal access token with the
            permissions required for scale set runners.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          {/* Direct link */}
          <a
            href={patUrl}
            target="_blank"
            rel="noopener noreferrer"
            className={cn(buttonVariants({ variant: "default" }), "w-full")}
          >
            <ExternalLink className="mr-2 size-4" />
            Create PAT on {host || "GitHub"}
          </a>

          {isClassic ? (
            <>
              {/* Classic PAT steps */}
              <ol className="flex flex-col gap-3 text-sm text-muted-foreground">
                <li>
                  <span className="font-medium text-foreground">1. Open the token creation page</span>
                  <br />
                  Go to <span className="font-mono text-xs">Settings → Developer settings → Personal access tokens → Tokens (classic) → Generate new token</span>, or click the button above.
                </li>
                <li>
                  <span className="font-medium text-foreground">2. Set token name and expiration</span>
                  <br />
                  Give the token a descriptive name and set an appropriate expiration date.
                </li>
                <li>
                  <span className="font-medium text-foreground">3. Select scopes</span>
                  <br />
                  Under <span className="font-mono text-xs">Select scopes</span>, check the required scopes based on your scope type (see tables below).
                </li>
                <li>
                  <span className="font-medium text-foreground">4. Generate and copy the token</span>
                  <br />
                  Click <span className="font-medium">Generate token</span>, then copy the generated value and paste it into the PAT field.
                </li>
              </ol>

              <div className="rounded-lg border bg-secondary/30 p-3">
                <p className="text-xs text-muted-foreground">
                  <span className="font-medium text-foreground">Note:</span> Classic tokens are not scoped per resource owner — one token works for all organizations and repositories you have access to on this host. However, you may use separate tokens for organization-scope and repository-scope runners if you prefer different permissions.
                </p>
              </div>
            </>
          ) : (
            <>
              {/* Fine-grained PAT steps */}
              <ol className="flex flex-col gap-3 text-sm text-muted-foreground">
                <li>
                  <span className="font-medium text-foreground">1. Open the token creation page</span>
                  <br />
                  Go to <span className="font-mono text-xs">Settings → Developer settings → Fine-grained personal access tokens → Generate new token</span>, or click the button above.
                </li>
                <li>
                  <span className="font-medium text-foreground">2. Select the resource owner</span>
                  <br />
                  {ownerName ? (
                    <>Select <span className="font-medium">{ownerName}</span> as the resource owner. </>
                  ) : (
                    <>Choose the organization or user account that owns the runner scope. </>
                  )}
                  The resource owner must match the scope where the runner will be registered.
                  <br />
                  <span className="mt-1 inline-block text-xs italic">
                    If you are not an owner of this organization, you will need to provide a request message explaining why you need this token. An organization owner must approve the request before the token is usable.
                  </span>
                </li>
                <li>
                  <span className="font-medium text-foreground">3. Set token name and expiration</span>
                  <br />
                  Give the token a descriptive name and set an appropriate expiration date.
                </li>
                <li>
                  <span className="font-medium text-foreground">4. Select permissions</span>
                  <br />
                  Under <span className="font-mono text-xs">Repository access</span> or <span className="font-mono text-xs">Organization permissions</span>, configure the permissions based on your scope type (see tables below).
                </li>
                <li>
                  <span className="font-medium text-foreground">5. Generate and copy the token</span>
                  <br />
                  Click <span className="font-medium">Generate token</span>, then copy the generated value and paste it into the PAT field.
                </li>
              </ol>
            </>
          )}

          {/* Permission tables — only show relevant scope types */}
          <div className="flex flex-col gap-4">
            {showOrg && (
              <div className="rounded-lg border p-3">
                <h4 className="text-sm font-semibold mb-2">
                  Organization Scope Runners
                </h4>
                <p className="text-xs text-muted-foreground mb-2">
                  Required when the runner URL points to an organization (e.g., <span className="font-mono">https://host/org</span>).
                </p>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b">
                      <th className="text-left py-1.5 font-medium">
                        {isClassic ? "Scope" : "Permission"}
                      </th>
                      <th className="text-left py-1.5 font-medium">Access</th>
                    </tr>
                  </thead>
                  <tbody>
                    {isClassic ? (
                      <>
                        <tr className="border-b">
                          <td className="py-1.5 font-mono">admin:org</td>
                          <td className="py-1.5">Read & Write</td>
                        </tr>
                      </>
                    ) : (
                      <>
                        <tr className="border-b">
                          <td className="py-1.5">Organization administration</td>
                          <td className="py-1.5">Read</td>
                        </tr>
                        <tr>
                          <td className="py-1.5">Organization self-hosted runners</td>
                          <td className="py-1.5">Read and Write</td>
                        </tr>
                      </>
                    )}
                  </tbody>
                </table>
              </div>
            )}

            {showRepo && (
              <div className="rounded-lg border p-3">
                <h4 className="text-sm font-semibold mb-2">
                  Repository Scope Runners
                </h4>
                <p className="text-xs text-muted-foreground mb-2">
                  Required when the runner URL points to a repository (e.g., <span className="font-mono">https://host/org/repo</span>).
                </p>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b">
                      <th className="text-left py-1.5 font-medium">
                        {isClassic ? "Scope" : "Permission"}
                      </th>
                      <th className="text-left py-1.5 font-medium">Access</th>
                    </tr>
                  </thead>
                  <tbody>
                    {isClassic ? (
                      <tr>
                        <td className="py-1.5 font-mono">repo</td>
                        <td className="py-1.5">Full control</td>
                      </tr>
                    ) : (
                      <>
                        <tr className="border-b">
                          <td className="py-1.5">Metadata</td>
                          <td className="py-1.5">Read (mandatory)</td>
                        </tr>
                        <tr>
                          <td className="py-1.5">Administration</td>
                          <td className="py-1.5">Read and Write</td>
                        </tr>
                      </>
                    )}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          <p className="text-xs text-muted-foreground">
            After creating the token, paste it into the PAT field above.
          </p>
        </div>
      </DialogContent>
    </Dialog>
  )
}
