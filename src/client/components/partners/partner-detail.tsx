import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Mail, LayoutGrid, Activity as ActivityIcon, Phone, AtSign, DollarSign, Tag, CircleDot, StickyNote, Calendar, Clock, Handshake, Plus, X } from "lucide-react";
import { useCrm } from "@/context";
import { api } from "@/api";
import { EntityIcon } from "@/components/shared";
import { OptionField, ValuesField } from "@/components/value-fields";
import { PARTNER_STATUSES, PartnerStatusBadge } from "@/components/partners/statuses";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { ActivityFeed, NotesSection, formatTimestamp } from "@/components/record-feed";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { RecordChip, RecordPicker } from "@/lib/relations";
import { cn, colorClasses, formatMoney } from "@/lib/utils";
import type { Activity, Deal, DealPartner, Partner } from "@/types";

// The partner record page: same anatomy as a company (DESIGN.md → Record
// pages), with the deals the partner takes part in. Every value is the
// control; there is no edit mode. `panel` renders it in the side panel beside
// the partners list.
export function PartnerDetail({ id, navigate, panel = false }: { id: string; navigate: (to: string) => void; panel?: boolean }) {
  const { fetchPartner, updatePartner, fetchActivities, setError, customFields, changes, stages } = useCrm();
  const relationDefs = customFields.filter((d) => d.entity_type === "partner" && d.field_type === "relation");
  const [partner, setPartner] = useState<Partner | null | undefined>(undefined);
  const [deals, setDeals] = useState<Deal[]>([]);
  const [activities, setActivities] = useState<Activity[]>([]);
  // The full page shows one tab at a time; the panel, everything in one column.
  const [tab, setTab] = useState("overview");
  const [noteOpen, setNoteOpen] = useState(false);
  const shows = (key: string) => panel || tab === key;

  const fetchDeals = () =>
    api<{ deals: Deal[] }>("GET", `/api/deals?partner_id=${encodeURIComponent(id)}&limit=100&sort=created_at&order=desc`).then((d) => d.deals);

  const saveField = async (patch: Partial<Partner>) => {
    if (!partner) return;
    try {
      await updatePartner(partner.id, patch);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const reload = async () => {
    const [fresh, list] = await Promise.all([fetchPartner(id), fetchDeals().catch(() => null)]);
    if (fresh) setPartner(fresh);
    if (list) setDeals(list);
  };
  // Any record write (here, in the list, a deal that took it on) re-reads this one.
  const seen = useRef(changes);
  useEffect(() => {
    if (seen.current === changes) return;
    seen.current = changes;
    void reload();
  }, [changes]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let alive = true;
    setPartner(undefined);
    fetchPartner(id).then((p) => { if (alive) setPartner(p); });
    fetchDeals().then((d) => { if (alive) setDeals(d); }, () => {});
    fetchActivities("partner", id).then((a) => { if (alive) setActivities(a); });
    return () => { alive = false; };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  if (partner === undefined) {
    return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;
  }
  if (partner === null) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-12 text-center">
        <p className="text-sm text-muted-foreground">Partner not found.</p>
        <Button size="sm" variant="outline" onClick={() => navigate("/partners")}><ArrowLeft className="size-4" /> Back to partners</Button>
      </div>
    );
  }

  const recent = [...activities].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const dealCount = partner.deal_count ?? deals.length;
  // What the partner gets from the deals won: its shares of them, once and each month.
  const won = deals.filter((d) => stages.find((s) => s.key === d.stage)?.is_won);
  const earned = won.reduce((t, d) => {
    const share = d.partners?.find((p) => p.id === partner.id);
    return { once: t.once + (share?.one_time_value ?? 0), monthly: t.monthly + (share?.recurring_value ?? 0) };
  }, { once: 0, monthly: 0 });

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {!panel && <RecordTopBar onClose={() => navigate("/partners")} crumb="Partners" />}

      <div className={cn("flex min-h-0 flex-1", panel && "flex-col overflow-y-auto")}>
        <aside className={cn("flex shrink-0 flex-col", !panel && "w-[31.25rem] overflow-y-auto border-r border-border")}>
          <div className="flex items-center gap-3 px-4 pt-4">
            <EntityIcon name={partner.name} className="size-9 rounded-md text-sm" />
            <div className="min-w-0 flex-1">
              <InlineField value={partner.name} placeholder="Partner name" onSave={(v) => { if (v) return saveField({ name: v }); }} className="h-8 w-auto px-1.5 text-base font-semibold" />
            </div>
          </div>

          <div className="flex flex-wrap gap-2 px-4 pt-3 pb-4">
            <Button variant="outline" size="sm" disabled={!partner.email} asChild={!!partner.email}>
              {partner.email ? <a href={`mailto:${partner.email}`}><Mail className="size-4" /> Compose email</a> : <><Mail className="size-4" /> Compose email</>}
            </Button>
          </div>

          <DetailsSection title="Record details">
            <dl className="flex flex-col">
              <Attr icon={Tag} label="Industry">
                <ValuesField entity="partner" field="industry" label="industry" value={partner.industry} onSave={(v) => saveField({ industry: v })} />
              </Attr>
              <Attr icon={CircleDot} label="Status">
                <OptionField options={PARTNER_STATUSES} label="status" value={partner.status} onSave={(v) => saveField({ status: v })} badge={(v) => <PartnerStatusBadge value={v} />} />
              </Attr>
              <Attr icon={AtSign} label="Email">
                <InlineField type="email" value={partner.email} placeholder="Set email…" onSave={(v) => saveField({ email: v })}
                  render={(v) => <a href={`mailto:${v}`} onClick={(e) => e.stopPropagation()} className="text-info hover:underline">{v}</a>} />
              </Attr>
              <Attr icon={Phone} label="Phone">
                <InlineField type="tel" value={partner.phone} placeholder="Set phone…" onSave={(v) => saveField({ phone: v })} className="tabular" />
              </Attr>
              <Attr icon={StickyNote} label="Description">
                <InlineField value={partner.notes} placeholder="Set description…" onSave={(v) => saveField({ notes: v })} />
              </Attr>
              <RelationAttrs defs={relationDefs} row={partner} onSave={(key, v) => saveField({ [key]: v } as Partial<Partner>)} />
            </dl>
          </DetailsSection>

          <RelationSections defs={relationDefs} row={partner} />

          <DetailsSection title="System">
            <dl className="flex flex-col">
              <Attr icon={Calendar} label="Created"><span className="px-2 text-sm">{formatTimestamp(partner.created_at)}</span></Attr>
              <Attr icon={Clock} label="Updated"><span className="px-2 text-sm">{formatTimestamp(partner.updated_at)}</span></Attr>
            </dl>
          </DetailsSection>
        </aside>

        <main className={cn("flex min-w-0 flex-col", panel ? "shrink-0 border-t border-border" : "flex-1 overflow-y-auto")}>
          {!panel && <RecordTabs active={tab} onChange={setTab} tabs={[
            { key: "overview", label: "Overview", icon: LayoutGrid },
            { key: "deals", label: "Deals", icon: Handshake, count: dealCount },
            { key: "activity", label: "Activity", icon: ActivityIcon, count: activities.length },
            { key: "notes", label: "Notes", icon: StickyNote, count: activities.filter((a) => a.type === "note").length },
          ]} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {!panel && tab === "overview" && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={CircleDot} label="Status" empty="No status" value={partner.status && <PartnerStatusBadge value={partner.status} />} />
                  <Tile icon={Tag} label="Industry" empty="No industry" value={partner.industry} />
                  <Tile icon={AtSign} label="Email" empty="No email address" value={partner.email} />
                  <Tile icon={Handshake} label="Deals" empty="No deals" value={dealCount ? `${dealCount} ${dealCount === 1 ? "deal" : "deals"}` : undefined} />
                  <Tile icon={DollarSign} label="From won deals" empty="Nothing won yet"
                    value={won.length ? <span className="tabular">{formatMoney(earned.once)} + {formatMoney(earned.monthly)}/mo</span> : undefined} />
                  <Tile icon={Clock} label="Last activity" empty="No activity" value={recent[0] ? formatTimestamp(recent[0].created_at) : undefined} />
                </div>
              </section>
            )}

            {shows("deals") && <PartnerDeals partnerId={partner.id} deals={deals} />}

            {shows("activity") && <ActivityFeed activities={activities} />}

            {shows("notes") && (
              <NotesSection entity="partner" recordId={partner.id} activities={activities} open={noteOpen} onOpenChange={setNoteOpen}
                onAdded={async () => setActivities(await fetchActivities("partner", partner.id))} />
            )}
          </div>
        </main>
      </div>
    </div>
  );
}

