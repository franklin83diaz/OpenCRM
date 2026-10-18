import { useEffect, useState } from "react";
import { Ban, ChevronsUpDown, Pencil } from "lucide-react";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { RecordChip, RecordIcon, useRecordNames } from "@/lib/relations";
import { cn } from "@/lib/utils";
import type { EntityType, RelationRecord } from "@/types";

/**
 * One record picked among the few a scope allows (a customer's users, a
 * company's contacts), not the whole table. `load` reads them when the menu
 * opens. On a record page (`link`) the record opens its page and a pencil
 * beside it changes it; `input` draws it as a form field, `modal` inside a
 * dialog. `disabled` (with `hint`) while there is no scope yet.
 */
export function ScopedRecordField({ entity, value, known, load, onChange, label, placeholder, emptyLabel, link = false, input = false, modal = false, disabled = false, hint }: {
  entity: EntityType;
  value: string | null | undefined;
  /** The current record, named. */
  known?: RelationRecord | null;
  load: () => Promise<RelationRecord[]>;
  onChange: (id: string | null) => void | Promise<void>;
  /** What the field is, lower case ("customer user"). */
  label: string;
  placeholder: string;
  emptyLabel: string;
  link?: boolean;
  input?: boolean;
  modal?: boolean;
  disabled?: boolean;
  hint?: string;
}) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<RelationRecord[] | null>(null);
  useEffect(() => {
    if (!open) return;
    let alive = true;
    setItems(null);
    load().then((r) => { if (alive) setItems(r); }, () => { if (alive) setItems([]); });
    return () => { alive = false; };
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  // A value set before the menu ever opened (a form started at a record) is named from the shared cache.
  const name = useRecordNames(value && known?.id !== value ? [{ entity, id: value }] : []);
  const record = value ? (known?.id === value ? known : items?.find((r) => r.id === value) ?? name(entity, value)) : null;
  const pick = (id: string | null) => {
    setOpen(false);
    if (id !== (value ?? null)) void onChange(id);
  };
  const menu = (
    <PopoverContent align="start" className="w-64 p-0">
      <Command>
        <CommandInput autoFocus placeholder={`Search ${label}s…`} />
        <CommandList>
          {items === null
            ? <div className="py-4 text-center text-[13px] text-muted-foreground">Loading…</div>
            : <CommandEmpty>{items.length ? "No match." : `No ${label}s here yet.`}</CommandEmpty>}
          <CommandGroup>
            <CommandItem value={`__none ${emptyLabel}`} onSelect={() => pick(null)} aria-selected={!value} className={cn(!value && "bg-secondary font-medium")}>
              <Ban className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate">{emptyLabel}</span>
            </CommandItem>
            {(items ?? []).map((r) => (
              <CommandItem key={r.id} value={`${r.id} ${r.label}`} onSelect={() => pick(r.id)} aria-selected={r.id === value} className={cn(r.id === value && "bg-secondary font-medium")}>
                <RecordIcon entity={entity} record={r} />
                <span className="truncate">{r.label || "Untitled"}</span>
              </CommandItem>
            ))}
          </CommandGroup>
        </CommandList>
      </Command>
    </PopoverContent>
  );

  if (link && record) {
    const change = `Change ${label}`;
    return (
      <Popover modal={modal} open={open} onOpenChange={setOpen}>
        <PopoverAnchor asChild>
          <div className="group/rel flex h-8 w-full min-w-0 items-center gap-1 px-2">
            <RecordChip entity={entity} record={record} />
            <PopoverTrigger asChild>
              <button type="button" aria-label={change} title={change}
                className="inline-flex size-6 shrink-0 items-center justify-center rounded-sm bg-card text-muted-foreground shadow-edge opacity-0 transition-opacity hover:text-foreground focus-visible:opacity-100 group-hover/rel:opacity-100 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100 [[data-agent]_&]:opacity-100">
                <Pencil className="size-3.5" aria-hidden />
              </button>
            </PopoverTrigger>
          </div>
        </PopoverAnchor>
        {menu}
      </Popover>
    );
  }
  return (
    <Popover modal={modal} open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" disabled={disabled} aria-label={record ? `Change ${label}` : placeholder} title={disabled ? hint : undefined}
          className={input
            ? "flex h-8 w-full items-center justify-between gap-2 rounded-sm bg-card px-3 text-sm shadow-edge focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60"
            : "flex h-8 w-full min-w-0 items-center rounded-[0.5rem] px-2 text-left text-sm hover:bg-secondary disabled:cursor-not-allowed disabled:hover:bg-transparent"}>
          {record ? <RecordChip entity={entity} record={record} link={false} />
            : value ? <span className="text-muted-foreground">…</span>
            : <span className={input ? "text-muted-foreground" : "text-faint"}>{disabled && hint ? hint : placeholder}</span>}
          {input && <ChevronsUpDown className="size-4 shrink-0 opacity-50" />}
        </button>
      </PopoverTrigger>
      {menu}
    </Popover>
  );
}
