import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Ban, CircleCheck, KeyRound, MoreHorizontal, Pencil, Plus, ShieldCheck, ShieldOff, Trash2 } from "lucide-react";
import { api } from "@/api";
import { useAuth } from "@/auth";
import { useCrm } from "@/context";
import { PageHeader, UserAvatar } from "@/components/shared";
import { Eyebrow, StatusPill } from "@/components/health";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { parseTimestamp } from "@/lib/utils";
import type { Account, Role } from "@/types";

const ROLE_LABEL: Record<Role, string> = { admin: "Admin", user: "User" };
const PASSWORD_HINT = "8 to 72 characters. Letters outside A–Z, like é, count as two or more.";

/** What a dialog on this page is doing, and to whom. */
type Open =
  | { kind: "add" }
  | { kind: "name" | "password" | "disable" | "delete"; account: Account }
  | null;

/**
 * Settings → Users (admins only; the API refuses anyone else). The accounts
 * that can sign in: add one, rename it, make it an admin or a user, disable or
 * enable it, give it a new password, delete it. The API guards the rules (you
 * can't remove or demote yourself, the CRM keeps one active admin) and its
 * words say why when it refuses.
 */
export function UsersPage({ navigate }: { navigate: (to: string) => void }) {
  const { user: me, setUser } = useAuth();
  const { setError } = useCrm();
  const [accounts, setAccounts] = useState<Account[] | null>(null);
  const [open, setOpen] = useState<Open>(null);

  const load = useCallback(async () => {
    try {
      setAccounts((await api<{ users: Account[] }>("GET", "/api/users")).users);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not load the accounts");
    }
  }, [setError]);
  useEffect(() => { void load(); }, [load]);

  const replace = (a: Account) => setAccounts((list) => (list ?? []).map((x) => (x.id === a.id ? a : x)));
  /** A change to one account; the API's answer is the account as it is now. */
  const patch = async (a: Account, body: Partial<Pick<Account, "name" | "role" | "disabled">> & { password?: string }) => {
    const r = await api<{ user: Account }>("PATCH", `/api/users/${encodeURIComponent(a.id)}`, body);
    replace(r.user);
    if (r.user.id === me.id) setUser({ id: r.user.id, email: r.user.email, name: r.user.name, role: r.user.role });
  };
  /** A change straight from the menu: a refusal shows in the error banner. */
  const quick = (a: Account, body: Partial<Pick<Account, "role" | "disabled">>) => {
    patch(a, body).catch((e) => setError(e instanceof Error ? e.message : "Could not save"));
  };

  return (
    <>
      <PageHeader title="Users">
        <Button size="sm" onClick={() => setOpen({ kind: "add" })}>
          <Plus className="size-4" /> Add user
        </Button>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        <div className="mx-auto flex max-w-4xl flex-col gap-6">
          <p className="text-sm text-muted-foreground">
            Everyone who can sign in to the CRM. Admins also manage this list. A new password or disabling an account
            signs that person out everywhere and deletes their API tokens.
          </p>

          {accounts === null ? (
            <div className="py-12 text-center text-sm text-muted-foreground">Loading…</div>
          ) : (
            <section className="flex flex-col gap-3">
              <Eyebrow>{accounts.length} {accounts.length === 1 ? "account" : "accounts"}</Eyebrow>
              <div className="overflow-hidden rounded-md shadow-edge">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Email</TableHead>
                      <TableHead>Role</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Added</TableHead>
                      <TableHead className="w-12"><span className="sr-only">Actions</span></TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {accounts.map((a) => {
                      const self = a.id === me.id;
                      return (
                        <TableRow key={a.id}>
                          <TableCell>
                            <span className="flex min-w-0 items-center gap-2">
                              <UserAvatar name={a.name} email={a.email} className="size-6 text-[0.625rem]" />
                              <span className="truncate font-medium">{a.name || <span className="font-normal text-faint">No name</span>}</span>
                              {self && <span className="shrink-0 text-xs text-muted-foreground">(you)</span>}
                            </span>
                          </TableCell>
                          <TableCell className="text-muted-foreground">{a.email}</TableCell>
                          <TableCell>
                            <span className="h-5 rounded-sm border border-border bg-secondary px-1.5 text-[0.6875rem] leading-[1.125rem] text-muted-foreground">{ROLE_LABEL[a.role]}</span>
                          </TableCell>
                          <TableCell>{a.disabled ? <StatusPill tone="neutral">Disabled</StatusPill> : <span className="text-muted-foreground">Active</span>}</TableCell>
                          <TableCell className="tabular">{parseTimestamp(a.created_at).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })}</TableCell>
                          <TableCell className="text-right">
                            <DropdownMenu>
                              <DropdownMenuTrigger asChild>
                                <Button variant="ghost" size="icon" aria-label={`Actions for ${a.email}`}>
                                  <MoreHorizontal className="size-4" />
                                </Button>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end">
                                <DropdownMenuItem onClick={() => setOpen({ kind: "name", account: a })}>
                                  <Pencil /> {a.name ? "Rename" : "Add a name"}
                                </DropdownMenuItem>
                                {self ? (
                                  <DropdownMenuItem onClick={() => navigate("/settings/account")}>
                                    <KeyRound /> Change your password
                                  </DropdownMenuItem>
                                ) : (
                                  <>
                                    <DropdownMenuItem onClick={() => quick(a, { role: a.role === "admin" ? "user" : "admin" })}>
                                      {a.role === "admin" ? <><ShieldOff /> Make a user</> : <><ShieldCheck /> Make an admin</>}
                                    </DropdownMenuItem>
                                    <DropdownMenuItem onClick={() => setOpen({ kind: "password", account: a })}>
                                      <KeyRound /> Set a new password
                                    </DropdownMenuItem>
                                    {a.disabled ? (
                                      <DropdownMenuItem onClick={() => quick(a, { disabled: false })}>
                                        <CircleCheck /> Enable
                                      </DropdownMenuItem>
                                    ) : (
                                      <DropdownMenuItem onClick={() => setOpen({ kind: "disable", account: a })}>
                                        <Ban /> Disable
                                      </DropdownMenuItem>
                                    )}
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem onClick={() => setOpen({ kind: "delete", account: a })} className="text-destructive focus:text-destructive">
                                      <Trash2 /> Delete
                                    </DropdownMenuItem>
                                  </>
                                )}
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>
            </section>
          )}
        </div>
      </div>

      <AddUserDialog open={open?.kind === "add"} onClose={() => setOpen(null)} onAdded={(a) => setAccounts((list) => [...(list ?? []), a].sort((x, y) => (x.email < y.email ? -1 : 1)))} />
      {open && open.kind === "name" && (
        <NameDialog account={open.account} onClose={() => setOpen(null)} onSave={(name) => patch(open.account, { name })} />
      )}
      {open && open.kind === "password" && (
        <PasswordDialog account={open.account} onClose={() => setOpen(null)} onSave={(password) => patch(open.account, { password })} />
      )}
      {open && open.kind === "disable" && (
        <ConfirmDialog
          title={`Disable ${who(open.account)}?`}
          description="They're signed out everywhere and can't sign in. Their API tokens are deleted: enabling the account again doesn't bring them back."
          action="Disable"
          doing="Disabling…"
          onClose={() => setOpen(null)}
          onConfirm={() => patch(open.account, { disabled: true })}
        />
      )}
      {open && open.kind === "delete" && (
        <ConfirmDialog
          title={`Delete ${who(open.account)}?`}
          description="Their account, sessions and API tokens are deleted. The records they worked on stay. This can't be undone."
          action="Delete"
          doing="Deleting…"
          onClose={() => setOpen(null)}
          onConfirm={async () => {
            await api("DELETE", `/api/users/${encodeURIComponent(open.account.id)}`);
            setAccounts((list) => (list ?? []).filter((x) => x.id !== open.account.id));
          }}
        />
      )}
    </>
  );
}

const who = (a: Account) => a.name || a.email;

/** A dialog's own error line: the API's words for why it refused. */
function DialogError({ error }: { error: string | null }) {
  return error ? <p role="alert" className="text-[0.8125rem] text-destructive">{error}</p> : null;
}

/** Runs a dialog's save: busy while it runs, the API's error kept on screen, closed when it worked. */
function useSave(onClose: () => void) {
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const run = async (work: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await work();
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    } finally {
      setBusy(false);
    }
  };
  return { error, busy, run };
}

function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-[0.8125rem] text-muted-foreground">{hint}</p>}
    </div>
  );
}

