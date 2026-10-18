/** Where a partnership stands, in order; a new partner starts as the first. */
export const PARTNER_STATUSES = [
  { value: "prospect", label: "Prospect" },
  { value: "onboarding", label: "Onboarding" },
  { value: "active", label: "Active" },
  { value: "inactive", label: "Inactive" },
];

/** A status as people read it: its label, else the value as stored. */
export const statusLabel = (value: string) => PARTNER_STATUSES.find((s) => s.value === value)?.label ?? value;
