import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Handshake, DollarSign, CircleDashed, Calendar, StickyNote, Building2, User, Clock, LayoutGrid, Network, Repeat, UserCheck, Activity as ActivityIcon } from "lucide-react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { ActivityFeed, NotesSection, formatTimestamp } from "@/components/record-feed";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { RecordChip, RelationInput } from "@/lib/relations";
import { OtherCompany } from "@/components/other-company";
import { OwnerField, memberName } from "@/components/deals/owner-field";
import { DealPartners } from "@/components/deals/deal-partners";
import { formatMoney, colorClasses, cn } from "@/lib/utils";
import { HealthPill, daysAgo } from "@/components/health";
import { TasksSection } from "@/components/tasks/tasks-section";
import { NextStepLine } from "@/components/deals/next-step";
import type { Deal, Activity, RelationRecord, StageDef } from "@/types";

/** A close date (YYYY-MM-DD) in the reader's format, read as a calendar day, not a UTC instant. */
function formatDay(day: string): string {
  const d = new Date(`${day}T00:00:00`);
  return Number.isNaN(d.getTime()) ? day : d.toLocaleDateString();
}

// The deal record page: same anatomy as a contact or company. Its company and
// its contact are both its own links, each optional; setting a contact on a
// deal with no company fills the company in (the server does it). It is worth
// something once and something each month, is assigned to one account or to
// nobody yet, and any number of partners take part in it, each with its share.
// `panel` renders it in the side panel beside the deals board.
export function DealDetail({ id, navigate, panel = false }: { id: string; navigate: (to: string) => void; panel?: boolean }) {
  const { fetchDeal, updateDeal, fetchActivities, fetchContact, setError, customFields, changes, stages } = useCrm();
  const relationDefs = customFields.filter((d) => d.entity_type === "deal" && d.field_type === "relation");
  const [deal, setDeal] = useState<Deal | null | undefined>(undefined);
  const [activities, setActivities] = useState<Activity[]>([]);
  // The full page shows one tab at a time; the panel, everything in one column.
  const [tab, setTab] = useState("overview");
  const [noteOpen, setNoteOpen] = useState(false);
  const shows = (key: string) => panel || tab === key;

  const saveField = async (patch: Partial<Deal>) => {
    if (!deal) return;
    try {
      await updateDeal(deal.id, patch);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const reload = async () => {
    const fresh = await fetchDeal(id);
    if (fresh) setDeal(fresh);
    setActivities(await fetchActivities("deal", id));
  };
  // Any record write (here, on the board, a link from another record) re-reads this one.
  const seen = useRef(changes);
  useEffect(() => {
    if (seen.current === changes) return;
    seen.current = changes;
    void reload();
  }, [changes]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let alive = true;
    setDeal(undefined);
    fetchDeal(id).then((d) => { if (alive) setDeal(d); });
    fetchActivities("deal", id).then((a) => { if (alive) setActivities(a); });
    return () => { alive = false; };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  // The company the deal's contact works at, which may not be the deal's own:
  // read from the contact, again after any record write (it may have moved).
  const contactId = deal?.contact_id ?? null;
  const [contactCompany, setContactCompany] = useState<RelationRecord | null>(null);
  useEffect(() => {
    let alive = true;
    setContactCompany(null);
    if (contactId) {
      fetchContact(contactId).then((c) => {
        if (alive && c?.company_id && c.company_name != null) setContactCompany({ id: c.company_id, label: c.company_name, domain: c.company_domain ?? null });
      });
    }
    return () => { alive = false; };
  }, [contactId, changes]); // eslint-disable-line react-hooks/exhaustive-deps

  if (deal === undefined) {
    return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;
  }
  if (deal === null) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-12 text-center">
        <p className="text-sm text-muted-foreground">Deal not found.</p>
        <Button size="sm" variant="outline" onClick={() => navigate("/deals")}><ArrowLeft className="size-4" /> Back to deals</Button>
      </div>
    );
  }

  const recent = [...activities].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const noteCount = activities.filter((a) => a.type === "note").length;
  const stage = stages.find((s) => s.key === deal.stage);
  const contactName = `${deal.contact_first_name ?? ""} ${deal.contact_last_name ?? ""}`.trim();
  const company: RelationRecord | undefined = deal.company_id && deal.company_name != null
    ? { id: deal.company_id, label: deal.company_name, domain: deal.company_domain ?? null }
    : undefined;
  const contact: RelationRecord | undefined = deal.contact_id && deal.contact_first_name != null
    ? { id: deal.contact_id, label: contactName, domain: null }
    : undefined;
  // Shown beside the contact when it isn't the deal's company, as a link to it.
  const otherCompany = contactCompany && contactCompany.id !== deal.company_id ? contactCompany : null;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {!panel && <RecordTopBar onClose={() => navigate("/deals")} crumb="Deals" />}

      <div className={cn("flex min-h-0 flex-1", panel && "flex-col overflow-y-auto")}>
        <aside className={cn("flex shrink-0 flex-col", !panel && "w-[31.25rem] overflow-y-auto border-r border-border")}>
          <div className="flex items-center gap-3 px-4 pt-4 pb-4">
            <span className="inline-flex size-9 shrink-0 items-center justify-center rounded-md bg-success-tint text-success"><Handshake className="size-4" /></span>
            <div className="min-w-0 flex-1">
              <InlineField value={deal.name} placeholder="Deal name" onSave={(v) => { if (v) return saveField({ name: v }); }} className="h-8 w-auto px-1.5 text-base font-semibold" />
            </div>
          </div>

          <DetailsSection title="Deal">
            <dl className="flex flex-col">
              <Attr icon={DollarSign} label="One-time">
                <InlineField type="number" value={deal.value ? String(deal.value) : ""} placeholder="Set one-time value…" onSave={(v) => saveField({ value: Number(v) || 0 })}
                  render={(v) => <span className="tabular">{formatMoney(Number(v))}</span>} />
              </Attr>
              <Attr icon={Repeat} label="Monthly">
                <InlineField type="number" value={deal.recurring_value ? String(deal.recurring_value) : ""} placeholder="Set monthly value…" onSave={(v) => saveField({ recurring_value: Number(v) || 0 })}
                  render={(v) => <span className="tabular">{formatMoney(Number(v))}<span className="text-muted-foreground">/mo</span></span>} />
              </Attr>
              <Attr icon={CircleDashed} label="Stage">
                <StageField value={deal.stage} stage={stage} stages={stages} onSave={(key) => saveField({ stage: key })} />
              </Attr>
              <Attr icon={UserCheck} label="Owner">
                <OwnerField value={deal.owner_id} owner={deal.owner} onChange={(id) => saveField({ owner_id: id })} />
              </Attr>
              <Attr icon={Calendar} label="Close date">
                <InlineField type="date" value={deal.close_date} placeholder="Set close date…" onSave={(v) => saveField({ close_date: v })} render={formatDay} />
              </Attr>
              <Attr icon={StickyNote} label="Notes">
                <InlineField value={deal.notes} placeholder="Set notes…" onSave={(v) => saveField({ notes: v })} />
              </Attr>
            </dl>
          </DetailsSection>

          <DetailsSection title="Relations">
            <dl className="flex flex-col">
              <Attr icon={Building2} label="Company">
                <RelationInput link entity="company" value={deal.company_id} known={company} placeholder="Set company…" emptyLabel="No company"
                  onChange={(v) => void saveField({ company_id: v })} />
              </Attr>
              <Attr icon={User} label="Contact">
                <RelationInput link entity="contact" value={deal.contact_id} known={contact} placeholder="Set contact…" emptyLabel="No contact"
                  onChange={(v) => void saveField({ contact_id: v })}
                  suffix={otherCompany && <OtherCompany company={otherCompany} title={`${contactName || "The contact"} works at ${otherCompany.label}, not at this deal's company`} />} />
              </Attr>
              <RelationAttrs defs={relationDefs} row={deal} onSave={(key, v) => saveField({ [key]: v } as Partial<Deal>)} />
            </dl>
          </DetailsSection>

          <DealPartners deal={deal} />

          <RelationSections defs={relationDefs} row={deal} />

          <DetailsSection title="System">
            <dl className="flex flex-col">
              <Attr icon={Calendar} label="Created"><span className="px-2 text-sm">{formatTimestamp(deal.created_at)}</span></Attr>
              <Attr icon={Clock} label="Updated"><span className="px-2 text-sm">{formatTimestamp(deal.updated_at)}</span></Attr>
            </dl>
          </DetailsSection>
        </aside>

        <main className={cn("flex min-w-0 flex-col", panel ? "shrink-0 border-t border-border" : "flex-1 overflow-y-auto")}>
          {!panel && <RecordTabs active={tab} onChange={setTab} tabs={[
            { key: "overview", label: "Overview", icon: LayoutGrid },
            { key: "activity", label: "Activity", icon: ActivityIcon, count: activities.length },
            { key: "notes", label: "Notes", icon: StickyNote, count: noteCount },
          ]} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {shows("overview") && deal.progress && (
              <section className="flex flex-col gap-3" aria-label="Next step">
                <h2 className="text-sm font-medium">Next step</h2>
                <div className="flex flex-col gap-2 rounded-md bg-card p-3.5 shadow-edge">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <HealthPill status={deal.progress.status} />
                    <NextStepLine progress={deal.progress} className="text-sm" />
                    <span className="ml-auto text-[0.8125rem] text-muted-foreground">Last contact {daysAgo(deal.progress.days_quiet).toLowerCase()}</span>
                  </div>
                  {deal.progress.reasons.length > 0 && (
                    <ul className="flex flex-col gap-0.5 text-[0.8125rem] text-muted-foreground">
                      {deal.progress.reasons.map((r) => <li key={r}>{r}</li>)}
                    </ul>
                  )}
                  {!deal.progress.next_step && (
                    <p className="text-[0.8125rem] text-muted-foreground">Add a task with a date below: that's the deal's next step.</p>
                  )}
                </div>
              </section>
            )}

            {!panel && tab === "overview" && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={DollarSign} label="One-time" empty="No one-time value" value={deal.value ? <span className="tabular">{formatMoney(deal.value)}</span> : undefined} />
                  <Tile icon={Repeat} label="Monthly" empty="No monthly value" value={deal.recurring_value ? <span className="tabular">{formatMoney(deal.recurring_value)}/mo</span> : undefined} />
                  <Tile icon={CircleDashed} label="Stage" empty="No stage" value={stage?.label ?? deal.stage} />
                  <Tile icon={UserCheck} label="Owner" empty="Unassigned" value={deal.owner ? memberName(deal.owner) : undefined} />
                  <Tile icon={Calendar} label="Close date" empty="No close date" value={deal.close_date ? formatDay(deal.close_date) : undefined} />
                  <Tile icon={Network} label="Partners" empty="No partners" value={deal.partners?.length ? deal.partners.map((p) => p.label).join(", ") : undefined} />
                  <Tile icon={Building2} label="Company" empty="No company" value={company && <RecordChip entity="company" record={company} />} />
                  <Tile icon={User} label="Contact" empty="No contact" value={contact && <RecordChip entity="contact" record={contact} />} />
                  <Tile icon={Clock} label="Last activity" empty="No activity" value={recent[0] ? formatTimestamp(recent[0].created_at) : undefined} />
                </div>
              </section>
            )}

            {shows("overview") && <TasksSection companyId={deal.company_id} dealId={deal.id} />}

            {shows("activity") && <ActivityFeed activities={activities} />}

            {shows("notes") && (
              <NotesSection entity="deal" recordId={deal.id} activities={activities} open={noteOpen} onOpenChange={setNoteOpen}
                onAdded={async () => setActivities(await fetchActivities("deal", deal.id))} />
            )}
          </div>
        </main>
      </div>
    </div>
  );
}

/** The stage as its own control: the stage's dot and name, which opens the pipeline's stages. */
function StageField({ value, stage, stages, onSave }: { value: string; stage?: StageDef; stages: StageDef[]; onSave: (key: string) => Promise<void> }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" aria-label="Change stage" className="flex h-8 w-full items-center gap-2 rounded-[0.5rem] px-2 text-left text-sm hover:bg-secondary">
          <span className={cn("size-2 shrink-0 rounded-full", colorClasses(stage?.color).dot)} />
          {stage?.label ?? value}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        {stages.map((s) => (
          <DropdownMenuItem key={s.key} onClick={() => { if (s.key !== value) void onSave(s.key); }}>
            <span className={cn("size-2 rounded-full", colorClasses(s.color).dot)} /> {s.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
