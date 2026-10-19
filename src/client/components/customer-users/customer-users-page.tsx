import { useEffect, useState } from "react";
import { Search, Plus, Trash2, X } from "lucide-react";
import { useCrm } from "@/context";
import { PageHeader, Avatar, EntityIcon, EmptyState } from "@/components/shared";
import { CustomerUserDialog } from "@/components/customer-users/customer-user-dialog";
import { FilterBar } from "@/components/filter-bar";
import { fieldsFromDefs, sanitize } from "@/lib/filters";
import { useListView } from "@/hooks/use-list-view";
import { ViewSwitcher } from "@/components/view-switcher";
import { withQuery } from "@/hooks/use-router";
import { RecordTable, columnKind, type NameColumn, type RecordColumn } from "@/components/record-table";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter, DialogClose } from "@/components/ui/dialog";
import { CustomFieldDisplay, readCustom } from "@/lib/custom-fields";
import { relationColumn } from "@/lib/relations";
import { customFieldCopy, customFieldEdit, recordEdit, textEdit } from "@/components/cell-editors";
import { useTableView, useAggregates, listFilterQuery } from "@/hooks/use-table-view";
import { downloadCsv, exportListCsv, exportViewCsv } from "@/lib/csv";
import { ExportButton } from "@/components/export-button";
import type { CustomerUser } from "@/types";

const dash = <span className="text-muted-foreground">—</span>;

/** A person's avatar from one name: its first word and its last. */
function nameParts(name: string): [string, string] {
  const words = name.trim().split(/\s+/);
  return [words[0] ?? "", words.length > 1 ? words[words.length - 1] : ""];
}

