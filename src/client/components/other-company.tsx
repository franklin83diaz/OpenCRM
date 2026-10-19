import { go } from "@/hooks/use-router";
import { recordHref } from "@/lib/relations";
import type { RelationRecord } from "@/types";

/**
 * "(XYZ Management)" after a person who works at another company than the
 * record they're shown on (a deal's contact, a building's property manager):
 * a link to that company. `title` says who works where.
 */
export function OtherCompany({ company, title }: { company: RelationRecord; title?: string }) {
  const href = recordHref("company", company.id);
  if (!href) return null;
  return (
    <a
      href={href}
      onClick={(e) => { e.stopPropagation(); if (e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey) { e.preventDefault(); go(href); } }}
      title={title}
      className="min-w-0 truncate text-[0.8125rem] text-muted-foreground underline decoration-border underline-offset-2 hover:text-foreground hover:decoration-foreground"
    >
      ({company.label})
    </a>
  );
}
