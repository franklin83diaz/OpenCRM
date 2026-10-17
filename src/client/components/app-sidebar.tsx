import { useEffect, useState, type MouseEvent } from "react";
import { Activity, BookUser, Building2, CircleUserRound, DollarSign, KeyRound, Layers, LogOut, PanelLeft, User, UsersRound, type LucideIcon } from "lucide-react";
import { useAuth } from "@/auth";
import { useCrm } from "@/context";
import { UserAvatar } from "@/components/shared";
import { cn } from "@/lib/utils";
import type { Route } from "@/hooks/use-router";

// The app's sidebar: the brand row (it opens the home screen, the contacts),
// the records, then Settings, then who is signed in and "Sign out". A record
// type owns a colour: its tile is the type's hue, and the same hue follows it
// onto its page header and its chips inside other records. 275px wide on a
// desktop, where it folds to icons on request; always icons on a narrow screen.

type TileColor = "red" | "orange" | "amber" | "green" | "sky" | "blue" | "violet" | "pink";

const TILE: Record<TileColor, string> = {
  red: "bg-tile-red",
  orange: "bg-tile-orange",
  amber: "bg-tile-amber",
  green: "bg-tile-green",
  sky: "bg-tile-sky",
  blue: "bg-tile-blue",
  violet: "bg-tile-violet",
  pink: "bg-tile-pink",
};

interface NavItem {
  id: string;
  label: string;
  href: string;
  icon: LucideIcon;
  color: TileColor;
  adminOnly?: boolean;
}

const RECORDS: NavItem[] = [
  { id: "contacts", label: "Contacts", href: "/contacts", icon: User, color: "blue" },
  { id: "companies", label: "Companies", href: "/companies", icon: Building2, color: "violet" },
  { id: "deals", label: "Deals", href: "/deals", icon: DollarSign, color: "green" },
  { id: "customers", label: "Customers", href: "/customers", icon: Activity, color: "orange" },
];

const SETTINGS: NavItem[] = [
  { id: "properties", label: "Attributes", href: "/settings/properties", icon: Layers, color: "green" },
  { id: "account", label: "Account", href: "/settings/account", icon: CircleUserRound, color: "sky" },
  { id: "tokens", label: "API tokens", href: "/settings/tokens", icon: KeyRound, color: "amber" },
  { id: "users", label: "Users", href: "/settings/users", icon: UsersRound, color: "pink", adminOnly: true },
];

/** The nav item a route belongs to: a record's own page lights its list. */
function activeFor(route: Route): string {
  if (route.name === "contact") return "contacts";
  if (route.name === "company") return "companies";
  if (route.name === "deal") return "deals";
  return route.name;
}

