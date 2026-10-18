import { cn, colorClasses, type ColorToken } from "@/lib/utils";

/** Where a partnership stands, in order; a new partner starts as the first.
 *  Each has the status colour of what it means: talking (info), getting
 *  going (warning), working deals (success), stopped (neutral). */
export const PARTNER_STATUSES: { value: string; label: string; tone: ColorToken }[] = [
  { value: "prospect", label: "Prospect", tone: "info" },
  { value: "onboarding", label: "Onboarding", tone: "warning" },
  { value: "active", label: "Active", tone: "success" },
  { value: "inactive", label: "Inactive", tone: "slate" },
];

/** A status as people read it: its label, else the value as stored. */
export const statusLabel = (value: string) => PARTNER_STATUSES.find((s) => s.value === value)?.label ?? value;

/** A partner's status as a pill in its own colour. */
export function PartnerStatusBadge({ value, className }: { value: string; className?: string }) {
  if (!value) return <span className="text-muted-foreground">—</span>;
  const status = PARTNER_STATUSES.find((s) => s.value === value);
  const c = colorClasses(status?.tone ?? "slate");
  return (
    <span className={cn("inline-block h-5 max-w-[12rem] truncate rounded-full px-2 align-middle text-[0.8125rem] font-medium leading-5", c.bg, c.text, className)}>
      {status?.label ?? value}
    </span>
  );
}
