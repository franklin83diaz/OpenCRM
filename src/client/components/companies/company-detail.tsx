import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Mail, Star, IdCard, LayoutGrid, Activity as ActivityIcon, Phone, Globe, Building2, Tag, AtSign, Users, StickyNote, Calendar, Clock, CheckSquare, BadgeCheck, CalendarClock } from "lucide-react";
import { useCrm } from "@/context";
import { EntityIcon } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { ActivityFeed, NotesSection, formatTimestamp } from "@/components/record-feed";
import { RecordChip } from "@/lib/relations";
import { api } from "@/api";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { ValuesField } from "@/components/value-fields";
import { TasksSection } from "@/components/tasks/tasks-section";
import { cn, formatDate } from "@/lib/utils";
import type { Company, Activity, Contact, Task } from "@/types";

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
            { key: "team", label: "Team", icon: Users, count: company.contact_count ?? 0 },
            { key: "notes", label: "Notes", icon: StickyNote, count: noteCount },
            { key: "tasks", label: "Tasks", icon: CheckSquare, count: taskCount },
          ]} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {!panel && tab === "overview" && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={Globe} label="Domain" empty="No domain" value={host && <a href={`https://${host}`} target="_blank" rel="noreferrer" className="text-info hover:underline">{host}</a>} />
                  <Tile icon={Users} label="Team" empty="No team" value={company.contact_count ? `${company.contact_count} ${company.contact_count === 1 ? "person" : "people"}` : undefined} />
                  <Tile icon={Tag} label="Industry" empty="No industry" value={company.industry} />
                  <Tile icon={AtSign} label="Email" empty="No email address" value={company.email} />
                  <Tile icon={Clock} label="Last activity" empty="No activity" value={recent[0] ? formatTimestamp(recent[0].created_at) : undefined} />
                  <Tile icon={Calendar} label="Created" empty="Unknown" value={formatTimestamp(company.created_at)} />
                </div>
              </section>
            )}

            {!panel && tab === "team" && <CompanyTeam companyId={company.id} />}

            {shows("tasks") && <TasksSection companyId={company.id} onCount={setTaskCount} />}

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

/** The people who work at the company: its contacts, with what they do there. */
function CompanyTeam({ companyId }: { companyId: string }) {
  const { changes, setError } = useCrm();
  const [people, setPeople] = useState<Contact[] | null>(null);
  useEffect(() => {
    let alive = true;
    api<{ contacts: Contact[] }>("GET", `/api/contacts?company_id=${encodeURIComponent(companyId)}&limit=100&sort=first_name&order=asc`)
      .then((r) => { if (alive) setPeople(r.contacts); }, (e) => setError(e instanceof Error ? e.message : "Could not load the team"));
    return () => { alive = false; };
  }, [companyId, changes]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <section className="flex flex-col gap-3" aria-label="Team">
      <h2 className="inline-flex items-center gap-2 text-sm font-medium">
        Team <span className="rounded-xs bg-secondary px-1.5 text-xs tabular text-muted-foreground">{people?.length ?? 0}</span>
      </h2>
      {people === null ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : people.length === 0 ? (
        <p className="text-sm text-faint">No contacts at this company yet.</p>
      ) : (
        <ul className="flex flex-col rounded-md bg-card shadow-edge">
          {people.map((p) => (
            <li key={p.id} className="flex h-11 items-center gap-3 px-3.5 [&+li]:border-t [&+li]:border-border">
              <span className="min-w-0 flex-1"><RecordChip entity="contact" record={{ id: p.id, label: `${p.first_name} ${p.last_name}`.trim(), domain: null }} /></span>
              {p.title && <span className="truncate text-[0.8125rem] text-muted-foreground">{p.title}</span>}
              {p.email && <a href={`mailto:${p.email}`} className="truncate text-[0.8125rem] text-info hover:underline">{p.email}</a>}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
