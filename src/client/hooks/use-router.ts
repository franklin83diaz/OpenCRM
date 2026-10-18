import { useState, useEffect, useCallback } from "react";

// A list route may carry `record`: the id open in the side panel beside the
// table (`/contacts?record=<id>`). The record's own path (`/contacts/<id>`) is
// the full page, so a deep link to a record still lands on the whole record.
// `view` is the list's named view (absent: its default view). `filters` opens
// a list pre-filtered (a filter tree, as the API takes it); the list reads it
// once and drops it from the URL (see useListView).
export type Route =
  | { name: "contacts"; record?: string; view?: string; filters?: string }
  | { name: "contact"; id: string }
  | { name: "companies"; record?: string; view?: string; filters?: string }
  | { name: "company"; id: string }
  | { name: "partners"; record?: string; view?: string; filters?: string }
  | { name: "partner"; id: string }
  | { name: "deals"; record?: string; view?: string }
  | { name: "deal"; id: string }
  | { name: "customers" }
  | { name: "customer-users"; record?: string; view?: string; filters?: string }
  | { name: "customer-user"; id: string }
  | { name: "tickets"; record?: string; view?: string; filters?: string; board?: boolean }
  | { name: "ticket"; id: string }
  | { name: "properties" }
  | { name: "account" }
  | { name: "tokens" }
  | { name: "users" }
  | { name: "not-found" };

function parse(pathname: string, search: string): Route {
  const q = new URLSearchParams(search);
  const record = q.get("record") || undefined;
  const filters = q.get("filters") ?? undefined;
  const view = q.get("view") || undefined;
  if (pathname === "/" || pathname === "/contacts") return { name: "contacts", record, view, filters };
  const m = pathname.match(/^\/contacts\/([^/]+)$/);
  if (m) return { name: "contact", id: decodeURIComponent(m[1]) };
  if (pathname === "/companies") return { name: "companies", record, view, filters };
  const cm = pathname.match(/^\/companies\/([^/]+)$/);
  if (cm) return { name: "company", id: decodeURIComponent(cm[1]) };
  if (pathname === "/partners") return { name: "partners", record, view, filters };
  const pm = pathname.match(/^\/partners\/([^/]+)$/);
  if (pm) return { name: "partner", id: decodeURIComponent(pm[1]) };
  if (pathname === "/deals") return { name: "deals", record, view };
  const dm = pathname.match(/^\/deals\/([^/]+)$/);
  if (dm) return { name: "deal", id: decodeURIComponent(dm[1]) };
  if (pathname === "/customers") return { name: "customers" };
  if (pathname === "/customer-users") return { name: "customer-users", record, view, filters };
  const um = pathname.match(/^\/customer-users\/([^/]+)$/);
  if (um) return { name: "customer-user", id: decodeURIComponent(um[1]) };
  // The tickets list and its board share their filters; /tickets/board is the board.
  if (pathname === "/tickets") return { name: "tickets", record, view, filters };
  if (pathname === "/tickets/board") return { name: "tickets", record, view, filters, board: true };
  const tm = pathname.match(/^\/tickets\/([^/]+)$/);
  if (tm) return { name: "ticket", id: decodeURIComponent(tm[1]) };
  if (pathname === "/settings/properties") return { name: "properties" };
  if (pathname === "/settings/account") return { name: "account" };
  if (pathname === "/settings/tokens") return { name: "tokens" };
  if (pathname === "/settings/users") return { name: "users" };
  return { name: "not-found" };
}

const current = () => window.location.pathname + window.location.search;

/** The current path with some query params set (or removed, with null); the rest kept. */
export function withQuery(changes: Record<string, string | null>): string {
  const q = new URLSearchParams(window.location.search);
  for (const [k, v] of Object.entries(changes)) {
    if (v === null) q.delete(k);
    else q.set(k, v);
  }
  const s = q.toString();
  return window.location.pathname + (s ? `?${s}` : "");
}

/** Navigate from anywhere, without the router's `navigate` in hand (a record
 *  chip deep in a table cell). The router hears it as a history change. */
export function go(to: string): void {
  if (to === current()) return;
  window.history.pushState(null, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export function useRouter() {
  const [path, setPath] = useState<string>(current);

  // `replace` swaps the entry instead of stacking one, so moving the panel
  // from record to record does not bury the list under a trail of history.
  const navigate = useCallback((to: string, opts?: { replace?: boolean }) => {
    if (to === current()) return;
    if (opts?.replace) window.history.replaceState(null, "", to);
    else window.history.pushState(null, "", to);
    setPath(current());
  }, []);

  useEffect(() => {
    const handler = () => setPath(current());
    window.addEventListener("popstate", handler);
    return () => window.removeEventListener("popstate", handler);
  }, []);

  const url = new URL(path, window.location.origin);
  return { path, route: parse(url.pathname, url.search), navigate };
}
