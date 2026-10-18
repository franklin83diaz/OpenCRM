import { useEffect, useState } from "react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogClose } from "@/components/ui/dialog";
import { CustomFieldsSection } from "@/lib/custom-fields";
import type { Partner } from "@/types";

interface FormState {
  name: string;
  domain: string;
  phone: string;
  email: string;
  notes: string;
}

const EMPTY: FormState = { name: "", domain: "", phone: "", email: "", notes: "" };

/** A new partner: its name, how to reach it, and its custom fields. Everything else is edited in place on its page. */
export function PartnerDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { addPartner, setError, customFields } = useCrm();
  const partnerFields = customFields.filter((d) => d.entity_type === "partner");
  const [form, setForm] = useState<FormState>(EMPTY);
  const [custom, setCustom] = useState<Record<string, unknown>>({});
  const [saving, setSaving] = useState(false);

  // Each opening starts empty.
  useEffect(() => {
    if (open) {
      setForm(EMPTY);
      setCustom(Object.fromEntries(partnerFields.map((d) => [d.key, ""])));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((f) => ({ ...f, [key]: value }));

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const data: Partial<Partner> = {
        name: form.name.trim(),
        domain: form.domain.trim(),
        phone: form.phone.trim(),
        email: form.email.trim(),
        notes: form.notes.trim(),
        custom,
      };
      await addPartner(data);
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save partner");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add partner</DialogTitle>
        </DialogHeader>

        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <div className="section-label">Partner</div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="partner-name">Name</Label>
            <Input id="partner-name" required value={form.name} onChange={(e) => set("name", e.target.value)} />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="partner-domain">Domain</Label>
              <Input id="partner-domain" placeholder="example.com" value={form.domain} onChange={(e) => set("domain", e.target.value)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="partner-email">Email</Label>
              <Input id="partner-email" type="email" value={form.email} onChange={(e) => set("email", e.target.value)} />
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="partner-phone">Phone</Label>
            <Input id="partner-phone" value={form.phone} onChange={(e) => set("phone", e.target.value)} />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="partner-notes">Notes</Label>
            <Textarea id="partner-notes" value={form.notes} onChange={(e) => set("notes", e.target.value)} />
          </div>

          <CustomFieldsSection defs={partnerFields} values={custom}
            onChange={(key, v) => setCustom((cst) => ({ ...cst, [key]: v }))} />

          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" size="sm" variant="outline">Cancel</Button>
            </DialogClose>
            <Button type="submit" size="sm" disabled={saving}>
              {saving ? "Saving…" : "Add partner"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
