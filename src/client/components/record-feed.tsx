import { useEffect, useState, type FormEvent } from "react";
import { Calendar, Mail, MessageSquare, Phone, Plus, StickyNote, Trophy, type LucideIcon } from "lucide-react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import type { Activity, EntityType } from "@/types";

// A record's timeline, as every record page shows it: its activity, and its
// notes with the box to write one. Activities the CRM no longer makes
// (emails, meetings, Slack) keep their look on records that have them.

const ACTIVITY_STYLE: Record<string, { icon: LucideIcon; className: string; kind?: string }> = {
  call: { icon: Phone, className: "bg-info-tint text-info", kind: "Call" },
  message: { icon: MessageSquare, className: "bg-cat-1-tint text-cat-1-text", kind: "Message" },
  email: { icon: Mail, className: "bg-primary/10 text-primary", kind: "Email" },
  meeting: { icon: Calendar, className: "bg-cat-4-tint text-cat-4-text", kind: "Meeting" },
  note: { icon: StickyNote, className: "bg-secondary text-muted-foreground" },
  slack: { icon: MessageSquare, className: "bg-cat-1-tint text-cat-1-text", kind: "Slack" },
  stage_change: { icon: Trophy, className: "bg-success-tint text-success" },
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

function FeedItem({ a }: { a: Activity }) {
  const style = ACTIVITY_STYLE[a.type] ?? ACTIVITY_STYLE.note;
  const Icon = style.icon;
  const meta = metaLine(parseMeta(a.meta));
  return (
    <li className="flex items-start gap-3 px-3.5 py-2.5 [&+li]:border-t [&+li]:border-border">
      <span className={cn("mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full", style.className)}><Icon className="size-3" /></span>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        {(a.body || style.kind) && (
          <p className="whitespace-pre-wrap text-sm">
            {style.kind && <span className="font-medium">{style.kind}{a.body ? ": " : ""}</span>}
            {a.body}
          </p>
        )}
        {meta && <p className="text-xs text-muted-foreground">{meta}</p>}
      </div>
      <span className="shrink-0 tabular text-xs text-muted-foreground">{formatTimestamp(a.created_at)}</span>
    </li>
  );
}

/** Everything that happened on the record, newest first. */
export function ActivityFeed({ activities, empty = "No activity yet." }: { activities: Activity[]; empty?: string }) {
  const list = newestFirst(activities);
  return (
    <section className="flex flex-col gap-3" aria-label="Activity">
      <h2 className="inline-flex items-center gap-2 text-sm font-medium">
        Activity {list.length > 0 && <span className="rounded-xs bg-secondary px-1.5 text-xs tabular text-muted-foreground">{list.length}</span>}
      </h2>
      {list.length === 0 ? (
        <p className="text-sm text-faint">{empty}</p>
      ) : (
        <ul className="flex flex-col rounded-md bg-card shadow-edge">
          {list.map((a) => <FeedItem key={a.id} a={a} />)}
        </ul>
      )}
    </section>
  );
}

/**
 * The record's notes, newest first, and + to write one: a box that saves on
 * "Add note" (or Ctrl/⌘+Enter) and closes on Cancel (or Escape). `open`
 * lets the page open the box from elsewhere (an "Add note" button). A note
 * saved, `onAdded` re-reads the timeline.
 */
export function NotesSection({ entity, recordId, activities, open, onOpenChange, onAdded }: {
  entity: EntityType;
  recordId: string;
  activities: Activity[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: () => Promise<void> | void;
}) {
  const { addNote, setError } = useCrm();
  const [body, setBody] = useState("");
  const [saving, setSaving] = useState(false);
  useEffect(() => { if (open) setBody(""); }, [open]);
  const notes = newestFirst(activities.filter((a) => a.type === "note"));

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    const text = body.trim();
    if (!text) return;
    setSaving(true);
    try {
      await addNote(entity, recordId, text);
      onOpenChange(false);
      await onAdded();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the note");
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="flex flex-col gap-3" aria-label="Notes">
      <div className="flex h-9 items-center justify-between">
        <h2 className="inline-flex items-center gap-2 text-sm font-medium">
          Notes <span className="rounded-xs bg-secondary px-1.5 text-xs tabular text-muted-foreground">{notes.length}</span>
        </h2>
        <Button variant="ghost" size="icon" aria-label="Add a note" title="Add a note" aria-expanded={open} onClick={() => onOpenChange(!open)}>
          <Plus className="size-4" />
        </Button>
      </div>
      {open && (
        <form onSubmit={(e) => void save(e)} className="flex flex-col gap-2 rounded-md bg-card p-3 shadow-edge">
          <Textarea
            autoFocus
            aria-label="Note"
            placeholder="Write a note…"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void save(); }
              if (e.key === "Escape") { e.preventDefault(); onOpenChange(false); }
            }}
          />
          <div className="flex justify-end gap-2">
            <Button type="button" size="sm" variant="ghost" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" size="sm" disabled={saving || !body.trim()}>{saving ? "Saving…" : "Add note"}</Button>
          </div>
        </form>
      )}
      {notes.length === 0 ? (
        !open && <p className="text-sm text-faint">No notes yet.</p>
      ) : (
        <ul className="flex flex-col rounded-md bg-card shadow-edge">
          {notes.map((n) => (
            <li key={n.id} className="flex flex-col gap-1 px-3.5 py-2.5 [&+li]:border-t [&+li]:border-border">
              <p className="whitespace-pre-wrap text-sm">{n.body}</p>
              <span className="tabular text-xs text-muted-foreground">{formatTimestamp(n.created_at)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
