import { useState, type FormEvent } from "react";
import { BookUser } from "lucide-react";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { User } from "@/types";

/**
 * The only screen a signed-out visitor sees. Accounts are made by an admin
 * (Settings → Users), so there is no sign-up. The API's own words explain a
 * refusal ("Wrong email or password", "Too many attempts…").
 */
export function LoginPage({ expired, onSignedIn }: { expired?: boolean; onSignedIn: (user: User) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { user } = await api<{ user: User }>("POST", "/api/auth/login", { email, password });
      onSignedIn(user);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not sign in");
      setBusy(false);
    }
  };

  return (
    <main className="flex min-h-dvh items-center justify-center bg-background p-6 text-foreground">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex items-center gap-2 text-[0.9375rem] font-semibold">
          <BookUser className="size-4 text-accent-brand" aria-hidden /> OpenCRM
        </div>
        <form onSubmit={submit} aria-label="Sign in" className="flex flex-col gap-4 rounded-md bg-card p-6 shadow-edge">
          <div className="flex flex-col gap-1">
            <h1 className="text-[1.375rem] font-semibold tracking-[-0.01em]">Sign in</h1>
            <p className="text-[0.8125rem] text-muted-foreground">With the email and password your admin set up for you.</p>
          </div>
          {expired && !error && (
            <p className="rounded-md bg-info-tint px-3 py-2 text-[0.8125rem] text-info">Your session ended. Sign in again to carry on.</p>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="login-email">Email</Label>
            <Input id="login-email" type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="login-password">Password</Label>
            <Input id="login-password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          {error && <p role="alert" className="text-[0.8125rem] text-destructive">{error}</p>}
          <Button type="submit" size="lg" className="w-full" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </div>
    </main>
  );
}
