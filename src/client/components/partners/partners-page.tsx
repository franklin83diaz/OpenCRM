import { useEffect, useState } from "react";
import { Search, Plus, Trash2, X } from "lucide-react";
import { useCrm } from "@/context";
import { PageHeader, EntityIcon, EmptyState, CategoryBadge } from "@/components/shared";
import { PARTNER_STATUSES, PartnerStatusBadge, statusLabel } from "@/components/partners/statuses";
import { PartnerDialog } from "@/components/partners/partner-dialog";
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
import { customFieldCopy, customFieldEdit, optionEdit, textEdit, valuesEdit } from "@/components/cell-editors";
import { useTableView, useAggregates, listFilterQuery } from "@/hooks/use-table-view";
import { downloadCsv, exportListCsv, exportViewCsv } from "@/lib/csv";
import { ExportButton } from "@/components/export-button";
import type { Partner } from "@/types";

const dash = <span className="text-muted-foreground">—</span>;

// The partners list: the companies' list without contacts and import, with
// each partner's status and how many deals it takes part in. `openId`,
// `viewParam` and `filtersParam` as ContactsPage.
export function PartnersPage({ navigate, openId, viewParam, filtersParam }: { navigate: (to: string, opts?: { replace?: boolean }) => void; openId?: string; viewParam?: string; filtersParam?: string }) {
  const { partners, partnersPag, stats, setPartnersPage, setPartnersSort, setPartnersSearch, setPartnersFilters, setPartnersView, deletePartners, updatePartner, customFields, setError } = useCrm();
  const partnerFields = customFields.filter((d) => d.entity_type === "partner");
  const filterFields = fieldsFromDefs(
    [
      { key: "name", label: "Name", type: "text", column: "name" },
      { key: "industry", label: "Industry", type: "text", column: "industry" },
      { key: "status", label: "Status", type: "enum", column: "status", options: PARTNER_STATUSES },
      { key: "email", label: "Email", type: "text", column: "email" },
      { key: "phone", label: "Phone", type: "text", column: "phone" },
      { key: "deals", label: "Deals", type: "relation", entity: "deal", column: "deals" },
      { key: "count:deals", label: "Deals count", type: "number", column: "deals" },
      { key: "created_at", label: "Created", type: "date" },
    ],
    partnerFields,
  );
  const view = useTableView("partner", viewParam, { name: 220, industry: 160, status: 130, email: 220, phone: 160, deals: 112 });
  const listView = useListView({ entity: "partner", table: view, filtersParam, pag: partnersPag, setFilters: setPartnersFilters, setView: setPartnersView, navigate });

  const name: NameColumn<Partner> = {
    label: "Name",
    sort: "name",
    href: (p) => `/partners?record=${encodeURIComponent(p.id)}`,
    text: (p) => p.name,
    render: (p) => (
      <>
        <EntityIcon name={p.name} className="size-5" />
        <span className="truncate">{p.name || "—"}</span>
      </>
    ),
  };
  // A cell edited in place saves its one field; the list refetches.
  const saveCell = async (p: Partner, patch: Record<string, unknown>) => {
    try {
      await updatePartner(p.id, patch as Partial<Partner>);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const columns: RecordColumn<Partner>[] = [
    { key: "industry", label: "Industry", sort: "industry", text: (p) => p.industry, edit: valuesEdit("partner", "industry", "Industry", saveCell), render: (p) => <CategoryBadge value={p.industry} /> },
    {
      key: "status", label: "Status", sort: "status", text: (p) => statusLabel(p.status),
      edit: optionEdit("status", PARTNER_STATUSES, saveCell, undefined, (v) => <PartnerStatusBadge value={v} />),
      render: (p) => <PartnerStatusBadge value={p.status} />,
    },
    { key: "email", label: "Email", sort: "email", text: (p) => p.email, edit: textEdit("email", saveCell, { input: "email" }), render: (p) => p.email ? <span className="truncate">{p.email}</span> : dash },
    { key: "phone", label: "Phone", sort: "phone", text: (p) => p.phone, edit: textEdit("phone", saveCell, { input: "tel" }), render: (p) => p.phone ? <span className="tabular truncate">{p.phone}</span> : dash },
    ...partnerFields.map((def): RecordColumn<Partner> => def.field_type === "relation" ? { ...relationColumn<Partner>(def), edit: customFieldEdit(def, saveCell, customFields) } : ({
      key: def.key, label: def.label, sort: def.key, kind: columnKind(def.field_type), edit: customFieldEdit(def, saveCell, customFields), copy: customFieldCopy(def),
      text: (p) => String(readCustom(p, def.key) ?? ""),
      render: (p) => <CustomFieldDisplay def={def} value={readCustom(p, def.key)} />,
    })),
    {
      key: "deals", label: "Deals", align: "right", kind: "number", sort: "count:deals", text: (p) => String(p.deal_count ?? 0),
      render: (p) => <span className="tabular">{p.deal_count ?? 0}</span>,
    },
  ];

  const [search, setSearch] = useState(partnersPag.search);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Debounce the search box → server-side filter.
  useEffect(() => {
    const t = setTimeout(() => setPartnersSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]); // eslint-disable-line react-hooks/exhaustive-deps

  // The selection holds only rows on screen: paging, searching or deleting drops the rest.
  useEffect(() => {
    setSelected((prev) => {
      const next = new Set(partners.filter((p) => prev.has(p.id)).map((p) => p.id));
      return next.size === prev.size ? prev : next;
    });
  }, [partners]);

  // The footer totals, for the columns (and name) that have a calculation set.
  const totals = useAggregates(
    "/api/partners/aggregates",
    listFilterQuery(partnersPag),
    ["name", ...columns.map((c) => c.key)].flatMap((key) => { const op = view.aggregate(key); return op ? [{ key, op }] : []; }),
    partners,
  );

  const openRecord = (id: string) => navigate(withQuery({ record: id }), { replace: !!openId });
  const totalPages = Math.max(1, Math.ceil(partnersPag.total / partnersPag.limit));

  const addButton = (
    <Button size="sm" onClick={() => setDialogOpen(true)}>
      <Plus className="size-4" />
      Add partner
    </Button>
  );

  const exportSelected = () => {
    const rows = partners.filter((p) => selected.has(p.id));
    const shown = columns.filter((c) => view.visible(c.key));
    downloadCsv("partners.csv", [name.label, ...shown.map((c) => c.label)], rows.map((r) => [name.text(r), ...shown.map((c) => c.text(r))]));
  };

  // Every record the list selects as it is now (filters, search, sort, the columns on screen).
  const exportAll = () => exportListCsv<Partner>({
    filename: listView.view?.name ?? "partners", listPath: "/api/partners", rowsKey: "partners", name,
    columns: columns.filter((c) => view.visible(c.key)),
    query: { filters: sanitize(partnersPag.filters), sort: partnersPag.sort, order: partnersPag.order, search: partnersPag.search },
  }).catch((e) => setError(e instanceof Error ? e.message : "Could not export"));

  const confirmDelete = async () => {
    const ids = [...selected];
    setDeleting(true);
    try {
      await deletePartners(ids);
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
      <PageHeader title="Partners" count={stats.partners}>
        {selected.size > 0 ? (
          <>
            <span className="tabular text-[0.8125rem] text-muted-foreground">{selected.size} selected</span>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
              <X className="size-4" />
              Clear
            </Button>
            <ExportButton selected={selected.size} pageRows={partners.length} total={partnersPag.total} onSelected={exportSelected} onAll={exportAll} />
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
                placeholder="Search partners…"
                aria-label="Search partners"
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
            count={partnersPag.total}
            onOpen={listView.open}
            onCreate={listView.create}
            onRename={listView.rename}
            onDelete={listView.remove}
            onExport={(v) => exportViewCsv<Partner>({ view: v, listPath: "/api/partners", rowsKey: "partners", name, columns }).catch((e) => setError(e instanceof Error ? e.message : "Could not export"))}
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
      {partners.length === 0 && !partnersPag.search && sanitize(listView.filters).length === 0 ? (
        <EmptyState title="No partners yet. Add the first firm you work deals with." action={addButton} />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="min-h-0 flex-1">
            <RecordTable
              rows={partners}
              name={name}
              columns={columns}
              view={view}
              sorting={partnersPag}
              onSort={setPartnersSort}
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
              Page {partnersPag.page} of {totalPages}
            </span>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="outline" disabled={partnersPag.page <= 1} onClick={() => setPartnersPage(partnersPag.page - 1)} aria-label="Previous page">
                Prev
              </Button>
              <Button size="sm" variant="outline" disabled={partnersPag.page >= totalPages} onClick={() => setPartnersPage(partnersPag.page + 1)} aria-label="Next page">
                Next
              </Button>
            </div>
          </footer>
        </div>
      )}

      <PartnerDialog open={dialogOpen} onOpenChange={setDialogOpen} />

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete {selected.size === 1 ? "partner" : `${selected.size} partners`}?</DialogTitle>
            <DialogDescription>
              {selected.size === 1 ? "It" : "They"} will be permanently removed, and taken off {selected.size === 1 ? "its" : "their"} deals. The deals stay. This can't be undone.
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
