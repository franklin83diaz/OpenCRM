import { useEffect, useState } from "react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogClose } from "@/components/ui/dialog";
import { RelationInput } from "@/lib/relations";
import { CustomFieldsSection } from "@/lib/custom-fields";
import type { CustomerUser } from "@/types";

interface FormState {
  name: string;
  email: string;
  phone: string;
  company_id: string | null;
  notes: string;
}

/** A new customer user: who they are, the customer they use the product at, and its custom fields. `companyId` starts it at that company. */
export function CustomerUserDialog({ open, onOpenChange, companyId }: { open: boolean; onOpenChange: (open: boolean) => void; companyId?: string | null }) {
  const { addCustomerUser, setError, customFields } = useCrm();
  const userFields = customFields.filter((d) => d.entity_type === "customer_user");
  const empty = (): FormState => ({ name: "", email: "", phone: "", company_id: companyId ?? null, notes: "" });
  const [form, setForm] = useState<FormState>(empty);
  const [custom, setCustom] = useState<Record<string, unknown>>({});
  const [saving, setSaving] = useState(false);

  // Each opening starts empty.
  useEffect(() => {
    if (open) {
      setForm(empty());
      setCustom(Object.fromEntries(userFields.map((d) => [d.key, ""])));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((f) => ({ ...f, [key]: value }));

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const data: Partial<CustomerUser> = {
        name: form.name.trim(),
        email: form.email.trim(),
        phone: form.phone.trim(),
        company_id: form.company_id,
        notes: form.notes.trim(),
        custom,
      };
      await addCustomerUser(data);
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save customer user");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add customer user</DialogTitle>
        </DialogHeader>

        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <div className="section-label">Customer user</div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cu-name">Name</Label>
            <Input id="cu-name" required value={form.name} onChange={(e) => set("name", e.target.value)} />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label>Company</Label>
            <div className="rounded-sm bg-card shadow-edge">
              <RelationInput entity="company" value={form.company_id} placeholder="The customer they use the product at…" emptyLabel="No company"
                onChange={(id) => set("company_id", id)} />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cu-email">Email</Label>
              <Input id="cu-email" type="email" value={form.email} onChange={(e) => set("email", e.target.value)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cu-phone">Phone</Label>
              <Input id="cu-phone" value={form.phone} onChange={(e) => set("phone", e.target.value)} />
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cu-notes">Notes</Label>
            <Textarea id="cu-notes" value={form.notes} onChange={(e) => set("notes", e.target.value)} />
          </div>

          <CustomFieldsSection defs={userFields} values={custom}
            onChange={(key, v) => setCustom((cst) => ({ ...cst, [key]: v }))} />

          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" size="sm" variant="outline">Cancel</Button>
            </DialogClose>
            <Button type="submit" size="sm" disabled={saving}>
              {saving ? "Saving…" : "Add customer user"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
