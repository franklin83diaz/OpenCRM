import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Mail, LayoutGrid, Activity as ActivityIcon, Phone, AtSign, Building2, StickyNote, Calendar, Clock, LifeBuoy } from "lucide-react";
import { useCrm } from "@/context";
import { Avatar } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { ActivityFeed, NotesSection, formatTimestamp } from "@/components/record-feed";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { RecordChip, RelationInput } from "@/lib/relations";
import { RecordTickets, useOpenTicketCount } from "@/components/tickets/record-tickets";
import { cn } from "@/lib/utils";
import type { Activity, CustomerUser, RelationRecord } from "@/types";

// The customer user record page: same anatomy as a contact (DESIGN.md →
// Record pages): who they are and the customer they use the product at.
// Every value is the control; there is no edit mode. `panel` renders it in
// the side panel beside the customer users list.
export function CustomerUserDetail({ id, navigate, panel = false }: { id: string; navigate: (to: string) => void; panel?: boolean }) {
  const { fetchCustomerUser, updateCustomerUser, fetchActivities, setError, customFields, changes } = useCrm();
  const relationDefs = customFields.filter((d) => d.entity_type === "customer_user" && d.field_type === "relation");
  const [user, setUser] = useState<CustomerUser | null | undefined>(undefined);
  const [activities, setActivities] = useState<Activity[]>([]);
  // The full page shows one tab at a time; the panel, everything in one column.
  const [tab, setTab] = useState("overview");
  const [noteOpen, setNoteOpen] = useState(false);
  const shows = (key: string) => panel || tab === key;
  const openTickets = useOpenTicketCount({ customerUserId: id });

  const saveField = async (patch: Partial<CustomerUser>) => {
    if (!user) return;
    try {
      await updateCustomerUser(user.id, patch);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const reload = async () => {
    const fresh = await fetchCustomerUser(id);
    if (fresh) setUser(fresh);
    setActivities(await fetchActivities("customer_user", id));
  };
  // Any record write (here, in the list, its company renamed) re-reads this one.
  const seen = useRef(changes);
  useEffect(() => {
    if (seen.current === changes) return;
    seen.current = changes;
    void reload();
  }, [changes]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let alive = true;
    setUser(undefined);
    fetchCustomerUser(id).then((u) => { if (alive) setUser(u); });
    fetchActivities("customer_user", id).then((a) => { if (alive) setActivities(a); });
    return () => { alive = false; };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  if (user === undefined) {
    return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;
  }
  if (user === null) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-12 text-center">
        <p className="text-sm text-muted-foreground">Customer user not found.</p>
        <Button size="sm" variant="outline" onClick={() => navigate("/customer-users")}><ArrowLeft className="size-4" /> Back to customer users</Button>
      </div>
    );
  }

  const recent = [...activities].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const words = user.name.trim().split(/\s+/);
  const company: RelationRecord | undefined = user.company_id && user.company_name != null
    ? { id: user.company_id, label: user.company_name, domain: user.company_domain ?? null }
    : undefined;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {!panel && <RecordTopBar onClose={() => navigate("/customer-users")} crumb="Customer users" />}

      <div className={cn("flex min-h-0 flex-1", panel && "flex-col overflow-y-auto")}>
        <aside className={cn("flex shrink-0 flex-col", !panel && "w-[31.25rem] overflow-y-auto border-r border-border")}>
          <div className="flex items-center gap-3 px-4 pt-4">
            <Avatar firstName={words[0]} lastName={words.length > 1 ? words[words.length - 1] : ""} className="size-9 text-sm" />
            <div className="min-w-0 flex-1">
              <InlineField value={user.name} placeholder="Name" onSave={(v) => { if (v) return saveField({ name: v }); }} className="h-8 w-auto px-1.5 text-base font-semibold" />
            </div>
          </div>

          <div className="flex flex-wrap gap-2 px-4 pt-3 pb-4">
            <Button variant="outline" size="sm" disabled={!user.email} asChild={!!user.email}>
              {user.email ? <a href={`mailto:${user.email}`}><Mail className="size-4" /> Compose email</a> : <><Mail className="size-4" /> Compose email</>}
            </Button>
          </div>

          <DetailsSection title="Record details">
            <dl className="flex flex-col">
              <Attr icon={Building2} label="Company">
                <RelationInput link customers entity="company" value={user.company_id} known={company} placeholder="Set company…" emptyLabel="No company"
                  onChange={(v) => void saveField({ company_id: v })} />
              </Attr>
              <Attr icon={AtSign} label="Email">
                <InlineField type="email" value={user.email} placeholder="Set email…" onSave={(v) => saveField({ email: v })}
                  render={(v) => <a href={`mailto:${v}`} onClick={(e) => e.stopPropagation()} className="text-info hover:underline">{v}</a>} />
              </Attr>
              <Attr icon={Phone} label="Phone">
                <InlineField type="tel" value={user.phone} placeholder="Set phone…" onSave={(v) => saveField({ phone: v })} className="tabular" />
              </Attr>
              <Attr icon={StickyNote} label="Notes">
                <InlineField value={user.notes} placeholder="Set notes…" onSave={(v) => saveField({ notes: v })} />
              </Attr>
              <RelationAttrs defs={relationDefs} row={user} onSave={(key, v) => saveField({ [key]: v } as Partial<CustomerUser>)} />
            </dl>
          </DetailsSection>

          <RelationSections defs={relationDefs} row={user} />

          <DetailsSection title="System">
            <dl className="flex flex-col">
              <Attr icon={Calendar} label="Created"><span className="px-2 text-sm">{formatTimestamp(user.created_at)}</span></Attr>
              <Attr icon={Clock} label="Updated"><span className="px-2 text-sm">{formatTimestamp(user.updated_at)}</span></Attr>
            </dl>
          </DetailsSection>
        </aside>

        <main className={cn("flex min-w-0 flex-col", panel ? "shrink-0 border-t border-border" : "flex-1 overflow-y-auto")}>
          {!panel && <RecordTabs active={tab} onChange={setTab} tabs={[
            { key: "overview", label: "Overview", icon: LayoutGrid },
            { key: "activity", label: "Activity", icon: ActivityIcon, count: activities.length },
            { key: "notes", label: "Notes", icon: StickyNote, count: activities.filter((a) => a.type === "note").length },
            { key: "tickets", label: "Tickets", icon: LifeBuoy, count: openTickets ?? 0 },
          ]} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {!panel && tab === "overview" && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={Building2} label="Company" empty="No company" value={company && <RecordChip entity="company" record={company} />} />
                  <Tile icon={AtSign} label="Email" empty="No email address" value={user.email} />
                  <Tile icon={Clock} label="Last activity" empty="No activity" value={recent[0] ? formatTimestamp(recent[0].created_at) : undefined} />
                </div>
              </section>
            )}

            {shows("tickets") && <RecordTickets companyId={user.company_id} customerUserId={user.id} />}

            {shows("activity") && <ActivityFeed activities={activities} />}

            {shows("notes") && (
              <NotesSection entity="customer_user" recordId={user.id} activities={activities} open={noteOpen} onOpenChange={setNoteOpen}
                onAdded={async () => setActivities(await fetchActivities("customer_user", user.id))} />
            )}
          </div>
        </main>
      </div>
    </div>
  );
}
