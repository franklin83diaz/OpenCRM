import { useEffect, useState, type ReactNode } from "react";
import { Building2, CalendarClock, IdCard, LifeBuoy, UserCheck, type LucideIcon } from "lucide-react";
import { DndContext, DragOverlay, PointerSensor, pointerWithin, useDraggable, useDroppable, useSensor, useSensors, type DragEndEvent, type DragStartEvent } from "@dnd-kit/core";
import { useCrm } from "@/context";
import { api } from "@/api";
import { Avatar, EntityIcon, UserAvatar } from "@/components/shared";
import { Card } from "@/components/ui/card";
import { memberName } from "@/components/deals/owner-field";
import { TICKET_STATUSES, TicketPriorityBadge, dueState } from "@/components/tickets/ticket-options";
import { sanitize, type FilterNode } from "@/lib/filters";
import { colorClasses, cn, formatDate } from "@/lib/utils";
import type { Ticket, TicketStatus } from "@/types";

/** How many tickets a column shows; its count says how many there are. */
const COLUMN_LIMIT = 100;

type Columns = Record<TicketStatus, { tickets: Ticket[]; total: number }>;

/**
 * The tickets by status, one column each, most urgent first: the list's own
 * filters and search, so the two show the same tickets. A card dropped on
 * another column moves the ticket there. `openId` is the ticket open in the
 * side panel.
 */
export function TicketsBoard({ filters, search, openId, onOpen }: { filters: FilterNode[]; search: string; openId?: string; onOpen: (id: string) => void }) {
  const { updateTicket, changes, setError, isAgent } = useCrm();
  const [columns, setColumns] = useState<Columns | null>(null);
  const query = JSON.stringify(sanitize(filters));

  useEffect(() => {
    let alive = true;
    const tz = String(-new Date().getTimezoneOffset());
    Promise.all(TICKET_STATUSES.map(async (s) => {
      const p = new URLSearchParams({ limit: String(COLUMN_LIMIT), sort: "priority", order: "desc", tz });
      if (search) p.set("search", search);
      p.set("filters", JSON.stringify([...JSON.parse(query), { field: "status", op: "is", value: [s.value] }]));
      const r = await api<{ tickets: Ticket[]; total: number }>("GET", `/api/tickets?${p}`);
      return [s.value, { tickets: r.tickets, total: r.total }] as const;
    })).then(
      (cols) => { if (alive) setColumns(Object.fromEntries(cols) as Columns); },
      (e) => setError(e instanceof Error ? e.message : "Could not load the board"),
    );
    return () => { alive = false; };
  }, [query, search, changes]); // eslint-disable-line react-hooks/exhaustive-deps

  // A press becomes a drag only after 4px of movement, so a click still opens the ticket.
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }));
  const [dragging, setDragging] = useState<Ticket | null>(null);
  const all = columns ? Object.values(columns).flatMap((c) => c.tickets) : [];
  const onDragStart = (e: DragStartEvent) => setDragging(all.find((t) => t.id === e.active.id) ?? null);
  const onDragEnd = (e: DragEndEvent) => {
    setDragging(null);
    const ticket = all.find((t) => t.id === e.active.id);
    const to = e.over ? (String(e.over.id) as TicketStatus) : null;
    if (!ticket || !to || ticket.status === to || !columns) return;
    // The card moves at once; the refetch the write sets off settles it.
    setColumns({
      ...columns,
      [ticket.status]: { tickets: columns[ticket.status].tickets.filter((t) => t.id !== ticket.id), total: columns[ticket.status].total - 1 },
      [to]: { tickets: [{ ...ticket, status: to }, ...columns[to].tickets], total: columns[to].total + 1 },
    });
    updateTicket(ticket.id, { status: to }).catch((err) => setError(err instanceof Error ? err.message : "Could not move the ticket"));
  };

  if (!columns) return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;

  return (
    <div className="min-h-0 flex-1 overflow-x-auto">
      <DndContext sensors={sensors} collisionDetection={pointerWithin} onDragStart={onDragStart} onDragEnd={onDragEnd} onDragCancel={() => setDragging(null)}>
        <div className="flex h-full min-w-max gap-4 p-6">
          {TICKET_STATUSES.map((s) => {
            const col = columns[s.value];
            return (
              <StatusDrop key={s.value} status={s.value}>
                <div className="flex h-7 items-center gap-2">
                  <span className={cn("size-2 shrink-0 rounded-full", colorClasses(s.tone).dot)} />
                  <span className="truncate text-sm font-semibold">{s.label}</span>
                  <span className="rounded-sm bg-card px-1.5 text-xs tabular text-muted-foreground shadow-edge">{col.total}</span>
                </div>
                <div className="-mx-3 -mb-3 flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto px-3 pb-3">
                  {col.tickets.map((t) => <TicketCard key={t.id} ticket={t} open={t.id === openId} draggable={!isAgent} onOpen={() => onOpen(t.id)} />)}
                  {col.total > col.tickets.length && (
                    <p className="py-1 text-center text-xs text-muted-foreground">{col.total - col.tickets.length} more: narrow the filters to see them</p>
                  )}
                </div>
              </StatusDrop>
            );
          })}
        </div>
        <DragOverlay dropAnimation={null}>
          {dragging && (
            <Card className="flex w-full -rotate-2 cursor-grabbing flex-col p-3 shadow-[var(--shadow-popover)]">
              <TicketTitle ticket={dragging} />
              <TicketFields ticket={dragging} />
            </Card>
          )}
        </DragOverlay>
      </DndContext>
    </div>
  );
}

