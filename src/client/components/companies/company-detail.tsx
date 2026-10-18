import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Mail, Star, IdCard, LayoutGrid, Activity as ActivityIcon, Phone, Globe, Building2, Tag, AtSign, Users, StickyNote, Calendar, Clock, CheckSquare, BadgeCheck, CalendarClock, LifeBuoy, UserRoundCheck, Network } from "lucide-react";
import { useCrm } from "@/context";
import { EntityIcon } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { ActivityFeed, NotesSection, formatTimestamp } from "@/components/record-feed";
import { RecordChip, RelationInput } from "@/lib/relations";
import { api } from "@/api";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { ValuesField } from "@/components/value-fields";
import { TasksSection } from "@/components/tasks/tasks-section";
import { ScopedRecordField } from "@/components/scoped-record-field";
import { RecordTickets, useOpenTicketCount } from "@/components/tickets/record-tickets";
import { cn, formatDate } from "@/lib/utils";
import type { Company, Activity, Contact, Deal, RelationRecord, Task } from "@/types";

/** A company's contacts, by name, for its main-contact picker. */
async function contactsOf(companyId: string): Promise<RelationRecord[]> {
  const { contacts } = await api<{ contacts: Contact[] }>("GET", `/api/contacts?company_id=${encodeURIComponent(companyId)}&limit=100&sort=first_name&order=asc`);
  return contacts.map((c) => ({ id: c.id, label: `${c.first_name} ${c.last_name}`.trim(), domain: null }));
}

