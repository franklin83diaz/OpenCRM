import { useEffect, useRef, useState, type FormEvent } from "react";
import { BadgeX, Calendar, Mail, MessageSquare, Phone, StickyNote, Trophy, type LucideIcon } from "lucide-react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import type { Activity, EntityType } from "@/types";

// A record's timeline, as every record page shows it: its activity, notes
// among it, with the box to write one. Activities the CRM no longer makes
// (emails, meetings, Slack) keep their look on records that have them.

const ACTIVITY_STYLE: Record<string, { icon: LucideIcon; className: string; kind?: string }> = {
  call: { icon: Phone, className: "bg-info-tint text-info", kind: "Call" },
  message: { icon: MessageSquare, className: "bg-cat-1-tint text-cat-1-text", kind: "Message" },
  email: { icon: Mail, className: "bg-primary/10 text-primary", kind: "Email" },
  meeting: { icon: Calendar, className: "bg-cat-4-tint text-cat-4-text", kind: "Meeting" },
  note: { icon: StickyNote, className: "bg-secondary text-muted-foreground" },
  slack: { icon: MessageSquare, className: "bg-cat-1-tint text-cat-1-text", kind: "Slack" },
  stage_change: { icon: Trophy, className: "bg-success-tint text-success" },
  // A company that stopped being a customer: its body says since when it was one.
  customer_change: { icon: BadgeX, className: "bg-warning-tint text-warning" },
};

function parseMeta(meta: string): Record<string, unknown> {
  try {
    const v = JSON.parse(meta);
    return v && typeof v === "object" ? (v as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

function metaLine(meta: Record<string, unknown>): string | null {
  if (typeof meta.to === "string" && meta.to) return `to ${meta.to}`;
  if (typeof meta.channel === "string" && meta.channel) return `#${meta.channel}`;
  return null;
}

/** A stored UTC timestamp ("YYYY-MM-DD HH:MM:SS") in the reader's format. */
export function formatTimestamp(createdAt: string): string {
  const d = new Date(createdAt.replace(" ", "T") + "Z");
  return Number.isNaN(d.getTime()) ? createdAt : d.toLocaleString();
}

const newestFirst = (list: Activity[]) => [...list].sort((a, b) => b.created_at.localeCompare(a.created_at));

/** What an activity says: its body, or for a customer that ended, since when it was one in the reader's format. */
function bodyOf(a: Activity, meta: Record<string, unknown>): string {
  if (a.type === "customer_change" && typeof meta.customer_since === "string") {
    const since = new Date(`${meta.customer_since}T00:00:00`);
    if (!Number.isNaN(since.getTime())) return `No longer a customer (a customer since ${since.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })})`;
  }
  return a.body;
}

function FeedItem({ a }: { a: Activity }) {
  const style = ACTIVITY_STYLE[a.type] ?? ACTIVITY_STYLE.note;
  const Icon = style.icon;
  const parsed = parseMeta(a.meta);
  const meta = metaLine(parsed);
  const body = bodyOf(a, parsed);
  return (
    <li className="flex items-start gap-3 px-3.5 py-2.5 [&+li]:border-t [&+li]:border-border">
      <span className={cn("mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full", style.className)}><Icon className="size-3" /></span>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        {(body || style.kind) && (
          <p className="whitespace-pre-wrap text-sm">
            {style.kind && <span className="font-medium">{style.kind}{body ? ": " : ""}</span>}
            {body}
          </p>
        )}
        {meta && <p className="text-xs text-muted-foreground">{meta}</p>}
      </div>
      <span className="shrink-0 tabular text-xs text-muted-foreground">{formatTimestamp(a.created_at)}</span>
    </li>
  );
}

/**
 * A record's timeline, newest first: everything that happened on it (notes,
 * stage changes, calls…) with the box to write a note on top, always there.
 * "Notes" narrows it to the notes. Ctrl/⌘+Enter (or "Add note") saves,
 * Escape clears. `focus` changing puts the cursor in the box (an "Add note"
 * button elsewhere on the page). A note saved, `onAdded` re-reads the
 * timeline.
 */
export function RecordActivity({ entity, recordId, activities, onAdded, focus = 0 }: {
  entity: EntityType;
  recordId: string;
  activities: Activity[];
  onAdded: () => Promise<void> | void;
  focus?: number;
}) {
  const { addNote, setError } = useCrm();
  const [body, setBody] = useState("");
  const [focused, setFocused] = useState(false);
  const [saving, setSaving] = useState(false);
  const [only, setOnly] = useState<"all" | "notes">("all");
  const box = useRef<HTMLTextAreaElement>(null);
  useEffect(() => { if (focus) box.current?.focus(); }, [focus]);

  const all = newestFirst(activities);
  const notes = all.filter((a) => a.type === "note");
  const list = only === "notes" ? notes : all;
  const writing = focused || body.trim() !== "";

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    const text = body.trim();
    if (!text) return;
    setSaving(true);
    try {
      await addNote(entity, recordId, text);
      setBody("");
      await onAdded();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the note");
    } finally {
      setSaving(false);
    }
  };

  const filter = (key: "all" | "notes", label: string, n: number) => (
    <button type="button" aria-pressed={only === key} onClick={() => setOnly(key)}
      className={cn("inline-flex h-7 items-center gap-1.5 rounded-sm px-2 text-[0.8125rem]", only === key ? "bg-secondary font-medium text-foreground" : "text-muted-foreground hover:text-foreground")}>
      {label} <span className="tabular text-xs text-muted-foreground">{n}</span>
    </button>
  );

  return (
    <section className="flex flex-col gap-3" aria-label="Activity">
      <div className="flex h-9 items-center justify-between gap-2">
        <h2 className="text-sm font-medium">Activity</h2>
        <div className="flex items-center gap-0.5" aria-label="Show">
          {filter("all", "All", all.length)}
          {filter("notes", "Notes", notes.length)}
        </div>
      </div>

      <form onSubmit={(e) => void save(e)} className="flex flex-col gap-2">
        <Textarea
          ref={box}
          aria-label="Note"
          placeholder="Write a note…"
          value={body}
          rows={writing ? 3 : 1}
          className={cn("resize-none", writing ? "min-h-20" : "h-9 min-h-9 overflow-hidden py-1.5")}
          onChange={(e) => setBody(e.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void save(); }
            if (e.key === "Escape") { e.preventDefault(); setBody(""); box.current?.blur(); }
          }}
        />
        {writing && (
          <div className="flex items-center justify-end gap-2">
            <span className="mr-auto text-xs text-muted-foreground">Ctrl+Enter to save</span>
            <Button type="button" size="sm" variant="ghost" onMouseDown={(e) => e.preventDefault()} onClick={() => { setBody(""); box.current?.blur(); }}>Cancel</Button>
            <Button type="submit" size="sm" disabled={saving || !body.trim()} onMouseDown={(e) => e.preventDefault()}>{saving ? "Saving…" : "Add note"}</Button>
          </div>
        )}
      </form>

      {list.length === 0 ? (
        <p className="text-sm text-faint">{only === "notes" ? "No notes yet." : "Nothing here yet. Write a note to start the timeline."}</p>
      ) : (
        <ul className="flex flex-col rounded-md bg-card shadow-edge">
          {list.map((a) => <FeedItem key={a.id} a={a} />)}
        </ul>
      )}
    </section>
  );
}
