import { useCallback, useEffect, useMemo, useState } from "react";
import { Building2, Handshake, IdCard, Network, Users } from "lucide-react";
import { api, ApiError, onUnauthorized } from "./api";
import { AuthContext, useAuth, type AuthValue } from "./auth";
import { useCrmState } from "./hooks/use-crm";
import { CrmContext } from "./context";
import { useRouter, withQuery } from "./hooks/use-router";
import { ErrorBanner } from "./components/error-banner";
import { TooltipProvider } from "./components/ui/tooltip";
import { Button } from "./components/ui/button";
import { AppSidebar } from "./components/app-sidebar";
import { LoginPage } from "./components/auth/login-page";
import { ContactsPage } from "./components/contacts/contacts-page";
import { ContactDetail } from "./components/contacts/contact-detail";
import { CompaniesPage } from "./components/companies/companies-page";
import { CompanyDetail } from "./components/companies/company-detail";
import { PartnersPage } from "./components/partners/partners-page";
import { PartnerDetail } from "./components/partners/partner-detail";
import { CustomerUsersPage } from "./components/customer-users/customer-users-page";
import { CustomerUserDetail } from "./components/customer-users/customer-user-detail";
import { DealsBoard } from "./components/deals/deals-board";
import { DealsProgressPage } from "./components/deals/deals-progress-page";
import { DealDetail } from "./components/deals/deal-detail";
import { PropertiesPage } from "./components/properties/properties-page";
import { CustomersPage } from "./components/customers/customers-page";
import { AccountPage } from "./components/settings/account-page";
import { TokensPage } from "./components/settings/tokens-page";
import { UsersPage } from "./components/settings/users-page";
import { RecordPanel, RecordPanelHeader } from "./components/record-page";
import type { User } from "./types";

type Auth =
  | { status: "checking" }
  | { status: "signed-out"; expired?: boolean }
  | { status: "unreachable"; message: string }
  | { status: "signed-in"; user: User };

/**
 * Who is here decides what renders: the login page, or the CRM. On load the
 * session cookie is checked with GET /api/auth/me. Afterwards a 401 from any
 * call (the session expired or was ended, the account was disabled) comes
 * back here through api.ts and shows the login page; nothing else retries.
 */
export function App() {
  const [auth, setAuth] = useState<Auth>({ status: "checking" });

  useEffect(() => onUnauthorized(() => {
    setAuth((a) => (a.status === "signed-in" ? { status: "signed-out", expired: true } : a.status === "signed-out" ? a : { status: "signed-out" }));
  }), []);

  const check = useCallback(async () => {
    setAuth({ status: "checking" });
    try {
      const { user } = await api<{ user: User }>("GET", "/api/auth/me");
      setAuth({ status: "signed-in", user });
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setAuth({ status: "signed-out" });
      else setAuth({ status: "unreachable", message: e instanceof Error ? e.message : String(e) });
    }
  }, []);
  useEffect(() => { void check(); }, [check]);

  const signOut = useCallback(async () => {
    try {
      await api("POST", "/api/auth/logout");
    } catch (e) {
      // A session that had already ended (expired, or ended elsewhere) is a
      // sign-out done: the 401 already showed the login page. Anything else
      // didn't sign out, and the sidebar says so.
      if (!(e instanceof ApiError && e.status === 401)) throw e;
    }
    // The next person to sign in starts on the home screen, not on this one's page.
    window.history.replaceState(null, "", "/");
    setAuth({ status: "signed-out" });
  }, []);
  const setUser = useCallback((user: User) => setAuth({ status: "signed-in", user }), []);

  const user = auth.status === "signed-in" ? auth.user : null;
  const value = useMemo<AuthValue | null>(() => (user ? { user, setUser, signOut } : null), [user, setUser, signOut]);

  if (auth.status === "checking") {
    return <div className="flex h-dvh items-center justify-center bg-background text-sm text-muted-foreground">Loading…</div>;
  }
  if (auth.status === "unreachable") {
    return (
      <div className="flex h-dvh flex-col items-center justify-center gap-3 bg-background p-6 text-center">
        <p className="text-sm text-muted-foreground">The CRM didn't answer: {auth.message}</p>
        <Button size="sm" variant="outline" onClick={() => void check()}>Try again</Button>
      </div>
    );
  }
  if (auth.status === "signed-out" || !value) {
    return <LoginPage expired={auth.status === "signed-out" && auth.expired} onSignedIn={setUser} />;
  }
  return (
    <AuthContext.Provider value={value}>
      {/* A new person gets a fresh CRM state: nothing of the last one's lists or panels. */}
      <Crm key={value.user.id} />
    </AuthContext.Provider>
  );
}

