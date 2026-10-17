import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Check, Copy, Plus, Trash2 } from "lucide-react";
import { api } from "@/api";
import { useCrm } from "@/context";
import { EmptyState, PageHeader } from "@/components/shared";
import { Eyebrow } from "@/components/health";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { parseTimestamp, timeAgo } from "@/lib/utils";
import type { ApiToken } from "@/types";

/**
 * Settings → API tokens: your own tokens, for scripts and agents. A token acts
 * as you on the CRM's records, sent as `Authorization: Bearer <token>`; it
 * can't manage accounts or tokens (the API answers 403). Its secret is shown
 * once, when it is created; the list only knows its first characters.
 */
export function TokensPage() {
  const { setError } = useCrm();
  const [tokens, setTokens] = useState<ApiToken[] | null>(null);
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<ApiToken | null>(null);

  const load = useCallback(async () => {
    try {
      setTokens((await api<{ tokens: ApiToken[] }>("GET", "/api/tokens")).tokens);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not load your tokens");
    }
  }, [setError]);
  useEffect(() => { void load(); }, [load]);

  const createButton = (
    <Button size="sm" onClick={() => setCreating(true)}>
      <Plus className="size-4" /> Create token
    </Button>
  );

  return (
    <>
      <PageHeader title="API tokens">{tokens && tokens.length > 0 && createButton}</PageHeader>
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-6">
        <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col gap-6">
          <p className="text-sm text-muted-foreground">
            A token lets a script or an agent use the CRM's API as you. Send it as{" "}
            <code className="rounded-xs bg-secondary px-1 py-0.5 font-mono text-[0.8125rem] text-foreground">Authorization: Bearer &lt;token&gt;</code>.
            It can read and change the CRM's records, not accounts or tokens. The API is described
            in <a href="/llms.txt" target="_blank" rel="noreferrer" className="text-info hover:underline">llms.txt</a>.
          </p>

          {tokens === null ? (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>
          ) : tokens.length === 0 ? (
            <EmptyState title="No API tokens yet. Create one for each script or agent, so you can revoke them one by one." action={createButton} />
          ) : (
            <section className="flex flex-col gap-3">
              <Eyebrow>{tokens.length} {tokens.length === 1 ? "token" : "tokens"}</Eyebrow>
              <div className="overflow-hidden rounded-md shadow-edge">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Token</TableHead>
                      <TableHead>Last used</TableHead>
                      <TableHead>Created</TableHead>
                      <TableHead className="w-12"><span className="sr-only">Actions</span></TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {tokens.map((t) => (
                      <TableRow key={t.id}>
                        <TableCell className="font-medium">{t.name}</TableCell>
                        <TableCell><code className="font-mono text-[0.8125rem] text-muted-foreground">{t.prefix}…</code></TableCell>
                        <TableCell className="tabular" title={t.last_used_at ? parseTimestamp(t.last_used_at).toLocaleString() : undefined}>
                          {t.last_used_at ? timeAgo(t.last_used_at) : <span className="text-faint">Never</span>}
                        </TableCell>
                        <TableCell className="tabular">{parseTimestamp(t.created_at).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })}</TableCell>
                        <TableCell className="text-right">
                          <Button variant="ghost" size="icon" aria-label={`Revoke ${t.name}`} title="Revoke" onClick={() => setRevoking(t)}>
                            <Trash2 className="size-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>
          )}
        </div>
      </div>

      <CreateTokenDialog open={creating} onOpenChange={setCreating} onCreated={(t) => setTokens((list) => [t, ...(list ?? [])])} />
      <RevokeTokenDialog token={revoking} onClose={() => setRevoking(null)} onRevoked={(id) => setTokens((list) => (list ?? []).filter((t) => t.id !== id))} />
    </>
  );
}

/** Name it, then copy the secret: the one time it is shown. */
function CreateTokenDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (open: boolean) => void; onCreated: (token: ApiToken) => void }) {
  const [name, setName] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) {
      setName("");
      setSecret(null);
      setError(null);
    }
  }, [open]);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await api<{ token: ApiToken; secret: string }>("POST", "/api/tokens", { name });
      setSecret(r.secret);
      onCreated(r.token);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create the token");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* While the secret is on screen a stray click outside doesn't lose it; Done, the X and Escape still close. */}
      <DialogContent className="sm:max-w-md" onInteractOutside={(e) => { if (secret) e.preventDefault(); }}>
        {secret === null ? (
          <form onSubmit={create} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Create an API token</DialogTitle>
              <DialogDescription>Name it after what will use it, so you know which one to revoke later.</DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="token-name">Name</Label>
              <Input id="token-name" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Nightly import, support agent…" />
            </div>
            {error && <p role="alert" className="text-[0.8125rem] text-destructive">{error}</p>}
            <DialogFooter>
              <DialogClose asChild><Button type="button" size="sm" variant="outline">Cancel</Button></DialogClose>
              <Button type="submit" size="sm" disabled={busy}>{busy ? "Creating…" : "Create token"}</Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Copy your new token</DialogTitle>
              <DialogDescription>This is the only time it is shown. Keep it where the script or agent reads its secrets, or in a password manager.</DialogDescription>
            </DialogHeader>
            <SecretBox secret={secret} />
            <DialogFooter>
              <DialogClose asChild><Button size="sm">Done</Button></DialogClose>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function SecretBox({ secret }: { secret: string }) {
  const [copied, setCopied] = useState<"yes" | "failed" | null>(null);
  const box = useRef<HTMLElement>(null);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied("yes");
    } catch {
      // No clipboard (an insecure origin, a denied permission): select it for a manual copy.
      const range = document.createRange();
      if (box.current) range.selectNodeContents(box.current);
      window.getSelection()?.removeAllRanges();
      window.getSelection()?.addRange(range);
      setCopied("failed");
    }
  };
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2 rounded-sm bg-secondary p-2 pl-3">
        <code ref={box} data-testid="token-secret" className="min-w-0 flex-1 break-all font-mono text-[0.8125rem]">{secret}</code>
        <Button size="sm" variant="outline" onClick={() => void copy()} aria-label="Copy the token">
          {copied === "yes" ? <Check className="size-4" strokeWidth={2.5} /> : <Copy className="size-4" />}
          {copied === "yes" ? "Copied" : "Copy"}
        </Button>
      </div>
      {copied === "failed" && <p className="text-[0.8125rem] text-muted-foreground">The browser didn't allow copying. The token is selected: copy it with your keyboard.</p>}
    </div>
  );
}

function RevokeTokenDialog({ token, onClose, onRevoked }: { token: ApiToken | null; onClose: () => void; onRevoked: (id: string) => void }) {
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { setError(null); }, [token]);

  const revoke = async () => {
    if (!token) return;
    setBusy(true);
    try {
      await api("DELETE", `/api/tokens/${encodeURIComponent(token.id)}`);
      onRevoked(token.id);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not revoke the token");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={!!token} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Revoke “{token?.name}”?</DialogTitle>
          <DialogDescription>Whatever uses it stops working at once. This can't be undone.</DialogDescription>
        </DialogHeader>
        {error && <p role="alert" className="text-[0.8125rem] text-destructive">{error}</p>}
        <DialogFooter>
          <DialogClose asChild><Button size="sm" variant="ghost">Cancel</Button></DialogClose>
          <Button size="sm" variant="destructive" onClick={() => void revoke()} disabled={busy}>{busy ? "Revoking…" : "Revoke"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