function AddUserDialog({ open, onClose, onAdded }: { open: boolean; onClose: () => void; onAdded: (a: Account) => void }) {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("user");
  const { error, busy, run } = useSave(onClose);

  useEffect(() => {
    if (open) {
      setEmail("");
      setName("");
      setPassword("");
      setRole("user");
    }
  }, [open]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      const r = await api<{ user: Account }>("POST", "/api/users", { email, name, password, role });
      onAdded(r.user);
    });
  };

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Add a user</DialogTitle>
            <DialogDescription>They sign in with this email and password. Give them the password safely; they can change it in Account.</DialogDescription>
          </DialogHeader>
          <Field id="new-user-email" label="Email">
            <Input id="new-user-email" type="email" autoComplete="off" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Field id="new-user-name" label="Name">
            <Input id="new-user-name" autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field id="new-user-password" label="Password" hint={PASSWORD_HINT}>
            <Input id="new-user-password" type="password" autoComplete="new-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Field id="new-user-role" label="Role" hint="An admin also manages the accounts.">
            <Select value={role} onValueChange={(v) => setRole(v as Role)}>
              <SelectTrigger id="new-user-role" className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="user">User</SelectItem>
                <SelectItem value="admin">Admin</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <DialogError error={error} />
          <DialogFooter>
            <DialogClose asChild><Button type="button" size="sm" variant="outline">Cancel</Button></DialogClose>
            <Button type="submit" size="sm" disabled={busy}>{busy ? "Adding…" : "Add user"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function NameDialog({ account, onClose, onSave }: { account: Account; onClose: () => void; onSave: (name: string) => Promise<void> }) {
  const [name, setName] = useState(account.name);
  const { error, busy, run } = useSave(onClose);
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="sm:max-w-sm">
        <form onSubmit={(e) => { e.preventDefault(); void run(() => onSave(name)); }} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{account.name ? "Rename" : "Add a name"}</DialogTitle>
            <DialogDescription>{account.email}</DialogDescription>
          </DialogHeader>
          <Field id="user-name" label="Name">
            <Input id="user-name" autoComplete="off" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <DialogError error={error} />
          <DialogFooter>
            <DialogClose asChild><Button type="button" size="sm" variant="outline">Cancel</Button></DialogClose>
            <Button type="submit" size="sm" disabled={busy}>{busy ? "Saving…" : "Save"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PasswordDialog({ account, onClose, onSave }: { account: Account; onClose: () => void; onSave: (password: string) => Promise<void> }) {
  const [password, setPassword] = useState("");
  const { error, busy, run } = useSave(onClose);
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="sm:max-w-sm">
        <form onSubmit={(e) => { e.preventDefault(); void run(() => onSave(password)); }} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Set a new password for {who(account)}</DialogTitle>
            <DialogDescription>They're signed out everywhere and their API tokens are deleted. Give them the new password safely.</DialogDescription>
          </DialogHeader>
          <Field id="user-password" label="New password" hint={PASSWORD_HINT}>
            <Input id="user-password" type="password" autoComplete="new-password" required autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <DialogError error={error} />
          <DialogFooter>
            <DialogClose asChild><Button type="button" size="sm" variant="outline">Cancel</Button></DialogClose>
            <Button type="submit" size="sm" disabled={busy}>{busy ? "Saving…" : "Set password"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ConfirmDialog({ title, description, action, doing, onClose, onConfirm }: {
  title: string;
  description: string;
  action: string;
  doing: string;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  const { error, busy, run } = useSave(onClose);
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogError error={error} />
        <DialogFooter>
          <DialogClose asChild><Button size="sm" variant="ghost">Cancel</Button></DialogClose>
          <Button size="sm" variant="destructive" disabled={busy} onClick={() => void run(onConfirm)}>{busy ? doing : action}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
