import { useEffect, useRef, useState } from "react";
import { ArrowLeft, LifeBuoy, LayoutGrid, Activity as ActivityIcon, Building2, IdCard, StickyNote, Calendar, Clock, CircleDot, Flag, UserCheck, CalendarClock, AlignLeft } from "lucide-react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { Textarea } from "@/components/ui/textarea";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { ActivityFeed, NotesSection, formatTimestamp } from "@/components/record-feed";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { OptionField } from "@/components/value-fields";
import { OwnerField, memberName } from "@/components/deals/owner-field";
import { ScopedRecordField } from "@/components/scoped-record-field";
import { customerUsersOf } from "@/components/tickets/ticket-dialog";
import { TICKET_PRIORITIES, TICKET_STATUSES, TicketPriorityBadge, TicketStatusBadge, dueState } from "@/components/tickets/ticket-options";
import { RecordChip, RelationInput } from "@/lib/relations";
import { cn, formatDate } from "@/lib/utils";
import type { Activity, RelationRecord, Ticket } from "@/types";

// The ticket record page: same anatomy as the other records (DESIGN.md →
// Record pages): what it is about, the customer and who reported it, where
// it stands. Every value is the control; there is no edit mode. `panel`
// renders it in the side panel beside the tickets list or board.
export function TicketDetail({ id, navigate, panel = false }: { id: string; navigate: (to: string) => void; panel?: boolean }) {
  const { fetchTicket, updateTicket, fetchActivities, setError, customFields, changes } = useCrm();
  const relationDefs = customFields.filter((d) => d.entity_type === "ticket" && d.field_type === "relation");
  const [ticket, setTicket] = useState<Ticket | null | undefined>(undefined);
  const [activities, setActivities] = useState<Activity[]>([]);
  // The full page shows one tab at a time; the panel, everything in one column.
  const [tab, setTab] = useState("overview");
  const [noteOpen, setNoteOpen] = useState(false);
  const shows = (key: string) => panel || tab === key;

  const saveField = async (patch: Partial<Ticket>) => {
    if (!ticket) return;
    try {
      await updateTicket(ticket.id, patch);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const reload = async () => {
    const fresh = await fetchTicket(id);
    if (fresh) setTicket(fresh);
    setActivities(await fetchActivities("ticket", id));
  };
  // Any record write (here, on the board, its customer renamed) re-reads this one.
  const seen = useRef(changes);
  useEffect(() => {
    if (seen.current === changes) return;
    seen.current = changes;
    void reload();
  }, [changes]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let alive = true;
    setTicket(undefined);
    fetchTicket(id).then((t) => { if (alive) setTicket(t); });
    fetchActivities("ticket", id).then((a) => { if (alive) setActivities(a); });
    return () => { alive = false; };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  if (ticket === undefined) {
    return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;
  }
  if (ticket === null) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-12 text-center">
        <p className="text-sm text-muted-foreground">Ticket not found.</p>
        <Button size="sm" variant="outline" onClick={() => navigate("/tickets")}><ArrowLeft className="size-4" /> Back to tickets</Button>
      </div>
    );
  }

  const company: RelationRecord | undefined = ticket.company_id && ticket.company_name != null
    ? { id: ticket.company_id, label: ticket.company_name, domain: ticket.company_domain ?? null }
    : undefined;
  const reporter: RelationRecord | undefined = ticket.customer_user_id && ticket.customer_user_name != null
    ? { id: ticket.customer_user_id, label: ticket.customer_user_name, domain: null }
    : undefined;
  const due = dueState(ticket.due_date, ticket.status);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {!panel && <RecordTopBar onClose={() => navigate("/tickets")} crumb="Tickets" />}

      <div className={cn("flex min-h-0 flex-1", panel && "flex-col overflow-y-auto")}>
        <aside className={cn("flex shrink-0 flex-col", !panel && "w-[31.25rem] overflow-y-auto border-r border-border")}>
          <div className="flex items-center gap-3 px-4 pt-4">
            <span className="inline-flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary text-muted-foreground"><LifeBuoy className="size-4" /></span>
            <div className="min-w-0 flex-1">
              <InlineField value={ticket.subject} placeholder="Subject" onSave={(v) => { if (v) return saveField({ subject: v }); }} className="h-8 w-auto px-1.5 text-base font-semibold" />
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2 px-4 pt-2 pb-4">
            <TicketStatusBadge value={ticket.status} />
            <TicketPriorityBadge value={ticket.priority} />
            {due && <span className={cn("text-[0.8125rem] font-medium", due === "overdue" ? "text-destructive" : "text-warning")}>{due === "overdue" ? "Overdue" : "Due today"}</span>}
          </div>

          <DetailsSection title="Record details">
            <dl className="flex flex-col">
              <Attr icon={CircleDot} label="Status">
                <OptionField options={TICKET_STATUSES} label="status" value={ticket.status} onSave={(v) => saveField({ status: v as Ticket["status"] })} badge={(v) => <TicketStatusBadge value={v} />} />
              </Attr>
              <Attr icon={Flag} label="Priority">
                <OptionField options={TICKET_PRIORITIES} label="priority" value={ticket.priority} onSave={(v) => saveField({ priority: v as Ticket["priority"] })} badge={(v) => <TicketPriorityBadge value={v} />} />
              </Attr>
              <Attr icon={Building2} label="Customer">
                {/* Moving it to another customer drops a reporter who isn't one of theirs (the server does). */}
                <RelationInput link customers entity="company" label="customer" value={ticket.company_id} known={company} placeholder="Set customer…" emptyLabel="No company"
                  onChange={(v) => { if (v) void saveField({ company_id: v }); }} />
              </Attr>
              <Attr icon={IdCard} label="Reported by">
                <ScopedRecordField link entity="customer_user" label="customer user" value={ticket.customer_user_id} known={reporter}
                  placeholder="Set customer user…" emptyLabel="Nobody in particular" disabled={!ticket.company_id} hint="No customer"
                  load={() => customerUsersOf(ticket.company_id!)} onChange={(v) => saveField({ customer_user_id: v })} />
              </Attr>
              <Attr icon={UserCheck} label="Assignee">
                <OwnerField value={ticket.owner_id} owner={ticket.owner} onChange={(v) => saveField({ owner_id: v })} />
              </Attr>
              <Attr icon={CalendarClock} label="Due date">
                <InlineField type="date" value={ticket.due_date ?? ""} placeholder="Set due date…" onSave={(v) => saveField({ due_date: v || null })}
                  render={(v) => <span className={cn("tabular", due === "overdue" && "text-destructive")}>{formatDate(v)}</span>} />
              </Attr>
              <RelationAttrs defs={relationDefs} row={ticket} onSave={(key, v) => saveField({ [key]: v } as Partial<Ticket>)} />
            </dl>
          </DetailsSection>

          <RelationSections defs={relationDefs} row={ticket} />

          <DetailsSection title="System">
            <dl className="flex flex-col">
              <Attr icon={Calendar} label="Created"><span className="px-2 text-sm">{formatTimestamp(ticket.created_at)}</span></Attr>
              <Attr icon={Clock} label="Updated"><span className="px-2 text-sm">{formatTimestamp(ticket.updated_at)}</span></Attr>
            </dl>
          </DetailsSection>
        </aside>

        <main className={cn("flex min-w-0 flex-col", panel ? "shrink-0 border-t border-border" : "flex-1 overflow-y-auto")}>
          {!panel && <RecordTabs active={tab} onChange={setTab} tabs={[
            { key: "overview", label: "Overview", icon: LayoutGrid },
            { key: "activity", label: "Activity", icon: ActivityIcon, count: activities.length },
            { key: "notes", label: "Notes", icon: StickyNote, count: activities.filter((a) => a.type === "note").length },
          ]} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {!panel && tab === "overview" && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={Building2} label="Customer" empty="No customer" value={company && <RecordChip entity="company" record={company} />} />
                  <Tile icon={IdCard} label="Reported by" empty="Nobody in particular" value={reporter && <RecordChip entity="customer_user" record={reporter} />} />
                  <Tile icon={UserCheck} label="Assignee" empty="Unassigned" value={ticket.owner ? memberName(ticket.owner) : undefined} />
                </div>
              </section>
            )}

            {shows("overview") && (
              <Description key={ticket.id} value={ticket.description} onSave={(v) => saveField({ description: v })} />
            )}

            {shows("activity") && <ActivityFeed activities={activities} />}

            {shows("notes") && (
              <NotesSection entity="ticket" recordId={ticket.id} activities={activities} open={noteOpen} onOpenChange={setNoteOpen}
                onAdded={async () => setActivities(await fetchActivities("ticket", ticket.id))} />
            )}
          </div>
        </main>
      </div>
    </div>
  );
}

/** What the ticket is about, as written: it edits in place, saved on leaving the box. */
function Description({ value, onSave }: { value: string; onSave: (value: string) => Promise<void> | void }) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <section className="flex flex-col gap-3" aria-label="Description">
      <h2 className="inline-flex items-center gap-2 text-sm font-medium"><AlignLeft className="size-4 text-muted-foreground" /> Description</h2>
      <Textarea aria-label="Description" value={draft} placeholder="What happened, what they need…" className="min-h-28"
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => { if (draft.trim() !== value) void onSave(draft.trim()); }} />
    </section>
  );
}