// The customer users list: the people who use the product at each customer,
// with the company they belong to. `openId`, `viewParam` and `filtersParam`
// as ContactsPage.
export function CustomerUsersPage({ navigate, openId, viewParam, filtersParam }: { navigate: (to: string, opts?: { replace?: boolean }) => void; openId?: string; viewParam?: string; filtersParam?: string }) {
  const { customerUsers, customerUsersPag, stats, setCustomerUsersPage, setCustomerUsersSort, setCustomerUsersSearch, setCustomerUsersFilters, setCustomerUsersView, deleteCustomerUsers, updateCustomerUser, customFields, setError } = useCrm();
  const userFields = customFields.filter((d) => d.entity_type === "customer_user");
  const filterFields = fieldsFromDefs(
    [
      { key: "name", label: "Name", type: "text", column: "name" },
      { key: "email", label: "Email", type: "text", column: "email" },
      { key: "phone", label: "Phone", type: "text", column: "phone" },
      { key: "company_id", label: "Company", type: "relation", entity: "company", column: "company" },
      { key: "created_at", label: "Created", type: "date" },
    ],
    userFields,
  );
  const view = useTableView("customer_user", viewParam, { name: 220, email: 240, phone: 160, company: 200 });
  const listView = useListView({ entity: "customer_user", table: view, filtersParam, pag: customerUsersPag, setFilters: setCustomerUsersFilters, setView: setCustomerUsersView, navigate });

  const name: NameColumn<CustomerUser> = {
    label: "Name",
    sort: "name",
    href: (u) => `/customer-users?record=${encodeURIComponent(u.id)}`,
    text: (u) => u.name,
    render: (u) => {
      const [first, last] = nameParts(u.name);
      return (
        <>
          <Avatar firstName={first} lastName={last} className="size-5 text-[0.5625rem]" />
          <span className="truncate">{u.name || "—"}</span>
        </>
      );
    },
  };
  // A cell edited in place saves its one field; the list refetches.
  const saveCell = async (u: CustomerUser, patch: Record<string, unknown>) => {
    try {
      await updateCustomerUser(u.id, patch as Partial<CustomerUser>);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const columns: RecordColumn<CustomerUser>[] = [
    {
      key: "email", label: "Email", sort: "email", text: (u) => u.email, edit: textEdit("email", saveCell, { input: "email" }), copy: (u) => u.email,
      render: (u) => u.email
        ? <a href={`mailto:${u.email}`} onClick={(e) => e.stopPropagation()} className="text-foreground underline decoration-border underline-offset-2 hover:decoration-foreground">{u.email}</a>
        : dash,
    },
    { key: "phone", label: "Phone", sort: "phone", text: (u) => u.phone, edit: textEdit("phone", saveCell, { input: "tel" }), copy: (u) => u.phone, render: (u) => u.phone ? <span className="tabular">{u.phone}</span> : dash },
    {
      key: "company", label: "Company", sort: "company_id", text: (u) => u.company_name ?? "", edit: recordEdit("company_id", "company", "Company", saveCell, true),
      render: (u) => u.company_name
        ? <span className="flex min-w-0 items-center gap-2"><EntityIcon name={u.company_name} domain={u.company_domain} className="size-5" /><span className="truncate">{u.company_name}</span></span>
        : dash,
    },
    ...userFields.map((def): RecordColumn<CustomerUser> => def.field_type === "relation" ? { ...relationColumn<CustomerUser>(def), edit: customFieldEdit(def, saveCell, customFields) } : ({
      key: def.key, label: def.label, sort: def.key, kind: columnKind(def.field_type), edit: customFieldEdit(def, saveCell, customFields), copy: customFieldCopy(def),
      text: (u) => String(readCustom(u, def.key) ?? ""),
      render: (u) => <CustomFieldDisplay def={def} value={readCustom(u, def.key)} />,
    })),
  ];

  const [search, setSearch] = useState(customerUsersPag.search);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Debounce the search box → server-side filter.
  useEffect(() => {
    const t = setTimeout(() => setCustomerUsersSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]); // eslint-disable-line react-hooks/exhaustive-deps

  // The selection holds only rows on screen: paging, searching or deleting drops the rest.
  useEffect(() => {
    setSelected((prev) => {
      const next = new Set(customerUsers.filter((u) => prev.has(u.id)).map((u) => u.id));
      return next.size === prev.size ? prev : next;
    });
  }, [customerUsers]);

  // The footer totals, for the columns (and name) that have a calculation set.
  const totals = useAggregates(
    "/api/customer-users/aggregates",
    listFilterQuery(customerUsersPag),
    ["name", ...columns.map((c) => c.key)].flatMap((key) => { const op = view.aggregate(key); return op ? [{ key, op }] : []; }),
    customerUsers,
  );

  const openRecord = (id: string) => navigate(withQuery({ record: id }), { replace: !!openId });
  const totalPages = Math.max(1, Math.ceil(customerUsersPag.total / customerUsersPag.limit));

  const addButton = (
    <Button size="sm" onClick={() => setDialogOpen(true)}>
      <Plus className="size-4" />
      Add customer user
    </Button>
  );

  const exportSelected = () => {
    const rows = customerUsers.filter((u) => selected.has(u.id));
    const shown = columns.filter((c) => view.visible(c.key));
    downloadCsv("customer-users.csv", [name.label, ...shown.map((c) => c.label)], rows.map((r) => [name.text(r), ...shown.map((c) => c.text(r))]));
  };

  // Every record the list selects as it is now (filters, search, sort, the columns on screen).
  const exportAll = () => exportListCsv<CustomerUser>({
    filename: listView.view?.name ?? "customer-users", listPath: "/api/customer-users", rowsKey: "customer_users", name,
    columns: columns.filter((c) => view.visible(c.key)),
    query: { filters: sanitize(customerUsersPag.filters), sort: customerUsersPag.sort, order: customerUsersPag.order, search: customerUsersPag.search },
  }).catch((e) => setError(e instanceof Error ? e.message : "Could not export"));

  const confirmDelete = async () => {
    const ids = [...selected];
    setDeleting(true);
    try {
      await deleteCustomerUsers(ids);
      setConfirmOpen(false);
      if (openId && ids.includes(openId)) navigate(withQuery({ record: null }), { replace: true });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not delete");
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <PageHeader title="Customer users" count={stats.customerUsers}>
        {selected.size > 0 ? (
          <>
            <span className="tabular text-[0.8125rem] text-muted-foreground">{selected.size} selected</span>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
              <X className="size-4" />
              Clear
            </Button>
            <ExportButton selected={selected.size} pageRows={customerUsers.length} total={customerUsersPag.total} onSelected={exportSelected} onAll={exportAll} />
            <Button size="sm" variant="destructive" onClick={() => setConfirmOpen(true)}>
              <Trash2 className="size-4" />
              Delete
            </Button>
          </>
        ) : (
          <>
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Name, email or phone…"
                aria-label="Search customer users"
                className="h-7 w-56 pl-8"
              />
            </div>
            {addButton}
          </>
        )}
      </PageHeader>
      <FilterBar
        leading={
          <ViewSwitcher
            views={listView.views}
            current={listView.view}
            count={customerUsersPag.total}
            onOpen={listView.open}
            onCreate={listView.create}
            onRename={listView.rename}
            onDelete={listView.remove}
            onExport={(v) => exportViewCsv<CustomerUser>({ view: v, listPath: "/api/customer-users", rowsKey: "customer_users", name, columns }).catch((e) => setError(e instanceof Error ? e.message : "Could not export"))}
          />
        }
        fields={filterFields}
        filters={listView.filters}
        onChange={listView.setFilters}
        isVisible={view.visible}
        dirty={listView.dirty}
        onSave={listView.update}
        locked={listView.locked}
        onSaveAs={listView.create}
        onReset={listView.reset}
      />

      {/* The first-run empty state is for an empty list, not a filtered one. */}
      {customerUsers.length === 0 && !customerUsersPag.search && sanitize(listView.filters).length === 0 ? (
        <EmptyState title="No customer users yet. Add the people who use your product at your customers." action={addButton} />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="min-h-0 flex-1">
            <RecordTable
              rows={customerUsers}
              name={name}
              columns={columns}
              view={view}
              sorting={customerUsersPag}
              onSort={setCustomerUsersSort}
              openId={openId}
              onOpen={openRecord}
              selected={selected}
              onSelectedChange={setSelected}
              onCustomize={() => navigate("/settings/properties")}
              totals={totals}
              onAdd={() => setDialogOpen(true)}
            />
          </div>

          <footer className="flex items-center justify-between border-t border-border px-6 py-3">
            <span className="tabular text-[0.8125rem] text-muted-foreground">
              Page {customerUsersPag.page} of {totalPages}
            </span>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="outline" disabled={customerUsersPag.page <= 1} onClick={() => setCustomerUsersPage(customerUsersPag.page - 1)} aria-label="Previous page">
                Prev
              </Button>
              <Button size="sm" variant="outline" disabled={customerUsersPag.page >= totalPages} onClick={() => setCustomerUsersPage(customerUsersPag.page + 1)} aria-label="Next page">
                Next
              </Button>
            </div>
          </footer>
        </div>
      )}

      <CustomerUserDialog open={dialogOpen} onOpenChange={setDialogOpen} />

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete {selected.size === 1 ? "customer user" : `${selected.size} customer users`}?</DialogTitle>
            <DialogDescription>
              {selected.size === 1 ? "It" : "They"} will be permanently removed. This can't be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose asChild>
              <Button size="sm" variant="ghost">Cancel</Button>
            </DialogClose>
            <Button size="sm" variant="destructive" onClick={confirmDelete} disabled={deleting}>
              {deleting ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
