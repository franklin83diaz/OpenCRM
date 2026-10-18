import { useEffect, useState } from "react";
import { Ban, ChevronsUpDown } from "lucide-react";
import { useCrm } from "@/context";
import { UserAvatar } from "@/components/shared";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { cn } from "@/lib/utils";
import type { TeamMember } from "@/types";

/** An account as people read it: its name, else its email. */
export const memberName = (m: TeamMember) => m.name || m.email;

/**
 * Who a deal is assigned to, as its own control: the account's avatar and
 * name, or "Unassigned", which opens the team to pick another or nobody.
 * `owner` names an account no longer on the team (disabled). `input` draws it
 * as a form field; `modal` inside a dialog, so the list scrolls.
 */
export function OwnerField({ value, owner, onChange, input = false, modal = false }: {
  value: string | null | undefined;
  owner?: TeamMember | null;
  onChange: (id: string | null) => void | Promise<void>;
  input?: boolean;
  modal?: boolean;
}) {
  const { team, refetchTeam } = useCrm();
  const [open, setOpen] = useState(false);
  // The team loads with the app; one that failed then is asked again when the list opens.
  useEffect(() => { if (open && !team.length) refetchTeam().catch(() => {}); }, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  const current = (value && (team.find((m) => m.id === value) ?? (owner?.id === value ? owner : null))) || null;
  const pick = (id: string | null) => {
    setOpen(false);
    if (id !== (value ?? null)) void onChange(id);
  };
  return (
    <Popover modal={modal} open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={current ? `Owner: ${memberName(current)}. Change` : "Assign an owner"}
          className={input
            ? "flex h-8 w-full items-center justify-between gap-2 rounded-sm bg-card px-3 text-sm shadow-edge focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2"
            : "flex h-8 w-full min-w-0 items-center gap-2 rounded-[0.5rem] px-2 text-left text-sm hover:bg-secondary"}
        >
          {current ? (
            <span className="flex min-w-0 items-center gap-2">
              <UserAvatar name={current.name} email={current.email} className="size-5 text-[0.5625rem]" />
              <span className="truncate">{memberName(current)}</span>
            </span>
          ) : (
            <span className={input ? "text-muted-foreground" : "text-faint"}>{value ? "…" : "Unassigned"}</span>
          )}
          {input && <ChevronsUpDown className="size-4 shrink-0 opacity-50" />}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64 p-0">
        <Command>
          <CommandInput autoFocus placeholder="Search people…" />
          <CommandList>
            <CommandEmpty>No match.</CommandEmpty>
            <CommandGroup>
              <CommandItem value="__unassigned Unassigned" onSelect={() => pick(null)} aria-selected={!value} className={cn(!value && "bg-secondary font-medium")}>
                <Ban className="size-3.5 shrink-0 text-muted-foreground" /> Unassigned
              </CommandItem>
              {team.map((m) => (
                <CommandItem key={m.id} value={`${m.id} ${m.name} ${m.email}`} onSelect={() => pick(m.id)} aria-selected={m.id === value} className={cn(m.id === value && "bg-secondary font-medium")}>
                  <UserAvatar name={m.name} email={m.email} className="size-5 text-[0.5625rem]" />
                  <span className="truncate">{memberName(m)}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