function Crm() {
  const isAgent = document.documentElement.hasAttribute("data-agent");
  const state = useCrmState(isAgent);
  const { route, navigate } = useRouter();
  const { user } = useAuth();
  // A list opens its records in a side panel; the record's own path is the full page.
  const openRecord = route.name === "contacts" || route.name === "companies" || route.name === "partners" || route.name === "customer-users" || route.name === "deals" ? route.record : undefined;

  return (
    <CrmContext.Provider value={state}>
    <TooltipProvider delayDuration={200}>
      <div className="flex h-dvh min-h-0 overflow-hidden bg-background text-foreground">
        <AppSidebar route={route} navigate={navigate} />
        <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
          {state.loading ? (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>
          ) : (
            <>
              {route.name === "contacts" && (
                <div className="flex min-h-0 flex-1">
                  <ContactsPage navigate={navigate} openId={openRecord} viewParam={route.view} filtersParam={route.filters} />
                  {openRecord && (
                    <RecordPanel label="Contact">
                      <RecordPanelHeader icon={Users} label="Contact" onExpand={() => navigate(`/contacts/${encodeURIComponent(openRecord)}`)} onClose={() => navigate(withQuery({ record: null }))} />
                      <ContactDetail key={openRecord} id={openRecord} navigate={navigate} panel />
                    </RecordPanel>
                  )}
                </div>
              )}
              {route.name === "contact" && <ContactDetail id={route.id} navigate={navigate} />}
              {route.name === "companies" && (
                <div className="flex min-h-0 flex-1">
                  <CompaniesPage navigate={navigate} openId={openRecord} viewParam={route.view} filtersParam={route.filters} />
                  {openRecord && (
                    <RecordPanel label="Company">
                      <RecordPanelHeader icon={Building2} label="Company" onExpand={() => navigate(`/companies/${encodeURIComponent(openRecord)}`)} onClose={() => navigate(withQuery({ record: null }))} />
                      <CompanyDetail key={openRecord} id={openRecord} navigate={navigate} panel />
                    </RecordPanel>
                  )}
                </div>
              )}
              {route.name === "company" && <CompanyDetail id={route.id} navigate={navigate} />}
              {route.name === "partners" && (
                <div className="flex min-h-0 flex-1">
                  <PartnersPage navigate={navigate} openId={openRecord} viewParam={route.view} filtersParam={route.filters} />
                  {openRecord && (
                    <RecordPanel label="Partner">
                      <RecordPanelHeader icon={Network} label="Partner" onExpand={() => navigate(`/partners/${encodeURIComponent(openRecord)}`)} onClose={() => navigate(withQuery({ record: null }))} />
                      <PartnerDetail key={openRecord} id={openRecord} navigate={navigate} panel />
                    </RecordPanel>
                  )}
                </div>
              )}
              {route.name === "partner" && <PartnerDetail id={route.id} navigate={navigate} />}
              {route.name === "deals" && (
                <div className="flex min-h-0 flex-1">
                  {route.view === "next-steps"
                    ? <DealsProgressPage navigate={navigate} openId={openRecord} />
                    : <DealsBoard navigate={navigate} openId={openRecord} />}
                  {openRecord && (
                    <RecordPanel label="Deal">
                      <RecordPanelHeader icon={Handshake} label="Deal" onExpand={() => navigate(`/deals/${encodeURIComponent(openRecord)}`)} onClose={() => navigate(withQuery({ record: null }))} />
                      <DealDetail key={openRecord} id={openRecord} navigate={navigate} panel />
                    </RecordPanel>
                  )}
                </div>
              )}
              {route.name === "deal" && <DealDetail id={route.id} navigate={navigate} />}
              {route.name === "customers" && <CustomersPage navigate={navigate} />}
              {route.name === "customer-users" && (
                <div className="flex min-h-0 flex-1">
                  <CustomerUsersPage navigate={navigate} openId={openRecord} viewParam={route.view} filtersParam={route.filters} />
                  {openRecord && (
                    <RecordPanel label="Customer user">
                      <RecordPanelHeader icon={IdCard} label="Customer user" onExpand={() => navigate(`/customer-users/${encodeURIComponent(openRecord)}`)} onClose={() => navigate(withQuery({ record: null }))} />
                      <CustomerUserDetail key={openRecord} id={openRecord} navigate={navigate} panel />
                    </RecordPanel>
                  )}
                </div>
              )}
              {route.name === "customer-user" && <CustomerUserDetail id={route.id} navigate={navigate} />}
              {route.name === "properties" && <PropertiesPage />}
              {route.name === "account" && <AccountPage navigate={navigate} />}
              {route.name === "tokens" && <TokensPage />}
              {route.name === "users" && (user.role === "admin"
                ? <UsersPage navigate={navigate} />
                : <Missing title="Admins only" text="Only an admin can manage accounts." navigate={navigate} />)}
              {route.name === "not-found" && <Missing title="Not found" text="That page doesn't exist." navigate={navigate} />}
            </>
          )}
        </main>
        <ErrorBanner />
      </div>
    </TooltipProvider>
    </CrmContext.Provider>
  );
}

function Missing({ title, text, navigate }: { title: string; text: string; navigate: (to: string) => void }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 p-12 text-center">
      <h1 className="text-xl font-bold tracking-tight">{title}</h1>
      <p className="text-sm text-muted-foreground">{text}</p>
      <button className="text-sm text-[var(--ring)] hover:underline" onClick={() => navigate("/contacts")}>
        Back to contacts
      </button>
    </div>
  );
}