/** A status's column: a tray holding its tickets, and where a dragged ticket is dropped. */
function StatusDrop({ status, children }: { status: string; children: ReactNode }) {
  const { setNodeRef, isOver } = useDroppable({ id: status });
  return (
    <div ref={setNodeRef} className={cn("flex min-h-0 w-72 shrink-0 flex-col gap-2 rounded-xl bg-secondary p-3 transition-shadow", isOver && "ring-2 ring-inset ring-ring/15")}>
      {children}
    </div>
  );
}

function TicketCard({ ticket: t, open, draggable, onOpen }: { ticket: Ticket; open: boolean; draggable: boolean; onOpen: () => void }) {
  const { setNodeRef, listeners, isDragging } = useDraggable({ id: t.id, disabled: !draggable });
  return (
    // The subject is the card's link to the ticket; its ::after stretches over the card.
    <Card ref={setNodeRef} {...listeners}
      className={cn("group/card relative flex flex-col p-3 transition-colors hover:bg-secondary/50", open && "ring-1 ring-inset ring-ring/30", isDragging && "opacity-40")}>
      <button type="button" onClick={onOpen} aria-current={open || undefined}
        className="min-w-0 text-left outline-none after:absolute after:inset-0 after:rounded-[inherit] after:content-[''] focus-visible:after:ring-2 focus-visible:after:ring-ring">
        <TicketTitle ticket={t} />
      </button>
      <TicketFields ticket={t} />
    </Card>
  );
}

function TicketTitle({ ticket }: { ticket: Ticket }) {
  return (
    <span className="flex min-h-7 min-w-0 items-center gap-2">
      <LifeBuoy className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      <span className="truncate text-sm font-medium leading-6 underline decoration-border underline-offset-2">{ticket.subject}</span>
    </span>
  );
}

/** A ticket's fields, one row each, a faint prompt while one is empty. */
function TicketFields({ ticket: t }: { ticket: Ticket }) {
  const [first, ...rest] = (t.customer_user_name ?? "").split(" ");
  const due = dueState(t.due_date, t.status);
  return (
    <dl className="flex flex-col text-sm">
      <div className="flex h-7 items-center"><dt className="sr-only">Priority</dt><dd><TicketPriorityBadge value={t.priority} /></dd></div>
      <FieldRow icon={Building2} label="Customer">
        {t.company_name ? <><EntityIcon name={t.company_name} domain={t.company_domain} className="size-5" /><span className="truncate">{t.company_name}</span></> : null}
      </FieldRow>
      <FieldRow icon={IdCard} label="Reported by" placeholder="Nobody in particular">
        {t.customer_user_name ? <><Avatar firstName={first} lastName={rest.join(" ")} className="size-5 text-[0.5625rem]" /><span className="truncate">{t.customer_user_name}</span></> : null}
      </FieldRow>
      <FieldRow icon={UserCheck} label="Assignee" placeholder="Unassigned">
        {t.owner ? <><UserAvatar name={t.owner.name} email={t.owner.email} className="size-5 text-[0.5625rem]" /><span className="truncate">{memberName(t.owner)}</span></> : null}
      </FieldRow>
      <FieldRow icon={CalendarClock} label="Due date" placeholder="No due date">
        {t.due_date ? <span className={cn("tabular", due === "overdue" && "font-medium text-destructive", due === "today" && "font-medium text-warning")}>{formatDate(t.due_date)}</span> : null}
      </FieldRow>
    </dl>
  );
}

function FieldRow({ icon: Icon, label, placeholder = label, children }: { icon: LucideIcon; label: string; placeholder?: string; children: ReactNode }) {
  return (
    <div className="flex h-7 min-w-0 items-center gap-2">
      <dt className="sr-only">{label}</dt>
      <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      <dd className="flex min-w-0 items-center gap-1.5">
        {children ?? (
          <>
            <span className="text-faint" aria-hidden>{placeholder}</span>
            <span className="sr-only">Not set</span>
          </>
        )}
      </dd>
    </div>
  );
}