/** Whether the screen is wide enough for the full sidebar (Tailwind's md). */
function useWide() {
  const query = "(min-width: 768px)";
  const [wide, setWide] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const onChange = () => setWide(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return wide;
}

/** A plain click goes through the router; cmd/ctrl/shift-click and the middle button keep the link's own behaviour. */
function plainClick(e: MouseEvent) {
  return !(e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0);
}

const row = "flex h-7 w-full items-center gap-1.5 rounded-[9px] text-sm font-medium tracking-[-0.01em] text-sidebar-foreground transition-colors hover:bg-black/[0.03] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring dark:hover:bg-white/[0.04] [[data-agent]_&]:h-10";
const activeRow = "bg-black/[0.04] hover:bg-black/[0.04] dark:bg-white/[0.06] dark:hover:bg-white/[0.06]";
const toggleButton = "inline-flex size-7 shrink-0 items-center justify-center rounded-[0.5rem] text-muted-foreground hover:bg-border/60 hover:text-foreground";

export function AppSidebar({ route, navigate }: { route: Route; navigate: (to: string) => void }) {
  const { user, signOut } = useAuth();
  const { stats, setError } = useCrm();
  const wide = useWide();
  const [collapsed, setCollapsed] = useState(false);
  const rail = collapsed || !wide;
  const active = activeFor(route);
  const counts: Record<string, number> = { contacts: stats.contacts, companies: stats.companies, deals: stats.deals, customers: stats.customers };
  const settings = SETTINGS.filter((item) => !item.adminOnly || user.role === "admin");

  const link = (item: NavItem) => {
    const on = item.id === active;
    const count = counts[item.id];
    return (
      <a
        key={item.id}
        href={item.href}
        aria-current={on ? "page" : undefined}
        aria-label={rail ? item.label : undefined}
        title={rail ? item.label : undefined}
        onClick={(e) => {
          if (!plainClick(e)) return;
          e.preventDefault();
          navigate(item.href);
        }}
        className={cn(row, rail ? "justify-center" : "pl-2 pr-4", on && activeRow)}
      >
        <span aria-hidden className={cn("inline-flex size-4 shrink-0 items-center justify-center rounded-[30%] border border-black/[0.06] text-white", TILE[item.color])}>
          <item.icon className="size-[11px]" strokeWidth={2.5} />
        </span>
        {!rail && <span className="min-w-0 flex-1 truncate">{item.label}</span>}
        {!rail && !!count && <span className="tabular text-xs text-muted-foreground">{count}</span>}
      </a>
    );
  };

  // signOut resolves when the session had already ended (the login page is up
  // by then); it fails only while the CRM is still on screen to say why.
  const doSignOut = () => {
    signOut().catch((e) => setError(e instanceof Error ? e.message : "Could not sign out"));
  };
  const who = user.name ? `${user.name} · ${user.email}` : user.email;

  return (
    <aside
      aria-label="Sidebar"
      className={cn("flex shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground", rail ? "w-14" : "w-[275px]")}
    >
      {/* Same height and rule as the page toolbar, so the line runs across the app. */}
      <div className={cn("flex h-14 shrink-0 items-center border-b border-sidebar-border", rail ? "justify-center" : "gap-2 pl-4 pr-2")}>
        {rail && collapsed ? (
          <button type="button" onClick={() => setCollapsed(false)} aria-label="Expand sidebar" title="Expand sidebar" className={toggleButton}>
            <PanelLeft className="size-4" />
          </button>
        ) : (
          <a
            href="/"
            aria-label={rail ? "OpenCRM home" : undefined}
            title={rail ? "OpenCRM" : undefined}
            onClick={(e) => {
              if (!plainClick(e)) return;
              e.preventDefault();
              navigate("/");
            }}
            className={cn("flex min-w-0 items-center gap-2 text-[0.9375rem] font-semibold", rail ? "size-7 justify-center" : "flex-1")}
          >
            <BookUser className="size-4 shrink-0 text-accent-brand" aria-hidden />
            {!rail && <span className="truncate">OpenCRM</span>}
          </a>
        )}
        {!rail && (
          <button type="button" onClick={() => setCollapsed(true)} aria-label="Collapse sidebar" title="Collapse sidebar" className={toggleButton}>
            <PanelLeft className="size-4" />
          </button>
        )}
      </div>

      <nav aria-label="Primary" className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-2">
        <div className="flex flex-col gap-px">{RECORDS.map(link)}</div>
        <div className="flex flex-col gap-px">
          {rail ? <div className="mx-2 mb-1 border-t border-sidebar-border" /> : <p className="mb-0.5 px-2 text-[0.8125rem] font-medium text-muted-foreground">Settings</p>}
          {settings.map(link)}
        </div>
      </nav>

      <div className="flex shrink-0 flex-col gap-1 border-t border-sidebar-border p-2">
        <div className={cn("flex items-center gap-2", rail ? "justify-center py-1" : "px-2 py-1")} title={rail ? who : undefined}>
          <UserAvatar name={user.name} email={user.email} className="size-6 text-[0.625rem]" />
          {!rail && (
            <div className="min-w-0 flex-1 leading-tight">
              <p className="truncate text-sm font-medium">{user.name || user.email}</p>
              {user.name && <p className="truncate text-xs text-muted-foreground">{user.email}</p>}
            </div>
          )}
        </div>
        <button
          type="button"
          onClick={doSignOut}
          aria-label="Sign out"
          title={rail ? "Sign out" : undefined}
          className={cn(row, rail ? "justify-center" : "pl-2 pr-4")}
        >
          <LogOut className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          {!rail && <span>Sign out</span>}
        </button>
      </div>
    </aside>
  );
}
