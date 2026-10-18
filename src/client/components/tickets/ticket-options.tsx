import { cn, colorClasses, type ColorToken } from "@/lib/utils";
import type { TicketPriority, TicketStatus } from "@/types";

/** Where a ticket stands, in order; a new ticket is open. Resolved and closed
 *  are done. Each has the colour of what it means: new (info), being worked
 *  (warning), waiting on the customer (a category hue), fixed (success),
 *  closed (neutral). */
export const TICKET_STATUSES: { value: TicketStatus; label: string; tone: ColorToken }[] = [
  { value: "open", label: "Open", tone: "info" },
  { value: "in_progress", label: "In progress", tone: "warning" },
  { value: "waiting", label: "Waiting", tone: "c4" },
  { value: "resolved", label: "Resolved", tone: "success" },
  { value: "closed", label: "Closed", tone: "slate" },
];

/** How urgent a ticket is, lowest first; a new ticket is medium. */
export const TICKET_PRIORITIES: { value: TicketPriority; label: string; tone: ColorToken }[] = [
  { value: "low", label: "Low", tone: "slate" },
  { value: "medium", label: "Medium", tone: "info" },
  { value: "high", label: "High", tone: "warning" },
  { value: "urgent", label: "Urgent", tone: "danger" },
];

/** Whether a ticket is done: resolved or closed. */
export const isDone = (status: string) => status === "resolved" || status === "closed";

export const ticketStatusLabel = (value: string) => TICKET_STATUSES.find((s) => s.value === value)?.label ?? value;
export const ticketPriorityLabel = (value: string) => TICKET_PRIORITIES.find((p) => p.value === value)?.label ?? value;

function Pill({ label, tone, className }: { label: string; tone: ColorToken; className?: string }) {
  const c = colorClasses(tone);
  return (
    <span className={cn("inline-block h-5 max-w-[12rem] truncate rounded-full px-2 align-middle text-[0.8125rem] font-medium leading-5", c.bg, c.text, className)}>
      {label}
    </span>
  );
}

/** A ticket's status as a pill in its own colour. */
export function TicketStatusBadge({ value, className }: { value: string; className?: string }) {
  if (!value) return <span className="text-muted-foreground">—</span>;
  const s = TICKET_STATUSES.find((x) => x.value === value);
  return <Pill label={s?.label ?? value} tone={s?.tone ?? "slate"} className={className} />;
}

/** A ticket's priority as a pill in its own colour. */
export function TicketPriorityBadge({ value, className }: { value: string; className?: string }) {
  if (!value) return <span className="text-muted-foreground">—</span>;
  const p = TICKET_PRIORITIES.find((x) => x.value === value);
  return <Pill label={p?.label ?? value} tone={p?.tone ?? "slate"} className={className} />;
}

/** A due date's state on a ticket not done: past it, due today, or later. */
export function dueState(due: string | null | undefined, status: string): "overdue" | "today" | null {
  if (!due || isDone(status)) return null;
  const today = new Date();
  const day = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, "0")}-${String(today.getDate()).padStart(2, "0")}`;
  return due < day ? "overdue" : due === day ? "today" : null;
}
