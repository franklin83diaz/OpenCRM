import { useState } from "react";
import { Plus, X } from "lucide-react";
import { useCrm } from "@/context";
import { api } from "@/api";
import { DetailsSection } from "@/components/record-page";
import { InlineField } from "@/components/ui/inline-field";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { RecordChip, RecordPicker } from "@/lib/relations";
import { formatMoney } from "@/lib/utils";
import type { Deal, DealPartner } from "@/types";

/**
 * The partners taking part in a deal, each with its share of it: once and
 * each month, edited in place. + puts another partner on the deal (a pick of
 * one already on it takes it off), × takes one off. Each change is its own
 * write (PUT or DELETE /api/deals/:id/partners/:partner_id), so two quick
 * ones never undo each other.
 */
export function DealPartners({ deal }: { deal: Deal }) {
  const { setError, recordsChanged } = useCrm();
  const [open, setOpen] = useState(false);
  const partners = deal.partners ?? [];
  const path = (partnerId: string) => `/api/deals/${encodeURIComponent(deal.id)}/partners/${encodeURIComponent(partnerId)}`;

  const write = async (method: "PUT" | "DELETE", partnerId: string, body?: Partial<DealPartner>) => {
    try {
      await api(method, path(partnerId), body);
      await recordsChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const toggle = (partnerId: string) =>
    partners.some((p) => p.id === partnerId) ? write("DELETE", partnerId) : write("PUT", partnerId, {});
  const amount = (v: string) => Number(v) || 0;
  const money = (v: string) => <span className="tabular">{formatMoney(Number(v))}</span>;

  return (
    <DetailsSection
      title={partners.length ? `Partners · ${partners.length}` : "Partners"}
      action={
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <button type="button" aria-label="Add partners" title="Add partners" className="inline-flex size-6 items-center justify-center rounded-xs hover:bg-secondary hover:text-foreground">
              <Plus className="size-3.5" />
            </button>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-64 p-0">
            <RecordPicker entity="partner" selected={partners.map((p) => p.id)} creatable placeholder="Search partners…" onPick={(r) => void toggle(r.id)} />
          </PopoverContent>
        </Popover>
      }
    >
      {partners.length === 0 ? (
        <p className="py-1 text-sm text-faint">No partners on this deal</p>
      ) : (
        <table className="w-full table-fixed text-sm">
          <thead>
            <tr className="text-left text-[0.8125rem] text-muted-foreground">
              <th className="h-7 font-normal">Partner</th>
              <th className="h-7 w-28 px-2 font-normal">One-time</th>
              <th className="h-7 w-28 px-2 font-normal">Monthly</th>
              <th className="h-7 w-7" aria-label="Remove" />
            </tr>
          </thead>
          <tbody>
            {partners.map((p) => (
              <tr key={p.id} className="group/row">
                <td className="h-9 min-w-0 pr-2"><RecordChip entity="partner" record={p} /></td>
                <td>
                  <InlineField type="number" value={p.one_time_value ? String(p.one_time_value) : ""} placeholder="One-time…" render={money}
                    onSave={(v) => write("PUT", p.id, { one_time_value: amount(v) })} />
                </td>
                <td>
                  <InlineField type="number" value={p.recurring_value ? String(p.recurring_value) : ""} placeholder="Monthly…" render={(v) => <>{money(v)}<span className="text-muted-foreground">/mo</span></>}
                    onSave={(v) => write("PUT", p.id, { recurring_value: amount(v) })} />
                </td>
                <td>
                  <button
                    type="button"
                    onClick={() => void write("DELETE", p.id)}
                    aria-label={`Take ${p.label} off this deal`}
                    title="Take off this deal"
                    className="inline-flex size-6 items-center justify-center rounded-xs text-muted-foreground opacity-0 hover:bg-secondary hover:text-foreground focus-visible:opacity-100 group-hover/row:opacity-100 [@media(hover:none)]:opacity-100 [[data-agent]_&]:opacity-100"
                  >
                    <X className="size-3.5" />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </DetailsSection>
  );
}