// The company record page: same anatomy as a contact (DESIGN.md → Record
// pages), with the company's own attributes and tiles. Every value is the
// control; there is no edit mode. `panel` renders it in the side panel beside
// the companies list, as ContactDetail does.
export function CompanyDetail({ id, navigate, panel = false }: { id: string; navigate: (to: string) => void; panel?: boolean }) {
  const { fetchCompany, updateCompany, fetchActivities, setError, customFields, changes } = useCrm();
  const relationDefs = customFields.filter((d) => d.entity_type === "company" && d.field_type === "relation");
  const [company, setCompany] = useState<Company | null | undefined>(undefined);
  const [activities, setActivities] = useState<Activity[]>([]);
  const [taskCount, setTaskCount] = useState(0);
  // The full page shows one tab at a time; the panel, everything in one column.
  const [tab, setTab] = useState("overview");
  const [noteOpen, setNoteOpen] = useState(false);
  const shows = (key: string) => panel || tab === key;
  const openTickets = useOpenTicketCount({ companyId: id });
  const partners = useCompanyPartners(id, company?.main_partner);
  // The tab's count while the Tasks tab isn't open: the company's open tasks.
  useEffect(() => {
    let alive = true;
    api<{ tasks: Task[] }>("GET", `/api/tasks?company_id=${encodeURIComponent(id)}&status=open&limit=200`)
      .then((r) => { if (alive) setTaskCount(r.tasks.length); }, () => {});
    return () => { alive = false; };
  }, [id, changes]);

  const saveField = async (patch: Partial<Company>) => {
    if (!company) return;
    try {
      await updateCompany(company.id, patch);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const reload = async () => {
    const fresh = await fetchCompany(id);
    if (fresh) setCompany(fresh);
  };
  // Any record write (here, in the list, a link from another record) re-reads this one.
  const seen = useRef(changes);
  useEffect(() => {
    if (seen.current === changes) return;
    seen.current = changes;
    void reload();
  }, [changes]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let alive = true;
    setCompany(undefined);
    fetchCompany(id).then((c) => { if (alive) setCompany(c); });
    fetchActivities("company", id).then((a) => { if (alive) setActivities(a); });
    return () => { alive = false; };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  if (company === undefined) {
    return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;
  }
  if (company === null) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-12 text-center">
        <p className="text-sm text-muted-foreground">Company not found.</p>
        <Button size="sm" variant="outline" onClick={() => navigate("/companies")}><ArrowLeft className="size-4" /> Back to companies</Button>
      </div>
    );
  }

  const recent = [...activities].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const noteCount = activities.filter((a) => a.type === "note").length;
  const host = (company.domain || "").replace(/^https?:\/\//i, "").replace(/\/.*$/, "");

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {!panel && <RecordTopBar onClose={() => navigate("/companies")} crumb="Companies" />}

      <div className={cn("flex min-h-0 flex-1", panel && "flex-col overflow-y-auto")}>
        <aside className={cn("flex shrink-0 flex-col", !panel && "w-[31.25rem] overflow-y-auto border-r border-border")}>
          <div className="flex items-center gap-3 px-4 pt-4">
            <EntityIcon name={company.name} domain={company.domain} className="size-9 rounded-md text-sm" />
            <div className="min-w-0 flex-1">
              <InlineField value={company.name} placeholder="Company name" onSave={(v) => saveField({ name: v })} className="h-8 w-auto px-1.5 text-base font-semibold" />
            </div>
            <Button variant="ghost" size="icon" aria-label="Favourite" title="Favourite"><Star className="size-4" /></Button>
          </div>

          <div className="flex flex-wrap gap-2 px-4 pt-3 pb-4">
            <Button variant="outline" size="sm" disabled={!company.email} asChild={!!company.email}>
              {company.email ? <a href={`mailto:${company.email}`}><Mail className="size-4" /> Compose email</a> : <><Mail className="size-4" /> Compose email</>}
            </Button>
            {/* The contacts list, filtered to this company: its "Company is …" filter, which can be removed there. */}
            <Button variant="outline" size="sm" onClick={() => navigate(`/contacts?filters=${encodeURIComponent(JSON.stringify([{ field: "company_id", op: "is", value: [company.id] }]))}`)}>
              <Users className="size-4" /> Contacts {company.contact_count ? <span className="rounded-xs bg-secondary px-1.5 text-xs tabular text-muted-foreground">{company.contact_count}</span> : null}
            </Button>
            {/* The people who use the product there, filtered the same way. */}
            <Button variant="outline" size="sm" onClick={() => navigate(`/customer-users?filters=${encodeURIComponent(JSON.stringify([{ field: "company_id", op: "is", value: [company.id] }]))}`)}>
              <IdCard className="size-4" /> Customer users
            </Button>
          </div>

          <DetailsSection title="Record details">
            <dl className="flex flex-col">
              <Attr icon={Globe} label="Domains">
                <InlineField value={company.domain} placeholder="Set domain…" onSave={(v) => saveField({ domain: v })}
                  render={(v) => <a href={`https://${v.replace(/^https?:\/\//i, "")}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="text-info hover:underline">{v}</a>} />
              </Attr>
              <Attr icon={Building2} label="Name"><span className="px-2 text-sm">{company.name}</span></Attr>
              <Attr icon={UserRoundCheck} label="Main contact">
                <ScopedRecordField link entity="contact" label="main contact" value={company.main_contact?.id ?? null} known={company.main_contact}
                  placeholder="Set main contact…" emptyLabel="No main contact" load={() => contactsOf(company.id)}
                  onChange={(v) => saveField({ main_contact_id: v })} />
              </Attr>
              <Attr icon={Network} label="Main partner">
                <RelationInput link entity="partner" label="main partner" value={company.main_partner?.id ?? null} known={company.main_partner}
                  placeholder="Set main partner…" emptyLabel="No main partner" onChange={(v) => void saveField({ main_partner_id: v })} />
              </Attr>
              <Attr icon={Tag} label="Industry">
                <ValuesField entity="company" field="industry" label="industry" value={company.industry} onSave={(v) => saveField({ industry: v })} />
              </Attr>
              <Attr icon={AtSign} label="Email">
                <InlineField type="email" value={company.email} placeholder="Set email…" onSave={(v) => saveField({ email: v })}
                  render={(v) => <a href={`mailto:${v}`} onClick={(e) => e.stopPropagation()} className="text-info hover:underline">{v}</a>} />
              </Attr>
              <Attr icon={Phone} label="Phone">
                <InlineField type="tel" value={company.phone} placeholder="Set phone…" onSave={(v) => saveField({ phone: v })} className="tabular" />
              </Attr>
              <Attr icon={StickyNote} label="Description">
                <InlineField value={company.notes} placeholder="Set description…" onSave={(v) => saveField({ notes: v })} />
              </Attr>
              <Attr icon={BadgeCheck} label="Customer since">
                <InlineField type="date" value={company.customer_since ?? ""} placeholder="Not a customer" onSave={(v) => saveField({ customer_since: v || null })}
                  render={(v) => <span className="tabular">{formatDate(v)}</span>} />
              </Attr>
              {(company.customer_since || company.renewal_date) && (
                <Attr icon={CalendarClock} label="Renews">
                  <InlineField type="date" value={company.renewal_date ?? ""} placeholder="Set renewal…" onSave={(v) => saveField({ renewal_date: v || null })}
                    render={(v) => <span className="tabular">{formatDate(v)}</span>} />
                </Attr>
              )}
              <RelationAttrs defs={relationDefs} row={company} onSave={(key, v) => saveField({ [key]: v } as Partial<Company>)} />
            </dl>
            <button type="button" className="mt-1 h-8 text-[0.8125rem] text-muted-foreground hover:text-foreground">View all values</button>
          </DetailsSection>

          <RelationSections defs={relationDefs} row={company} />

          <DetailsSection title="Lists" action={<button type="button" className="hover:text-foreground">Add to list</button>}>
            <p className="py-1 text-sm text-faint">This record has not been added to any lists</p>
          </DetailsSection>
        </aside>

        <main className={cn("flex min-w-0 flex-col", panel ? "shrink-0 border-t border-border" : "flex-1 overflow-y-auto")}>
          {!panel && <RecordTabs active={tab} onChange={setTab} tabs={[
            { key: "overview", label: "Overview", icon: LayoutGrid },
            { key: "activity", label: "Activity", icon: ActivityIcon, count: activities.length },
            { key: "team", label: "Team", icon: Users, count: (company.contact_count ?? 0) + (partners?.length ?? 0) },
            { key: "notes", label: "Notes", icon: StickyNote, count: noteCount },
            { key: "tasks", label: "Tasks", icon: CheckSquare, count: taskCount },
            { key: "tickets", label: "Tickets", icon: LifeBuoy, count: openTickets ?? 0 },
          ]} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {!panel && tab === "overview" && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={Globe} label="Domain" empty="No domain" value={host && <a href={`https://${host}`} target="_blank" rel="noreferrer" className="text-info hover:underline">{host}</a>} />
                  <Tile icon={UserRoundCheck} label="Main contact" empty="No main contact" value={company.main_contact && <RecordChip entity="contact" record={company.main_contact} />} />
                  <Tile icon={Network} label="Main partner" empty="No main partner" value={company.main_partner && <RecordChip entity="partner" record={company.main_partner} />} />
                  <Tile icon={Users} label="Team" empty="No team" value={company.contact_count ? `${company.contact_count} ${company.contact_count === 1 ? "person" : "people"}` : undefined} />
                  <Tile icon={Tag} label="Industry" empty="No industry" value={company.industry} />
                  <Tile icon={AtSign} label="Email" empty="No email address" value={company.email} />
                  <Tile icon={Clock} label="Last activity" empty="No activity" value={recent[0] ? formatTimestamp(recent[0].created_at) : undefined} />
                  <Tile icon={Calendar} label="Created" empty="Unknown" value={formatTimestamp(company.created_at)} />
                </div>
              </section>
            )}

            {!panel && tab === "team" && (
              <CompanyTeam companyId={company.id} partners={partners}
                mainContactId={company.main_contact?.id ?? null} onMainContact={(v) => saveField({ main_contact_id: v })}
                mainPartnerId={company.main_partner?.id ?? null} onMainPartner={(v) => saveField({ main_partner_id: v })} />
            )}

            {shows("tasks") && <TasksSection companyId={company.id} onCount={setTaskCount} />}

            {shows("tickets") && <RecordTickets companyId={company.id} customer={!!company.customer_since} />}

            {shows("activity") && <ActivityFeed activities={activities} />}

            {shows("notes") && (
              <NotesSection entity="company" recordId={company.id} activities={activities} open={noteOpen} onOpenChange={setNoteOpen}
                onAdded={async () => setActivities(await fetchActivities("company", company.id))} />
            )}
          </div>
        </main>
      </div>
    </div>
  );
}

/** A partner on the company's team: one that worked its deals (which ones, and
 *  whether it was the deal's main partner), or its main partner. */
interface TeamPartner {
  record: RelationRecord;
  deals: { id: string; name: string; main: boolean }[];
}

/** The partners who worked the company's deals, by name, and its main partner
 *  even if it worked none (first then); null while loading. */
function useCompanyPartners(companyId: string, main: RelationRecord | null | undefined): TeamPartner[] | null {
  const { changes } = useCrm();
  const [fromDeals, setFromDeals] = useState<TeamPartner[] | null>(null);
  useEffect(() => {
    let alive = true;
    api<{ deals: Deal[] }>("GET", `/api/deals?company_id=${encodeURIComponent(companyId)}&limit=100&sort=created_at&order=asc`)
      .then((r) => {
        const byId = new Map<string, TeamPartner>();
        for (const d of r.deals) {
          for (const p of d.partners ?? []) {
            const tp = byId.get(p.id) ?? { record: { id: p.id, label: p.label, domain: p.domain }, deals: [] };
            tp.deals.push({ id: d.id, name: d.name, main: !!p.is_main });
            byId.set(p.id, tp);
          }
        }
        if (alive) setFromDeals([...byId.values()].sort((a, b) => a.record.label.localeCompare(b.record.label)));
      }, () => { if (alive) setFromDeals([]); });
    return () => { alive = false; };
  }, [companyId, changes]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!fromDeals) return null;
  return main && !fromDeals.some((p) => p.record.id === main.id) ? [{ record: main, deals: [] }, ...fromDeals] : fromDeals;
}

const mainBadge = <span className="inline-block h-5 shrink-0 rounded-full bg-warning-tint px-2 text-[0.8125rem] font-medium leading-5 text-warning">Main</span>;

/** The star on a team row: it marks the main one, or makes this one main. */
function MainStar({ name, role, main, onClick }: { name: string; role: string; main: boolean; onClick: () => void }) {
  const action = main ? `${name} is the ${role}. Unset` : `Make ${name} the ${role}`;
  return (
    <button type="button" aria-label={action} title={action} aria-pressed={main} onClick={onClick}
      className={cn("inline-flex size-7 shrink-0 items-center justify-center rounded-sm transition-opacity hover:bg-secondary",
        main ? "text-warning" : "text-muted-foreground opacity-0 focus-visible:opacity-100 group-hover/row:opacity-100 [@media(hover:none)]:opacity-100 [[data-agent]_&]:opacity-100")}>
      <Star className={cn("size-4", main && "fill-current")} />
    </button>
  );
}

/**
 * Who the customer is: the company's contacts, with what they do there, and
 * the partners who worked its deals. Its main contact and its main partner
 * come first, marked Main; a star makes another one main.
 */
function CompanyTeam({ companyId, partners, mainContactId, onMainContact, mainPartnerId, onMainPartner }: {
  companyId: string;
  partners: TeamPartner[] | null;
  mainContactId: string | null;
  onMainContact: (id: string | null) => void | Promise<void>;
  mainPartnerId: string | null;
  onMainPartner: (id: string | null) => void | Promise<void>;
}) {
  const { changes, setError } = useCrm();
  const [people, setPeople] = useState<Contact[] | null>(null);
  useEffect(() => {
    let alive = true;
    api<{ contacts: Contact[] }>("GET", `/api/contacts?company_id=${encodeURIComponent(companyId)}&limit=100&sort=first_name&order=asc`)
      .then((r) => { if (alive) setPeople(r.contacts); }, (e) => setError(e instanceof Error ? e.message : "Could not load the team"));
    return () => { alive = false; };
  }, [companyId, changes]); // eslint-disable-line react-hooks/exhaustive-deps

  const count = (n: number) => <span className="rounded-xs bg-secondary px-1.5 text-xs tabular text-muted-foreground">{n}</span>;
  const mainFirst = <T,>(list: T[], isMain: (x: T) => boolean) => [...list.filter(isMain), ...list.filter((x) => !isMain(x))];

  return (
    <section className="flex flex-col gap-6" aria-label="Team">
      <div className="flex flex-col gap-3">
        <h2 className="inline-flex items-center gap-2 text-sm font-medium">Contacts {count(people?.length ?? 0)}</h2>
        {people === null ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : people.length === 0 ? (
          <p className="text-sm text-faint">No contacts at this company yet.</p>
        ) : (
          <>
            {!mainContactId && <p className="text-[0.8125rem] text-muted-foreground">No main contact yet: pick one with its star.</p>}
            <ul className="flex flex-col rounded-md bg-card shadow-edge">
              {mainFirst(people, (p) => p.id === mainContactId).map((p) => {
                const name = `${p.first_name} ${p.last_name}`.trim();
                const main = p.id === mainContactId;
                return (
                  <li key={p.id} className="group/row flex h-11 items-center gap-3 px-3.5 [&+li]:border-t [&+li]:border-border">
                    <span className="flex min-w-0 flex-1 items-center gap-2">
                      <RecordChip entity="contact" record={{ id: p.id, label: name, domain: null }} />
                      {main && mainBadge}
                    </span>
                    {p.title && <span className="truncate text-[0.8125rem] text-muted-foreground">{p.title}</span>}
                    {p.email && <a href={`mailto:${p.email}`} className="truncate text-[0.8125rem] text-info hover:underline">{p.email}</a>}
                    <MainStar name={name} role="main contact" main={main} onClick={() => void onMainContact(main ? null : p.id)} />
                  </li>
                );
              })}
            </ul>
          </>
        )}
      </div>

      <div className="flex flex-col gap-3">
        <h2 className="inline-flex items-center gap-2 text-sm font-medium">Partners {count(partners?.length ?? 0)}</h2>
        {partners === null ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : partners.length === 0 ? (
          <p className="text-sm text-faint">No partner has worked a deal with this company yet.</p>
        ) : (
          <>
            {!mainPartnerId && <p className="text-[0.8125rem] text-muted-foreground">No main partner yet: pick one with its star.</p>}
            <ul className="flex flex-col rounded-md bg-card shadow-edge">
              {mainFirst(partners, (p) => p.record.id === mainPartnerId).map((p) => {
                const main = p.record.id === mainPartnerId;
                return (
                  <li key={p.record.id} className="group/row flex min-h-11 items-center gap-3 px-3.5 py-1.5 [&+li]:border-t [&+li]:border-border">
                    <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="flex min-w-0 items-center gap-2">
                        <RecordChip entity="partner" record={p.record} />
                        {main && mainBadge}
                      </span>
                      {p.deals.length > 0 && (
                        <span className="truncate text-[0.8125rem] text-muted-foreground">
                          {p.deals.map((d, i) => <span key={d.id}>{i > 0 && ", "}{d.name}{d.main && <span className="text-warning"> (main)</span>}</span>)}
                        </span>
                      )}
                    </span>
                    <MainStar name={p.record.label} role="main partner" main={main} onClick={() => void onMainPartner(main ? null : p.record.id)} />
                  </li>
                );
              })}
            </ul>
          </>
        )}
      </div>
    </section>
  );
}
