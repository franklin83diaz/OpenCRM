import { useEffect, useState } from "react";
import { Ban, Pencil } from "lucide-react";
import { api } from "@/api";
import { useCrm } from "@/context";
import { DetailsSection } from "@/components/record-page";
import { Avatar } from "@/components/shared";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { OtherCompany } from "@/components/other-company";
import { RecordChip } from "@/lib/relations";
import { cn } from "@/lib/utils";
import type { Company, Contact, RelationRecord } from "@/types";

const fullName = (c: Contact) => `${c.first_name} ${c.last_name}`.trim();

/**
 * A company's main contact, who may work anywhere: a building's main contact
 * is often the property manager at the management firm. The menu lists the
 * company's own contacts first; typing searches every contact, each shown
 * with the company they work at. Set, the contact opens their page, the
 * company they work at follows when it isn't this one, and a pencil changes it.
 */
export function MainContactField({ companyId, companyName, value, contactCompany, onChange }: {
  companyId: string;
  companyName: string;
  /** The main contact, named; null for none. */
  value: RelationRecord | null | undefined;
  /** The company the main contact works at, when they have one. */
  contactCompany?: RelationRecord | null;
  onChange: (id: string | null) => void | Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [results, setResults] = useState<Contact[] | null>(null);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    const q = search.trim();
    const t = setTimeout(() => {
      const path = q
        ? `/api/contacts?search=${encodeURIComponent(q)}&limit=20&sort=first_name&order=asc`
        : `/api/contacts?company_id=${encodeURIComponent(companyId)}&limit=50&sort=first_name&order=asc`;
      api<{ contacts: Contact[] }>("GET", path).then((r) => { if (alive) setResults(r.contacts); }, () => { if (alive) setResults([]); });
    }, q ? 200 : 0);
    return () => { alive = false; clearTimeout(t); };
  }, [open, search, companyId]);
  useEffect(() => { if (!open) { setSearch(""); setResults(null); } }, [open]);

  const pick = (id: string | null) => {
    setOpen(false);
    if (id !== (value?.id ?? null)) void onChange(id);
  };
  const other = contactCompany && contactCompany.id !== companyId ? contactCompany : null;

  const menu = (
    <PopoverContent align="start" className="w-72 p-0">
      <Command shouldFilter={false}>
        <CommandInput autoFocus value={search} onValueChange={setSearch} placeholder="Search every contact…" />
        <CommandList>
          {results === null
            ? <div className="py-4 text-center text-[13px] text-muted-foreground">Loading…</div>
            : <CommandEmpty>{search.trim() ? "No contact matches." : `No contacts at ${companyName} yet: type to search every contact.`}</CommandEmpty>}
          {!search.trim() && (
            <CommandGroup>
              <CommandItem value="__none" onSelect={() => pick(null)} aria-selected={!value} className={cn(!value && "bg-secondary font-medium")}>
                <Ban className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="truncate">No main contact</span>
              </CommandItem>
            </CommandGroup>
          )}
          {results && results.length > 0 && (
            <CommandGroup heading={search.trim() ? "Every contact" : `At ${companyName}`}>
              {results.map((c) => {
                const on = c.id === value?.id;
                return (
                  <CommandItem key={c.id} value={c.id} onSelect={() => pick(c.id)} aria-selected={on} className={cn(on && "bg-secondary font-medium")}>
                    <Avatar firstName={c.first_name} lastName={c.last_name} className="size-4 text-[0.4375rem]" />
                    <span className="truncate">{fullName(c) || "Untitled"}</span>
                    {search.trim() && c.company_name && <span className="ml-auto shrink-0 truncate pl-2 text-xs text-muted-foreground">{c.company_name}</span>}
                  </CommandItem>
                );
              })}
            </CommandGroup>
          )}
        </CommandList>
      </Command>
    </PopoverContent>
  );

  if (value) {
    return (
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverAnchor asChild>
          <div className="group/rel flex h-8 w-full min-w-0 items-center gap-1 px-2">
            <RecordChip entity="contact" record={value} />
            {other && <OtherCompany company={other} title={`${value.label} works at ${other.label}`} />}
            <PopoverTrigger asChild>
              <button type="button" aria-label="Change main contact" title="Change main contact"
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
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" aria-label="Set main contact…" className="flex h-8 w-full min-w-0 items-center rounded-[0.5rem] px-2 text-left text-sm hover:bg-secondary">
          <span className="text-faint">Set main contact…</span>
        </button>
      </PopoverTrigger>
      {menu}
    </Popover>
  );
}

/**
 * On a contact's page: the companies they are the main contact of, their own
 * or others (the buildings a property manager looks after). Nothing when
 * there are none.
 */
export function MainContactOf({ contactId }: { contactId: string }) {
  const { changes } = useCrm();
  const [companies, setCompanies] = useState<Company[]>([]);
  useEffect(() => {
    let alive = true;
    const filters = encodeURIComponent(JSON.stringify([{ field: "main_contact_id", op: "is", value: [contactId] }]));
    api<{ companies: Company[] }>("GET", `/api/companies?filters=${filters}&limit=100&sort=name&order=asc`)
      .then((r) => { if (alive) setCompanies(r.companies); }, () => { if (alive) setCompanies([]); });
    return () => { alive = false; };
  }, [contactId, changes]);
  if (companies.length === 0) return null;
  return (
    <DetailsSection title={`Main contact of · ${companies.length}`}>
      <ul className="flex flex-wrap gap-1.5 py-1">
        {companies.map((c) => (
          <li key={c.id}><RecordChip entity="company" record={{ id: c.id, label: c.name, domain: c.domain }} /></li>
        ))}
      </ul>
    </DetailsSection>
  );
}
