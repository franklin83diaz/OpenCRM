import { useEffect, useState } from "react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogClose,
} from "@/components/ui/dialog";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@/components/ui/select";
import { Combobox } from "@/components/ui/combobox";
import { api } from "@/api";
import { CustomFieldsSection, readCustom } from "@/lib/custom-fields";
import { RelationSections } from "@/components/record-relations";
import { RecordsInput } from "@/lib/relations";
import { OwnerField } from "@/components/deals/owner-field";
import type { Deal } from "@/types";

// Radix Select forbids an empty-string item value, so we use a sentinel for the
// "None" contact and company options and map it back to null on submit.
const NO_CONTACT = "__none__";

interface FormState {
  name: string;
  contact_id: string;
  company_id: string;
  value: string;
  recurring_value: string;
  owner_id: string | null;
  stage: string;
  close_date: string;
  notes: string;
  partner_ids: string[];
}

function toForm(deal: Deal | undefined, defaultStage: string): FormState {
  return {
    name: deal?.name ?? "",
    contact_id: deal?.contact_id ?? "",
    company_id: deal?.company_id ?? "",
    value: deal?.value != null ? String(deal.value) : "",
    recurring_value: deal?.recurring_value ? String(deal.recurring_value) : "",
    owner_id: deal?.owner_id ?? null,
    stage: deal?.stage || defaultStage,
    close_date: deal?.close_date ?? "",
    notes: deal?.notes ?? "",
    partner_ids: (deal?.partners ?? []).map((p) => p.id),
  };
}

export function DealDialog({
  open,
  onOpenChange,
  deal,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  deal?: Deal;
}) {
  const { addDeal, updateDeal, setError, customFields, stages, boardDeals } = useCrm();
  const dealFields = customFields.filter((d) => d.entity_type === "deal");
  // A deal's one_to_many relations are listed here too, from the board's copy
  // (kept fresh as links change).
  const live = deal && (boardDeals.find((d) => d.id === deal.id) ?? deal);
  const [form, setForm] = useState<FormState>(() => toForm(deal, stages[0]?.key ?? ""));
  const [custom, setCustom] = useState<Record<string, unknown>>({});
  const [saving, setSaving] = useState(false);

  // Reset the form each time the dialog opens (create vs edit).
  useEffect(() => {
    if (open) {
      setForm(toForm(deal, stages[0]?.key ?? ""));
      setCustom(Object.fromEntries(dealFields.map((d) => [d.key, readCustom(deal, d.key) ?? ""])));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, deal]);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const data: Partial<Deal> = {
        name: form.name.trim(),
        contact_id: form.contact_id === "" ? null : form.contact_id,
        // Sent only when changed: left alone, the server gives a deal with no
        // company its contact's company.
        ...(form.company_id !== (deal?.company_id ?? "") ? { company_id: form.company_id || null } : {}),
        value: parseFloat(form.value) || 0,
        recurring_value: parseFloat(form.recurring_value) || 0,
        // Sent only when changed: a new deal is nobody's until someone takes it.
        ...((form.owner_id ?? null) !== (deal?.owner_id ?? null) ? { owner_id: form.owner_id } : {}),
        stage: form.stage,
        close_date: form.close_date,
        notes: form.notes.trim(),
        // Sent only when changed: they replace the deal's partners.
        ...(form.partner_ids.join() !== (deal?.partners ?? []).map((p) => p.id).join() ? { partner_ids: form.partner_ids } : {}),
        custom,
      };
      if (deal) await updateDeal(deal.id, data);
      else await addDeal(data);
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save deal");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{deal ? "Edit deal" : "Add deal"}</DialogTitle>
        </DialogHeader>

        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <div className="section-label">Deal</div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="name">Name</Label>
            <Input id="name" required value={form.name} onChange={(e) => set("name", e.target.value)} />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="contact">Contact</Label>
            <Combobox
              id="contact"
              value={form.contact_id === "" ? NO_CONTACT : form.contact_id}
              onChange={(v) => set("contact_id", v === NO_CONTACT ? "" : v)}
              placeholder="None"
              searchPlaceholder="Search contacts…"
              emptyText="No contacts found."
              options={[{ value: NO_CONTACT, label: "None" }]}
              valueLabel={
                deal ? `${deal.contact_first_name ?? ""} ${deal.contact_last_name ?? ""}`.trim() || undefined : undefined
              }
              onSearch={async (query) => {
                const { contacts } = await api<{ contacts: { id: string; first_name: string; last_name: string }[] }>(
                  "GET",
                  `/api/contacts?limit=20&search=${encodeURIComponent(query)}`,
                );
                return contacts.map((c) => ({ value: c.id, label: `${c.first_name} ${c.last_name}`.trim() || "—" }));
              }}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="company">Company</Label>
            <Combobox
              id="company"
              value={form.company_id === "" ? NO_CONTACT : form.company_id}
              onChange={(v) => set("company_id", v === NO_CONTACT ? "" : v)}
              placeholder="None"
              searchPlaceholder="Search companies…"
              emptyText="No companies found."
              options={[{ value: NO_CONTACT, label: "None" }]}
              valueLabel={deal?.company_name || undefined}
              onSearch={async (query) => {
                const { companies } = await api<{ companies: { id: string; name: string }[] }>(
                  "GET",
                  `/api/companies?limit=20&search=${encodeURIComponent(query)}`,
                );
                return companies.map((c) => ({ value: c.id, label: c.name || "—" }));
              }}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label>Partners</Label>
            <div className="rounded-sm bg-card shadow-edge">
              <RecordsInput modal entity="partner" label="partners" value={form.partner_ids} known={deal?.partners}
                onChange={(ids) => set("partner_ids", ids)} />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="value">One-time value</Label>
              <Input
                id="value"
                type="number"
                min="0"
                step="any"
                value={form.value}
                onChange={(e) => set("value", e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="recurring_value">Monthly value</Label>
              <Input
                id="recurring_value"
                type="number"
                min="0"
                step="any"
                placeholder="While they stay a customer"
                value={form.recurring_value}
                onChange={(e) => set("recurring_value", e.target.value)}
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label>Owner</Label>
              <OwnerField input modal value={form.owner_id} owner={deal?.owner} onChange={(id) => set("owner_id", id)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="stage">Stage</Label>
              <Select value={form.stage} onValueChange={(v) => set("stage", v)}>
                <SelectTrigger id="stage" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {stages.map((s) => (
                    <SelectItem key={s.key} value={s.key}>
                      {s.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="close_date">Close date</Label>
            <Input
              id="close_date"
              type="date"
              value={form.close_date}
              onChange={(e) => set("close_date", e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="notes">Notes</Label>
            <Textarea id="notes" value={form.notes} onChange={(e) => set("notes", e.target.value)} />
          </div>

          <CustomFieldsSection defs={dealFields} values={custom}
            onChange={(key, v) => setCustom((cst) => ({ ...cst, [key]: v }))} />

          {live && (
            <div className="-mx-4">
              <RelationSections defs={dealFields} row={live} />
            </div>
          )}

          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" size="sm" variant="outline">
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" size="sm" disabled={saving}>
              {saving ? "Saving…" : deal ? "Save" : "Add deal"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
