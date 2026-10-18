import { useEffect, useState } from "react";
import { Ban, ChevronDown, UserCheck, Users } from "lucide-react";
import { useAuth } from "@/auth";
import { useCrm } from "@/context";
import { UserAvatar } from "@/components/shared";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { memberName } from "@/components/deals/owner-field";
import { isGroup, type FilterNode, type FilterRule } from "@/lib/filters";
import { cn } from "@/lib/utils";

/**
 * Whose deals the board shows: everyone's, the ones nobody has taken yet, or
 * one person's (you first). It is the board's "Owner" filter, set in one
 * pick: its chip shows beside it, and either one changes the other.
 */
export function OwnerSwitch({ filters, onChange }: { filters: FilterNode[]; onChange: (filters: FilterNode[]) => void }) {
  const { team, refetchTeam } = useCrm();
  const { user } = useAuth();
  const [open, setOpen] = useState(false);
  // The team loads with the app; one that failed then (an older API) is asked again here.
  useEffect(() => { if (!team.length) refetchTeam().catch(() => {}); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const rule = filters.find((n): n is FilterRule => !isGroup(n) && n.field === "owner_id");
  const ids = rule?.op === "is" && Array.isArray(rule.value) ? rule.value : [];
  const current: "all" | "none" | "some" | string =
    !rule ? "all" : rule.op === "is_empty" ? "none" : ids.length === 1 ? ids[0] : rule.op === "is" && ids.length === 0 ? "all" : "some";
  const person = team.find((m) => m.id === current);
  const label = current === "all" ? "Everyone" : current === "none" ? "Unassigned" : person ? (person.id === user.id ? "You" : memberName(person)) : "Several";

  const pick = (next: "all" | "none" | string) => {
    setOpen(false);
    const others = filters.filter((n) => isGroup(n) || n.field !== "owner_id");
    if (next === "all") onChange(others);
    else if (next === "none") onChange([...others, { field: "owner_id", op: "is_empty" }]);
    else onChange([...others, { field: "owner_id", op: "is", value: [next] }]);
  };
  // You first, then the others by name.
  const people = [...team].sort((a, b) => (a.id === user.id ? -1 : b.id === user.id ? 1 : 0));

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" aria-label={`Owner: ${label}. Change`} className="inline-flex h-7 max-w-[16rem] items-center gap-1.5 rounded-sm px-2 text-[0.8125rem] font-medium hover:bg-secondary">
          <UserCheck className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{label}</span>
          <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64 p-0">
        <Command>
          {people.length > 6 && <CommandInput autoFocus placeholder="Search people…" />}
          <CommandList>
            <CommandEmpty>No match.</CommandEmpty>
            <CommandGroup>
              <CommandItem value="__all Everyone" onSelect={() => pick("all")} aria-selected={current === "all"} className={cn(current === "all" && "bg-secondary font-medium")}>
                <Users className="size-3.5 shrink-0 text-muted-foreground" /> Everyone
              </CommandItem>
              <CommandItem value="__none Unassigned" onSelect={() => pick("none")} aria-selected={current === "none"} className={cn(current === "none" && "bg-secondary font-medium")}>
                <Ban className="size-3.5 shrink-0 text-muted-foreground" /> Unassigned
              </CommandItem>
              {people.map((m) => (
                <CommandItem key={m.id} value={`${m.id} ${m.name} ${m.email}`} onSelect={() => pick(m.id)} aria-selected={current === m.id} className={cn(current === m.id && "bg-secondary font-medium")}>
                  <UserAvatar name={m.name} email={m.email} className="size-5 text-[0.5625rem]" />
                  <span className="truncate">{memberName(m)}{m.id === user.id && <span className="text-muted-foreground"> (you)</span>}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
