import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Mail, Calendar, StickyNote, Star, LayoutGrid, Activity as ActivityIcon, Phone, Building2, Briefcase, AtSign, User, Clock } from "lucide-react";
import { useCrm } from "@/context";
import { Avatar, CategoryBadge } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { ActivityFeed, NotesSection, formatTimestamp } from "@/components/record-feed";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { RecordChip, RelationInput } from "@/lib/relations";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import type { Contact, Activity, RelationRecord } from "@/types";

// `panel` is the same record in the side panel beside the contacts list: one
// column, no tab strip, and no highlight tiles repeating the details above them.
export function ContactDetail({ id, navigate, panel = false }: { id: string; navigate: (to: string) => void; panel?: boolean }) {
  const { fetchContact, fetchActivities, updateContact, setError, customFields, changes } = useCrm();
  const relationDefs = customFields.filter((d) => d.entity_type === "contact" && d.field_type === "relation");

  const [contact, setContact] = useState<Contact | null | undefined>(undefined);
  const [activities, setActivities] = useState<Activity[]>([]);

  // The full page shows one tab at a time; the panel, everything in one column.
  const [tab, setTab] = useState("overview");
  const [noteOpen, setNoteOpen] = useState(false);
  const shows = (key: string) => panel || tab === key;

  // Click-to-edit saves one field, then re-reads the record so derived columns
  // (company name, custom flat fields) stay consistent with the server.
  const saveField = async (patch: Partial<Contact>) => {
    if (!contact) return;
    try {
      await updateContact(contact.id, patch);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const reload = async () => {
    const fresh = await fetchContact(id);
    if (fresh) setContact(fresh);
  };
  // Any record write (here, in the list, a link from another record) re-reads this one.
  const seen = useRef(changes);
  useEffect(() => {
    if (seen.current === changes) return;
    seen.current = changes;
    void reload();
    void fetchActivities("contact", id).then(setActivities);
  }, [changes]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let alive = true;
    setContact(undefined);
    fetchContact(id).then((c) => {
      if (alive) setContact(c);
    });
    fetchActivities("contact", id).then((a) => {
      if (alive) setActivities(a);
    });
    return () => {
      alive = false;
    };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  const reloadActivities = async () => {
    setActivities(await fetchActivities("contact", id));
  };

  if (contact === undefined) {
    return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;
  }

  if (contact === null) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-12 text-center">
        <p className="text-sm text-muted-foreground">Contact not found.</p>
        <Button size="sm" variant="outline" onClick={() => navigate("/contacts")}>
          <ArrowLeft className="size-4" />
          Back to contacts
        </Button>
      </div>
    );
  }

  const company: RelationRecord | undefined = contact.company_id && contact.company_name != null
    ? { id: contact.company_id, label: contact.company_name, domain: contact.company_domain ?? null }
    : undefined;
  const fullName = `${contact.first_name} ${contact.last_name}`.trim() || "Contact";

  // "Add note" beside the email button: the Notes tab, with the box open.
  const startNote = () => {
    setTab("notes");
    setNoteOpen(true);
  };

  const noteCount = activities.filter((a) => a.type === "note").length;
  const recent = [...activities].sort((a, b) => b.created_at.localeCompare(a.created_at));

  const tabs: Array<{ key: string; label: string; icon: typeof Mail; count?: number }> = [
    { key: "overview", label: "Overview", icon: LayoutGrid },
    { key: "activity", label: "Activity", icon: ActivityIcon, count: activities.length },
    { key: "notes", label: "Notes", icon: StickyNote, count: noteCount },
  ];

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Top bar: same height and rule as the sidebar brand row, so the line runs across. */}
      {!panel && <RecordTopBar onClose={() => navigate("/contacts")} crumb="Contacts" />}

      <div className={cn("flex min-h-0 flex-1", panel && "flex-col overflow-y-auto")}>
        {/* Left column: identity, actions, record details. */}
        <aside className={cn("flex shrink-0 flex-col", !panel && "w-[31.25rem] overflow-y-auto border-r border-border")}>
          <div className="flex items-center gap-3 px-4 pt-4">
            <Avatar firstName={contact.first_name} lastName={contact.last_name} className="size-9 text-xs" />
            <div className="flex min-w-0 flex-1 items-center">
              <InlineField value={contact.first_name} placeholder="First name" onSave={(v) => saveField({ first_name: v })} className="h-8 w-auto px-1.5 text-base font-semibold" />
              <InlineField value={contact.last_name} placeholder="Last name" onSave={(v) => saveField({ last_name: v })} className="h-8 w-auto px-1.5 text-base font-semibold" />
            </div>
            <Button variant="ghost" size="icon" aria-label="Favourite" title="Favourite"><Star className="size-4" /></Button>
          </div>

          <div className="flex flex-wrap gap-2 px-4 pt-3 pb-4">
            <Button variant="outline" size="sm" disabled={!contact.email} asChild={!!contact.email}>
              {contact.email ? <a href={`mailto:${contact.email}`}><Mail className="size-4" /> Compose email</a> : <><Mail className="size-4" /> Compose email</>}
            </Button>
            <Button variant="outline" size="icon" onClick={startNote} aria-label="Add note" title="Add note">
              <StickyNote className="size-4" />
            </Button>
          </div>

          <DetailsSection title="Record details">
            <dl className="flex flex-col">
              <Attr icon={User} label="Name"><span className="px-2 text-sm">{fullName}</span></Attr>
              <Attr icon={AtSign} label="Email addresses">
                <InlineField type="email" value={contact.email} placeholder="Set email…" onSave={(v) => saveField({ email: v })}
                  render={(v) => <a href={`mailto:${v}`} onClick={(e) => e.stopPropagation()} className="text-info hover:underline">{v}</a>} />
              </Attr>
              <Attr icon={Phone} label="Phone numbers">
                <InlineField type="tel" value={contact.phone} placeholder="Set phone…" onSave={(v) => saveField({ phone: v })} className="tabular" />
              </Attr>
              <Attr icon={Building2} label="Company">
                <RelationInput link entity="company" value={contact.company_id} placeholder="Set company…" emptyLabel="No company"
                  known={company}
                  onChange={(v) => void saveField({ company_id: v })} />
              </Attr>
              <Attr icon={Briefcase} label="Job title">
                <InlineField value={contact.title} placeholder="Set job title…" onSave={(v) => saveField({ title: v })} />
              </Attr>
              <Attr icon={Clock} label="Status"><span className="px-2"><CategoryBadge value={contact.status} /></span></Attr>
              <RelationAttrs defs={relationDefs} row={contact} onSave={(key, v) => saveField({ [key]: v } as Partial<Contact>)} />
            </dl>
            <button type="button" className="mt-1 h-8 text-[0.8125rem] text-muted-foreground hover:text-foreground">View all values</button>
          </DetailsSection>

          <RelationSections defs={relationDefs} row={contact} />

          <DetailsSection title="Lists" action={<button type="button" className="hover:text-foreground">Add to list</button>}>
            <p className="py-1 text-sm text-faint">This record has not been added to any lists</p>
          </DetailsSection>
        </aside>

        {/* Main column: tabs, highlights, activity, and the sections that will grow. */}
        <main className={cn("flex min-w-0 flex-col", panel ? "shrink-0 border-t border-border" : "flex-1 overflow-y-auto")}>
          {!panel && <RecordTabs active={tab} onChange={setTab} tabs={tabs} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {!panel && tab === "overview" && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={AtSign} label="Email addresses" empty="No email address" value={contact.email && <a href={`mailto:${contact.email}`} className="text-info hover:underline">{contact.email}</a>} />
                  <Tile icon={Phone} label="Phone numbers" empty="No phone number" value={contact.phone} />
                  <Tile icon={Building2} label="Company" empty="No company" value={company && <RecordChip entity="company" record={company} />} />
                  <Tile icon={Briefcase} label="Job title" empty="No job title" value={contact.title} />
                  <Tile icon={Clock} label="Last activity" empty="No activity" value={recent[0] ? formatTimestamp(recent[0].created_at) : undefined} />
                  <Tile icon={Calendar} label="Created" empty="Unknown" value={formatTimestamp(contact.created_at)} />
                </div>
              </section>
            )}

            {shows("activity") && <ActivityFeed activities={activities} empty="No activity yet. Add a note to start the timeline." />}

            {shows("notes") && (
              <NotesSection entity="contact" recordId={contact.id} activities={activities} open={noteOpen} onOpenChange={setNoteOpen} onAdded={reloadActivities} />
            )}
          </div>
        </main>
      </div>
    </div>
  );
}
