import { useEffect, useState } from "react";
import { useCrm } from "@/context";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogClose } from "@/components/ui/dialog";
import { RelationInput } from "@/lib/relations";
import { CustomFieldsSection } from "@/lib/custom-fields";
import { OptionField } from "@/components/value-fields";
import { OwnerField } from "@/components/deals/owner-field";
import { ScopedRecordField } from "@/components/scoped-record-field";
import { TICKET_PRIORITIES, TICKET_STATUSES, TicketPriorityBadge, TicketStatusBadge } from "@/components/tickets/ticket-options";
import type { CustomerUser, RelationRecord, Ticket, TicketPriority, TicketStatus } from "@/types";

/** A customer's users, by name, for a picker scoped to that customer. */
export async function customerUsersOf(companyId: string): Promise<RelationRecord[]> {
  const { customer_users } = await api<{ customer_users: CustomerUser[] }>("GET", `/api/customer-users?company_id=${encodeURIComponent(companyId)}&limit=100&sort=name&order=asc`);
  return customer_users.map((u) => ({ id: u.id, label: u.name, domain: null }));
}

interface FormState {
  subject: string;
  description: string;
  status: TicketStatus;
  priority: TicketPriority;
  company_id: string | null;
  customer_user_id: string | null;
  owner_id: string | null;
  due_date: string;
}

/**
 * A new ticket: what it is about, the customer it is for (and who there
 * reported it), how urgent, who works it, by when. `companyId` and
 * `customerUserId` start it at that customer, or that customer user. The
 * ticket made, `onCreated` gets it.
 */
export function TicketDialog({ open, onOpenChange, companyId, customerUserId, onCreated }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  companyId?: string | null;
  customerUserId?: string | null;
  onCreated?: (ticket: Ticket) => void;
}) {
  const { addTicket, setError, customFields } = useCrm();
  const ticketFields = customFields.filter((d) => d.entity_type === "ticket");
  const empty = (): FormState => ({
    subject: "", description: "", status: "open", priority: "medium",
    company_id: companyId ?? null, customer_user_id: customerUserId ?? null, owner_id: null, due_date: "",
  });
  const [form, setForm] = useState<FormState>(empty);
  const [custom, setCustom] = useState<Record<string, unknown>>({});
  const [saving, setSaving] = useState(false);

  // Each opening starts empty.
  useEffect(() => {
    if (open) {
      setForm(empty());
      setCustom(Object.fromEntries(ticketFields.map((d) => [d.key, ""])));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((f) => ({ ...f, [key]: value }));

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.company_id && !form.customer_user_id) {
      setError("Pick the customer the ticket is for");
      return;
    }
    setSaving(true);
    try {
      const ticket = await addTicket({
        subject: form.subject.trim(),
        description: form.description.trim(),
        status: form.status,
        priority: form.priority,
        company_id: form.company_id,
        customer_user_id: form.customer_user_id,
        owner_id: form.owner_id,
        due_date: form.due_date || null,
        custom,
      });
      onOpenChange(false);
      onCreated?.(ticket);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save ticket");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New ticket</DialogTitle>
        </DialogHeader>

        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tk-subject">Subject</Label>
            <Input id="tk-subject" required autoFocus value={form.subject} onChange={(e) => set("subject", e.target.value)} placeholder="e.g. Can't sign in" />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label>Customer</Label>
              <div className="rounded-sm bg-card shadow-edge">
                {/* Another customer's user can't stay: the reporter is picked again. */}
                <RelationInput customers entity="company" value={form.company_id} placeholder="The customer…" emptyLabel="No company"
                  onChange={(id) => setForm((f) => ({ ...f, company_id: id, customer_user_id: id === f.company_id ? f.customer_user_id : null }))} />
              </div>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>Reported by</Label>
              <ScopedRecordField input modal entity="customer_user" label="customer user" value={form.customer_user_id}
                placeholder="A customer user…" emptyLabel="Nobody in particular"
                disabled={!form.company_id} hint="Pick the customer first"
                load={() => customerUsersOf(form.company_id!)}
                onChange={(id) => set("customer_user_id", id)} />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="tk-status">Status</Label>
              <OptionField id="tk-status" input modal options={TICKET_STATUSES} label="status" value={form.status}
                onSave={(v) => set("status", v as TicketStatus)} badge={(v) => <TicketStatusBadge value={v} />} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="tk-priority">Priority</Label>
              <OptionField id="tk-priority" input modal options={TICKET_PRIORITIES} label="priority" value={form.priority}
                onSave={(v) => set("priority", v as TicketPriority)} badge={(v) => <TicketPriorityBadge value={v} />} />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label>Assignee</Label>
              <OwnerField input modal value={form.owner_id} onChange={(id) => set("owner_id", id)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="tk-due">Due date</Label>
              <Input id="tk-due" type="date" value={form.due_date} onChange={(e) => set("due_date", e.target.value)} />
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tk-description">Description</Label>
            <Textarea id="tk-description" value={form.description} onChange={(e) => set("description", e.target.value)} placeholder="What happened, what they need…" />
          </div>

          <CustomFieldsSection defs={ticketFields} values={custom}
            onChange={(key, v) => setCustom((cst) => ({ ...cst, [key]: v }))} />

          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" size="sm" variant="outline">Cancel</Button>
            </DialogClose>
            <Button type="submit" size="sm" disabled={saving || !form.subject.trim()}>
              {saving ? "Saving…" : "Create ticket"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