/**
 * The deals a partner takes part in: each with its stage and value, and the
 * partner's share of it, once and each month, edited in place. + adds the
 * partner to another deal, × takes it off one; each change is its own write
 * to the deal's partners.
 */
function PartnerDeals({ partnerId, deals }: { partnerId: string; deals: Deal[] }) {
  const { setError, recordsChanged, stages } = useCrm();
  const [open, setOpen] = useState(false);
  const linked = deals.map((d) => d.id);

  const write = async (method: "PUT" | "DELETE", dealId: string, body?: Partial<DealPartner>) => {
    try {
      await api(method, `/api/deals/${encodeURIComponent(dealId)}/partners/${encodeURIComponent(partnerId)}`, body);
      await recordsChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const amount = (v: string) => Number(v) || 0;
  const money = (v: string) => <span className="tabular">{formatMoney(Number(v))}</span>;

  return (
    <section className="flex flex-col gap-3" aria-label="Deals">
      <div className="flex items-center justify-between">
        <h2 className="inline-flex items-center gap-2 text-sm font-medium">
          Deals <span className="rounded-xs bg-secondary px-1.5 text-xs tabular text-muted-foreground">{deals.length}</span>
        </h2>
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Add to a deal" title="Add to a deal"><Plus className="size-4" /></Button>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-64 p-0">
            <RecordPicker entity="deal" selected={linked} placeholder="Search deals…" onPick={(r) => void write(linked.includes(r.id) ? "DELETE" : "PUT", r.id, linked.includes(r.id) ? undefined : {})} />
          </PopoverContent>
        </Popover>
      </div>
      {deals.length === 0 ? (
        <p className="text-sm text-faint">Not part of any deal yet.</p>
      ) : (
        <div className="rounded-md bg-card shadow-edge">
          <table className="w-full table-fixed text-sm">
            <thead>
              <tr className="text-left text-[0.8125rem] text-muted-foreground">
                <th className="h-9 pl-3.5 font-normal">Deal</th>
                <th className="h-9 w-28 px-2 font-normal">Share once</th>
                <th className="h-9 w-28 px-2 font-normal">Share monthly</th>
                <th className="h-9 w-9" aria-label="Remove" />
              </tr>
            </thead>
            <tbody>
              {deals.map((d) => {
                const stage = stages.find((s) => s.key === d.stage);
                const share = d.partners?.find((p) => p.id === partnerId);
                return (
                  <tr key={d.id} className="group/deal border-t border-border">
                    <td className="min-w-0 py-2 pl-3.5 pr-2">
                      <RecordChip entity="deal" record={{ id: d.id, label: d.name, domain: null }} />
                      <span className="mt-0.5 flex items-center gap-1.5 truncate text-xs text-muted-foreground">
                        <span className={cn("size-2 shrink-0 rounded-full", colorClasses(stage?.color).dot)} />
                        {stage?.label ?? d.stage} · <span className="tabular">{formatMoney(d.value)}{d.recurring_value ? ` + ${formatMoney(d.recurring_value)}/mo` : ""}</span>
                      </span>
                    </td>
                    <td>
                      <InlineField type="number" value={share?.one_time_value ? String(share.one_time_value) : ""} placeholder="Once…" render={money}
                        onSave={(v) => write("PUT", d.id, { one_time_value: amount(v) })} />
                    </td>
                    <td>
                      <InlineField type="number" value={share?.recurring_value ? String(share.recurring_value) : ""} placeholder="Monthly…" render={(v) => <>{money(v)}<span className="text-muted-foreground">/mo</span></>}
                        onSave={(v) => write("PUT", d.id, { recurring_value: amount(v) })} />
                    </td>
                    <td>
                      <button
                        type="button"
                        onClick={() => void write("DELETE", d.id)}
                        aria-label={`Take off ${d.name}`}
                        title="Take off this deal"
                        className="inline-flex size-6 items-center justify-center rounded-xs text-muted-foreground opacity-0 hover:bg-secondary hover:text-foreground focus-visible:opacity-100 group-hover/deal:opacity-100 [@media(hover:none)]:opacity-100 [[data-agent]_&]:opacity-100"
                      >
                        <X className="size-3.5" />
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
