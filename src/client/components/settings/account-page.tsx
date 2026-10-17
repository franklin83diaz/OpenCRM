import { useState, type FormEvent, type MouseEvent } from "react";
import { api } from "@/api";
import { useAuth } from "@/auth";
import { PageHeader, UserAvatar } from "@/components/shared";
import { Section } from "@/components/settings-ui";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/**
 * Settings → Account: who you are signed in as, and your password. A password
 * change ends your other sessions and keeps this one; API tokens don't use the
 * password, so they keep working, and the page says so next to the form.
 */
export function AccountPage({ navigate }: { navigate: (to: string) => void }) {
  const { user } = useAuth();
  const go = (to: string) => (e: MouseEvent) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    navigate(to);
  };

  return (
    <>
      <PageHeader title="Account" />
      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        <div className="mx-auto flex max-w-2xl flex-col gap-8">
          <section aria-label="Signed in as" className="flex flex-col gap-3 rounded-md p-4 shadow-edge">
            <div className="flex items-center gap-3">
              <UserAvatar name={user.name} email={user.email} className="size-9 text-xs" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{user.name || user.email}</div>
                {user.name && <div className="truncate text-[0.8125rem] text-muted-foreground">{user.email}</div>}
              </div>
              <span className="h-5 shrink-0 rounded-sm border border-border bg-secondary px-1.5 text-[0.6875rem] leading-[1.125rem] text-muted-foreground">
                {user.role === "admin" ? "Admin" : "User"}
              </span>
            </div>
            <p className="text-[0.8125rem] text-muted-foreground">
              {user.role === "admin"
                ? <>Names, roles and new accounts are in <a href="/settings/users" onClick={go("/settings/users")} className="text-info hover:underline">Users</a>.</>
                : "An admin manages your name, your role and your account."}
            </p>
          </section>

          <Section title="Password" description="Changing it signs you out in every other browser. This one stays signed in.">
            <PasswordForm />
            <p className="text-[0.8125rem] text-muted-foreground">
              Your API tokens keep working after you change your password: they don't use it. To cut one off, revoke it
              in <a href="/settings/tokens" onClick={go("/settings/tokens")} className="text-info hover:underline">API tokens</a>.
            </p>
          </Section>
        </div>
      </div>
    </>
  );
}

function PasswordForm() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setDone(false);
    setError(null);
    if (next !== again) {
      setError("The new passwords don't match.");
      return;
    }
    setBusy(true);
    try {
      await api("PUT", "/api/auth/password", { current_password: current, new_password: next });
      setCurrent("");
      setNext("");
      setAgain("");
      setDone(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not change the password");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} aria-label="Change password" className="flex flex-col gap-4 rounded-md p-4 shadow-edge">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="current-password">Current password</Label>
        <Input id="current-password" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="new-password">New password</Label>
          <Input id="new-password" type="password" autoComplete="new-password" required value={next} onChange={(e) => setNext(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="repeat-password">Repeat the new password</Label>
          <Input id="repeat-password" type="password" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} />
        </div>
      </div>
      <p className="text-[0.8125rem] text-muted-foreground">8 to 72 characters. Letters outside A–Z, like é, count as two or more.</p>
      {error && <p role="alert" className="text-[0.8125rem] text-destructive">{error}</p>}
      {done && <p role="status" className="text-[0.8125rem] text-success">Password changed. Your other sessions are signed out; your API tokens still work.</p>}
      <div className="flex justify-end">
        <Button type="submit" size="sm" disabled={busy}>{busy ? "Changing…" : "Change password"}</Button>
      </div>
    </form>
  );
}
