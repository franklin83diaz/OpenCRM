import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { useCrm } from "@/context";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { memberName } from "@/components/deals/owner-field";
import { TicketDialog } from "@/components/tickets/ticket-dialog";
import { TicketPriorityBadge, TicketStatusBadge, dueState, isDone } from "@/components/tickets/ticket-options";
import { RecordChip } from "@/lib/relations";
import { cn, formatDate } from "@/lib/utils";
import type { Ticket } from "@/types";

/** How many tickets a customer, or one of its users, has that aren't done; null while loading. */
export function useOpenTicketCount(scope: { companyId?: string; customerUserId?: string }): number | null {
  const { changes } = useCrm();
  const [count, setCount] = useState<number | null>(null);
  const q = scope.customerUserId ? `customer_user_id=${encodeURIComponent(scope.customerUserId)}` : `company_id=${encodeURIComponent(scope.companyId ?? "")}`;
  useEffect(() => {
    let alive = true;
    const filters = encodeURIComponent(JSON.stringify([{ field: "status", op: "is_not", value: ["resolved", "closed"] }]));
    api<{ total: number }>("GET", `/api/tickets?${q}&limit=1&filters=${filters}`)
      .then((r) => { if (alive) setCount(r.total); }, () => { if (alive) setCount(0); });
    return () => { alive = false; };
  }, [q, changes]);
  return count;
}

/**
 * A customer's tickets, or one customer user's: the open ones first, then the
 * done, each with where it stands; + opens a new one for them.
 * `companyId` is the customer (for a customer user, theirs); a company that
 * isn't a customer (`customer` false) takes no new ones.
 */
export function RecordTickets({ companyId, customerUserId, customer = true }: { companyId?: string | null; customerUserId?: string; customer?: boolean }) {
  const { changes, setError } = useCrm();
  const [tickets, setTickets] = useState<Ticket[] | null>(null);
  const [adding, setAdding] = useState(false);
  const q = customerUserId ? `customer_user_id=${encodeURIComponent(customerUserId)}` : `company_id=${encodeURIComponent(companyId ?? "")}`;
  useEffect(() => {
    let alive = true;
    api<{ tickets: Ticket[] }>("GET", `/api/tickets?${q}&limit=100&sort=created_at&order=desc`)
      .then((r) => { if (alive) setTickets(r.tickets); }, (e) => setError(e instanceof Error ? e.message : "Could not load the tickets"));
    return () => { alive = false; };
  }, [q, changes]); // eslint-disable-line react-hooks/exhaustive-deps

  const list = tickets ? [...tickets.filter((t) => !isDone(t.status)), ...tickets.filter((t) => isDone(t.status))] : null;
  const open = tickets?.filter((t) => !isDone(t.status)).length ?? 0;

  return (
    <section className="flex flex-col gap-3" aria-label="Tickets">
      <div className="flex h-9 items-center justify-between">
        <h2 className="inline-flex items-center gap-2 text-sm font-medium">
          Tickets <span className="rounded-xs bg-secondary px-1.5 text-xs tabular text-muted-foreground">{open} open</span>
        </h2>
        <Button variant="ghost" size="icon" aria-label="New ticket" title={customer && companyId ? "New ticket" : "Only a customer has tickets"} disabled={!companyId || !customer} onClick={() => setAdding(true)}>
          <Plus className="size-4" />
        </Button>
      </div>
      {list === null ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : list.length === 0 ? (
        <p className="text-sm text-faint">No tickets yet.</p>
      ) : (
        <ul className="flex flex-col rounded-md bg-card shadow-edge">
          {list.map((t) => {
            const due = dueState(t.due_date, t.status);
            const reporter = !customerUserId && t.customer_user_name;
            return (
              <li key={t.id} className={cn("flex flex-col gap-0.5 px-3.5 py-2 [&+li]:border-t [&+li]:border-border", isDone(t.status) && "opacity-70")}>
                <div className="flex min-h-7 items-center gap-2">
                  <span className="flex min-w-0 flex-1 overflow-hidden"><RecordChip entity="ticket" record={{ id: t.id, label: t.subject, domain: null }} /></span>
                  <TicketPriorityBadge value={t.priority} />
                  <TicketStatusBadge value={t.status} />
                </div>
                {(reporter || t.owner || t.due_date) && (
                  <p className="flex min-w-0 items-center gap-1.5 truncate text-[0.8125rem] text-muted-foreground">
                    {reporter && <span className="truncate">By {reporter}</span>}
                    {reporter && (t.owner || t.due_date) && <span aria-hidden>·</span>}
                    {t.owner && <span className="truncate">{memberName(t.owner)}</span>}
                    {t.owner && t.due_date && <span aria-hidden>·</span>}
                    {t.due_date && <span className={cn("shrink-0 tabular", due === "overdue" && "text-destructive", due === "today" && "text-warning")}>Due {formatDate(t.due_date)}</span>}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <TicketDialog open={adding} onOpenChange={setAdding} companyId={companyId} customerUserId={customerUserId} />
    </section>
  );
}
