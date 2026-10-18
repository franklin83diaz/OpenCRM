import { useState, type ReactNode } from "react";
import { ChevronsUpDown } from "lucide-react";
import { CategoryBadge } from "@/components/shared";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { OptionMenu, ValuesMenu } from "@/components/cell-editors";
import type { EntityType } from "@/types";

// One-value controls for a record page or a form: the value as a pill, which
// opens a menu to change it. `input` draws them as a form field; `modal`
// inside a dialog, so the menu scrolls.

const inlineTrigger = "flex h-8 w-full items-center rounded-[0.5rem] px-2 text-left text-sm hover:bg-secondary";
const inputTrigger = "flex h-8 w-full items-center justify-between gap-2 rounded-sm bg-card px-3 text-sm shadow-edge ring-offset-background focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2";

function PillField({ id, label, value, shown, input, modal, menu }: {
  id?: string;
  label: string;
  value: string;
  shown: ReactNode;
  input: boolean;
  modal: boolean;
  menu: (close: () => void) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Popover modal={modal} open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button id={id} type="button" aria-label={value ? `Change ${label}` : `Set ${label}`} className={input ? inputTrigger : inlineTrigger}>
          {value ? shown : <span className={input ? "text-muted-foreground" : "text-faint"}>{input ? "None" : `Set ${label}…`}</span>}
          {input && <ChevronsUpDown className="size-4 shrink-0 opacity-50" />}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64 p-0">{menu(() => setOpen(false))}</PopoverContent>
    </Popover>
  );
}

/** A free-text field picked from the values it already holds, or a new one typed in (an industry). */
export function ValuesField({ entity, field, label, value, onSave, id, input = false, modal = false }: {
  entity: EntityType;
  field: string;
  /** What the field is, lower case ("industry"). */
  label: string;
  value: string;
  onSave: (value: string) => void | Promise<void>;
  id?: string;
  input?: boolean;
  modal?: boolean;
}) {
  return (
    <PillField id={id} label={label} value={value} input={input} modal={modal} shown={<CategoryBadge value={value} />}
      menu={(close) => (
        <ValuesMenu entity={entity} field={field} value={value || null} emptyLabel={`No ${label}`}
          onPick={(v) => { close(); if ((v ?? "") !== value) void onSave(v ?? ""); }} />
      )} />
  );
}

/** One of a fixed list of options (a status); it can't be left empty. `badge` draws a value (its own colours). */
export function OptionField({ options, label, value, onSave, badge, id, input = false, modal = false }: {
  options: { value: string; label: string }[];
  badge?: (value: string) => ReactNode;
  /** What the field is, lower case ("status"). */
  label: string;
  value: string;
  onSave: (value: string) => void | Promise<void>;
  id?: string;
  input?: boolean;
  modal?: boolean;
}) {
  const current = options.find((o) => o.value === value);
  return (
    <PillField id={id} label={label} value={value} input={input} modal={modal} shown={badge ? badge(value) : <CategoryBadge value={current?.label ?? value} />}
      menu={(close) => (
        <OptionMenu options={options} value={value || null} render={badge}
          onPick={(v) => { close(); if (v && v !== value) void onSave(v); }} />
      )} />
  );
}
