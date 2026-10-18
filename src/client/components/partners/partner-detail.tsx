import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Mail, LayoutGrid, Activity as ActivityIcon, Phone, Globe, AtSign, StickyNote, Calendar, Clock, Handshake, Plus, X } from "lucide-react";
import { useCrm } from "@/context";
import { api } from "@/api";
import { EntityIcon } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { InlineField } from "@/components/ui/inline-field";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { RecordTopBar, Attr, DetailsSection, Tile, RecordTabs } from "@/components/record-page";
import { RelationAttrs, RelationSections } from "@/components/record-relations";
import { RecordChip, RecordPicker } from "@/lib/relations";
import { cn, colorClasses, formatMoney } from "@/lib/utils";
import type { Activity, Deal, Partner } from "@/types";

function formatTimestamp(createdAt: string): string {
  const d = new Date(createdAt.replace(" ", "T") + "Z");
  return Number.isNaN(d.getTime()) ? createdAt : d.toLocaleString();
}

// The partner record page: same anatomy as a company (DESIGN.md → Record
// pages), with the deals the partner takes part in. Every value is the
// control; there is no edit mode. `panel` renders it in the side panel beside
// the partners list.
export function PartnerDetail({ id, navigate, panel = false }: { id: string; navigate: (to: string) => void; panel?: boolean }) {
  const { fetchPartner, updatePartner, fetchActivities, setError, customFields, changes } = useCrm();
  const relationDefs = customFields.filter((d) => d.entity_type === "partner" && d.field_type === "relation");
  const [partner, setPartner] = useState<Partner | null | undefined>(undefined);
  const [deals, setDeals] = useState<Deal[]>([]);
  const [activities, setActivities] = useState<Activity[]>([]);

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
  const host = (partner.domain || "").replace(/^https?:\/\//i, "").replace(/\/.*$/, "");
  const dealCount = partner.deal_count ?? deals.length;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {!panel && <RecordTopBar onClose={() => navigate("/partners")} crumb="Partners" />}

      <div className={cn("flex min-h-0 flex-1", panel && "flex-col overflow-y-auto")}>
        <aside className={cn("flex shrink-0 flex-col", !panel && "w-[31.25rem] overflow-y-auto border-r border-border")}>
          <div className="flex items-center gap-3 px-4 pt-4">
            <EntityIcon name={partner.name} domain={partner.domain} className="size-9 rounded-md text-sm" />
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
              <Attr icon={Globe} label="Domain">
                <InlineField value={partner.domain} placeholder="Set domain…" onSave={(v) => saveField({ domain: v })}
                  render={(v) => <a href={`https://${v.replace(/^https?:\/\//i, "")}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="text-info hover:underline">{v}</a>} />
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
          {!panel && <RecordTabs tabs={[
            { key: "overview", label: "Overview", icon: LayoutGrid },
            { key: "deals", label: "Deals", icon: Handshake, count: dealCount },
            { key: "activity", label: "Activity", icon: ActivityIcon, count: activities.length },
          ]} />}

          <div className={cn("flex flex-col gap-8", panel ? "p-4" : "p-6")}>
            {!panel && (
              <section className="flex flex-col gap-3">
                <h2 className="text-sm font-medium">Highlights</h2>
                <div className="grid grid-cols-3 gap-3">
                  <Tile icon={Globe} label="Domain" empty="No domain" value={host && <a href={`https://${host}`} target="_blank" rel="noreferrer" className="text-info hover:underline">{host}</a>} />
                  <Tile icon={Handshake} label="Deals" empty="No deals" value={dealCount ? `${dealCount} ${dealCount === 1 ? "deal" : "deals"}` : undefined} />
                  <Tile icon={AtSign} label="Email" empty="No email address" value={partner.email} />
                  <Tile icon={Phone} label="Phone" empty="No phone" value={partner.phone} />
                  <Tile icon={Clock} label="Last activity" empty="No activity" value={recent[0] ? formatTimestamp(recent[0].created_at) : undefined} />
                  <Tile icon={Calendar} label="Created" empty="Unknown" value={formatTimestamp(partner.created_at)} />
                </div>
              </section>
            )}

            <PartnerDeals partnerId={partner.id} deals={deals} />

            <section className="flex flex-col gap-3">
              <h2 className="text-sm font-medium">Activity</h2>
              {recent.length === 0 ? (
                <p className="text-sm text-faint">No activity yet.</p>
              ) : (
                <ul className="flex flex-col rounded-md bg-card shadow-edge">
                  {recent.slice(0, 6).map((a) => (
                    <li key={a.id} className="flex items-start gap-3 px-3.5 py-2.5 [&+li]:border-t [&+li]:border-border">
                      <span className="mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-secondary text-muted-foreground"><StickyNote className="size-3" /></span>
                      <p className="min-w-0 flex-1 whitespace-pre-wrap text-sm">{a.body}</p>
                      <span className="shrink-0 tabular text-xs text-muted-foreground">{formatTimestamp(a.created_at)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>
        </main>
      </div>
    </div>
  );
}

/**
 * The deals a partner takes part in: each with its stage and value, + to
 * add the partner to another deal and × to take it off one. Both write the
 * deal's partner_ids.
 */
function PartnerDeals({ partnerId, deals }: { partnerId: string; deals: Deal[] }) {
  const { fetchDeal, updateDeal, setError, stages } = useCrm();
  const [open, setOpen] = useState(false);
  const linked = deals.map((d) => d.id);

  // Reads the deal for its partners as they are now, then writes them back with this one added or taken off.
  const toggle = async (dealId: string) => {
    try {
      const deal = await fetchDeal(dealId);
      if (!deal) throw new Error("That deal no longer exists");
      const ids = (deal.partners ?? []).map((p) => p.id);
      await updateDeal(dealId, { partner_ids: ids.includes(partnerId) ? ids.filter((x) => x !== partnerId) : [...ids, partnerId] });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };

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
            <RecordPicker entity="deal" selected={linked} placeholder="Search deals…" onPick={(r) => void toggle(r.id)} />
          </PopoverContent>
        </Popover>
      </div>
      {deals.length === 0 ? (
        <p className="text-sm text-faint">Not part of any deal yet.</p>
      ) : (
        <ul className="flex flex-col rounded-md bg-card shadow-edge">
          {deals.map((d) => {
            const stage = stages.find((s) => s.key === d.stage);
            return (
              <li key={d.id} className="group/deal flex h-11 items-center gap-3 px-3.5 [&+li]:border-t [&+li]:border-border">
                <span className="min-w-0 flex-1"><RecordChip entity="deal" record={{ id: d.id, label: d.name, domain: null }} /></span>
                <span className="inline-flex shrink-0 items-center gap-1.5 text-[0.8125rem] text-muted-foreground">
                  <span className={cn("size-2 rounded-full", colorClasses(stage?.color).dot)} />
                  {stage?.label ?? d.stage}
                </span>
                <span className="w-24 shrink-0 text-right tabular text-sm">{formatMoney(d.value)}</span>
                <button
                  type="button"
                  onClick={() => void toggle(d.id)}
                  aria-label={`Take off ${d.name}`}
                  title="Take off this deal"
                  className="inline-flex size-6 shrink-0 items-center justify-center rounded-xs text-muted-foreground opacity-0 hover:bg-secondary hover:text-foreground focus-visible:opacity-100 group-hover/deal:opacity-100 [@media(hover:none)]:opacity-100 [[data-agent]_&]:opacity-100"
                >
                  <X className="size-3.5" />
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
