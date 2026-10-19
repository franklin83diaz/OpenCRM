import { useState } from "react";
import { useCrm } from "@/context";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter, DialogClose } from "@/components/ui/dialog";
import { formatDate } from "@/lib/utils";

/**
 * "Remove as customer?": the company stops being a customer and nothing else
 * changes (POST /api/companies/:id/remove-customer). Its timeline keeps that
 * it was one. `company` null keeps it closed.
 */
export function RemoveCustomerDialog({ company, onClose }: {
  company: { id: string; name: string; customer_since?: string | null } | null;
  onClose: () => void;
}) {
  const { removeCustomer, setError } = useCrm();
  const [busy, setBusy] = useState(false);
  const confirm = async () => {
    if (!company) return;
    setBusy(true);
    try {
      await removeCustomer(company.id);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not remove the customer");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={!!company} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Remove {company?.name ?? "this company"} as a customer?</DialogTitle>
          <DialogDescription>
            It stays in Companies with its contacts, deals, customer users and tickets; only its Customer since and renewal dates are cleared.
            {company?.customer_since && <> Its activity will show it was a customer since {formatDate(company.customer_since)}.</>}
            {" "}A won deal, or a Customer since date set again, makes it a customer again.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose asChild>
            <Button size="sm" variant="ghost">Cancel</Button>
          </DialogClose>
          <Button size="sm" variant="destructive" onClick={() => void confirm()} disabled={busy}>
            {busy ? "Removing…" : "Remove as customer"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
