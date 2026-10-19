import { useEffect, useState } from "react";
import { Search, Plus, Trash2, X, LifeBuoy } from "lucide-react";
import { useCrm } from "@/context";
import { PageHeader, Avatar, EntityIcon, EmptyState, UserAvatar } from "@/components/shared";
import { TicketDialog } from "@/components/tickets/ticket-dialog";
import { TicketsBoard } from "@/components/tickets/tickets-board";
import { TICKET_PRIORITIES, TICKET_STATUSES, TicketPriorityBadge, TicketStatusBadge, dueState, ticketPriorityLabel, ticketStatusLabel } from "@/components/tickets/ticket-options";
import { memberName } from "@/components/deals/owner-field";
import { OwnerSwitch } from "@/components/deals/owner-switch";
import { FilterBar } from "@/components/filter-bar";
import { fieldsFromDefs, sanitize } from "@/lib/filters";
import { useListView } from "@/hooks/use-list-view";
import { ViewSwitcher } from "@/components/view-switcher";
import { withQuery } from "@/hooks/use-router";
import { RecordTable, columnKind, type NameColumn, type RecordColumn } from "@/components/record-table";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter, DialogClose } from "@/components/ui/dialog";
import { CustomFieldDisplay, readCustom } from "@/lib/custom-fields";
import { relationColumn } from "@/lib/relations";
import { customFieldCopy, customFieldEdit, optionEdit, recordEdit, textEdit } from "@/components/cell-editors";
import { useTableView, useAggregates, listFilterQuery } from "@/hooks/use-table-view";
import { downloadCsv, exportListCsv, exportViewCsv } from "@/lib/csv";
import { ExportButton } from "@/components/export-button";
import { cn, formatDate } from "@/lib/utils";
import type { Ticket } from "@/types";

const dash = <span className="text-muted-foreground">—</span>;

// The tickets: a list, or a board by status (`board`), over the same filters,
// view and search. `openId`, `viewParam` and `filtersParam` as ContactsPage.
export function TicketsPage({ navigate, openId, viewParam, filtersParam, board = false }: { navigate: (to: string, opts?: { replace?: boolean }) => void; openId?: string; viewParam?: string; filtersParam?: string; board?: boolean }) {
  const { tickets, ticketsPag, stats, team, setTicketsPage, setTicketsSort, setTicketsSearch, setTicketsFilters, setTicketsView, deleteTickets, updateTicket, customFields, setError } = useCrm();
  const ticketFields = customFields.filter((d) => d.entity_type === "ticket");
  const teamOptions = team.map((m) => ({ label: memberName(m), value: m.id }));
  const filterFields = fieldsFromDefs(
    [
      { key: "subject", label: "Subject", type: "text", column: "subject" },
      { key: "status", label: "Status", type: "enum", column: "status", options: TICKET_STATUSES },
      { key: "priority", label: "Priority", type: "enum", column: "priority", options: TICKET_PRIORITIES },
      { key: "company_id", label: "Customer", type: "relation", entity: "company", column: "company" },
      { key: "customer_user_id", label: "Reported by", type: "relation", entity: "customer_user", column: "customer_user" },
      // "Assignee is empty": the tickets nobody has taken yet.
      { key: "owner_id", label: "Assignee", type: "enum", column: "owner", options: teamOptions },
      { key: "due_date", label: "Due date", type: "date", column: "due_date" },
      { key: "description", label: "Description", type: "text" },
      { key: "created_at", label: "Created", type: "date", column: "created_at" },
    ],
    ticketFields,
  );
  const view = useTableView("ticket", viewParam, { name: 280, status: 130, priority: 110, company: 200, customer_user: 180, owner: 170, due_date: 130, created_at: 170 });
  const listView = useListView({ entity: "ticket", table: view, filtersParam, pag: ticketsPag, setFilters: setTicketsFilters, setView: setTicketsView, navigate });

  const name: NameColumn<Ticket> = {
    label: "Subject",
    sort: "subject",
    href: (t) => `/tickets?record=${encodeURIComponent(t.id)}`,
    text: (t) => t.subject,
    render: (t) => (
      <>
        <LifeBuoy className="size-4 shrink-0 text-muted-foreground" />
        <span className="truncate">{t.subject || "—"}</span>
      </>
    ),
  };
  // A cell edited in place saves its one field; the list refetches.
  const saveCell = async (t: Ticket, patch: Record<string, unknown>) => {
    try {
      await updateTicket(t.id, patch as Partial<Ticket>);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save");
    }
  };
  const columns: RecordColumn<Ticket>[] = [
    {
      key: "status", label: "Status", sort: "status", text: (t) => ticketStatusLabel(t.status),
      edit: optionEdit("status", TICKET_STATUSES, saveCell, undefined, (v) => <TicketStatusBadge value={v} />),
      render: (t) => <TicketStatusBadge value={t.status} />,
    },
    {
      key: "priority", label: "Priority", sort: "priority", text: (t) => ticketPriorityLabel(t.priority),
      edit: optionEdit("priority", TICKET_PRIORITIES, saveCell, undefined, (v) => <TicketPriorityBadge value={v} />),
      render: (t) => <TicketPriorityBadge value={t.priority} />,
    },
    {
      key: "company", label: "Customer", sort: "company_id", text: (t) => t.company_name ?? "", edit: recordEdit("company_id", "company", "Customer", saveCell, true),
      render: (t) => t.company_name
        ? <span className="flex min-w-0 items-center gap-2"><EntityIcon name={t.company_name} domain={t.company_domain} className="size-5" /><span className="truncate">{t.company_name}</span></span>
        : dash,
    },
    {
      key: "customer_user", label: "Reported by", sort: "customer_user_id", text: (t) => t.customer_user_name ?? "",
      render: (t) => {
        if (!t.customer_user_name) return dash;
        const [first, ...rest] = t.customer_user_name.split(" ");
        return <span className="flex min-w-0 items-center gap-2"><Avatar firstName={first} lastName={rest.join(" ")} className="size-5 text-[0.5625rem]" /><span className="truncate">{t.customer_user_name}</span></span>;
      },
    },
    {
      key: "owner", label: "Assignee", sort: "owner_id", text: (t) => (t.owner ? memberName(t.owner) : ""),
      edit: optionEdit("owner_id", teamOptions, saveCell, "Unassigned"),
      render: (t) => t.owner
        ? <span className="flex min-w-0 items-center gap-2"><UserAvatar name={t.owner.name} email={t.owner.email} className="size-5 text-[0.5625rem]" /><span className="truncate">{memberName(t.owner)}</span></span>
        : <span className="text-faint">Unassigned</span>,
    },
    {
      key: "due_date", label: "Due date", sort: "due_date", kind: "date", text: (t) => t.due_date ?? "", edit: textEdit("due_date", saveCell, { input: "date", nullable: true }),
      render: (t) => {
        if (!t.due_date) return dash;
        const due = dueState(t.due_date, t.status);
        return <span className={cn("tabular", due === "overdue" && "font-medium text-destructive", due === "today" && "font-medium text-warning")}>{formatDate(t.due_date)}</span>;
      },
    },
    { key: "created_at", label: "Created", sort: "created_at", kind: "date", text: (t) => t.created_at, render: (t) => <span className="tabular text-muted-foreground">{formatDate(t.created_at)}</span> },
    ...ticketFields.map((def): RecordColumn<Ticket> => def.field_type === "relation" ? { ...relationColumn<Ticket>(def), edit: customFieldEdit(def, saveCell, customFields) } : ({
      key: def.key, label: def.label, sort: def.key, kind: columnKind(def.field_type), edit: customFieldEdit(def, saveCell, customFields), copy: customFieldCopy(def),
      text: (t) => String(readCustom(t, def.key) ?? ""),
      render: (t) => <CustomFieldDisplay def={def} value={readCustom(t, def.key)} />,
    })),
  ];

  const [search, setSearch] = useState(ticketsPag.search);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Debounce the search box → server-side filter.
  useEffect(() => {
    const t = setTimeout(() => setTicketsSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]); // eslint-disable-line react-hooks/exhaustive-deps

  // The selection holds only rows on screen: paging, searching or deleting drops the rest.
  useEffect(() => {
    setSelected((prev) => {
      const next = new Set(tickets.filter((t) => prev.has(t.id)).map((t) => t.id));
      return next.size === prev.size ? prev : next;
    });
  }, [tickets]);

  // The footer totals, for the columns (and subject) that have a calculation set.
  const totals = useAggregates(
    "/api/tickets/aggregates",
    listFilterQuery(ticketsPag),
    ["subject", ...columns.map((c) => c.key)].flatMap((key) => { const op = view.aggregate(key); return op ? [{ key, op }] : []; }),
    tickets,
  );

  const openRecord = (id: string) => navigate(withQuery({ record: id }), { replace: !!openId });
  const totalPages = Math.max(1, Math.ceil(ticketsPag.total / ticketsPag.limit));
  // List and board keep the view and filters in the address; the panel closes.
  const switchTo = (next: "list" | "board") => {
    const q = new URLSearchParams(window.location.search);
    q.delete("record");
    const s = q.toString();
    navigate((next === "board" ? "/tickets/board" : "/tickets") + (s ? `?${s}` : ""));
  };

  const addButton = (
    <Button size="sm" onClick={() => setDialogOpen(true)}>
      <Plus className="size-4" />
      New ticket
    </Button>
  );

  const exportSelected = () => {
    const rows = tickets.filter((t) => selected.has(t.id));
    const shown = columns.filter((c) => view.visible(c.key));
    downloadCsv("tickets.csv", [name.label, ...shown.map((c) => c.label)], rows.map((r) => [name.text(r), ...shown.map((c) => c.text(r))]));
  };

  // Every record the list selects as it is now (filters, search, sort, the columns on screen).
  const exportAll = () => exportListCsv<Ticket>({
    filename: listView.view?.name ?? "tickets", listPath: "/api/tickets", rowsKey: "tickets", name,
    columns: columns.filter((c) => view.visible(c.key)),
    query: { filters: sanitize(ticketsPag.filters), sort: ticketsPag.sort, order: ticketsPag.order, search: ticketsPag.search },
  }).catch((e) => setError(e instanceof Error ? e.message : "Could not export"));

  const confirmDelete = async () => {
    const ids = [...selected];
    setDeleting(true);
    try {
      await deleteTickets(ids);
      setConfirmOpen(false);
      if (openId && ids.includes(openId)) navigate(withQuery({ record: null }), { replace: true });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not delete");
    } finally {
      setDeleting(false);
    }
  };

  const empty = tickets.length === 0 && !ticketsPag.search && sanitize(listView.filters).length === 0;

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <PageHeader title="Tickets" count={stats.tickets}>
        {selected.size > 0 && !board ? (
          <>
            <span className="tabular text-[0.8125rem] text-muted-foreground">{selected.size} selected</span>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
              <X className="size-4" />
              Clear
            </Button>
            <ExportButton selected={selected.size} pageRows={tickets.length} total={ticketsPag.total} onSelected={exportSelected} onAll={exportAll} />
            <Button size="sm" variant="destructive" onClick={() => setConfirmOpen(true)}>
              <Trash2 className="size-4" />
              Delete
            </Button>
          </>
        ) : (
          <>
            <Tabs value={board ? "board" : "list"} onValueChange={(v) => switchTo(v as "list" | "board")}>
              <TabsList aria-label="Tickets view">
                <TabsTrigger value="list">List</TabsTrigger>
                <TabsTrigger value="board">Board</TabsTrigger>
              </TabsList>
            </Tabs>
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Subject, email or phone…"
                aria-label="Search tickets"
                className="h-7 w-56 pl-8"
              />
            </div>
            {addButton}
          </>
        )}
      </PageHeader>
      <FilterBar
        leading={
          <>
            <ViewSwitcher
              views={listView.views}
              current={listView.view}
              count={ticketsPag.total}
              onOpen={listView.open}
              onCreate={listView.create}
              onRename={listView.rename}
              onDelete={listView.remove}
              onExport={(v) => exportViewCsv<Ticket>({ view: v, listPath: "/api/tickets", rowsKey: "tickets", name, columns }).catch((e) => setError(e instanceof Error ? e.message : "Could not export"))}
            />
            <OwnerSwitch filters={listView.filters} onChange={listView.setFilters} />
          </>
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
      {empty && stats.tickets === 0 ? (
        <EmptyState title="No tickets yet. Log your customers' requests and problems here." action={addButton} />
      ) : board ? (
        <TicketsBoard filters={ticketsPag.filters} search={ticketsPag.search} openId={openId} onOpen={openRecord} />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="min-h-0 flex-1">
            <RecordTable
              rows={tickets}
              name={name}
              columns={columns}
              view={view}
              sorting={ticketsPag}
              onSort={setTicketsSort}
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
              Page {ticketsPag.page} of {totalPages}
            </span>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="outline" disabled={ticketsPag.page <= 1} onClick={() => setTicketsPage(ticketsPag.page - 1)} aria-label="Previous page">
                Prev
              </Button>
              <Button size="sm" variant="outline" disabled={ticketsPag.page >= totalPages} onClick={() => setTicketsPage(ticketsPag.page + 1)} aria-label="Next page">
                Next
              </Button>
            </div>
          </footer>
        </div>
      )}

      <TicketDialog open={dialogOpen} onOpenChange={setDialogOpen} onCreated={(t) => openRecord(t.id)} />

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete {selected.size === 1 ? "ticket" : `${selected.size} tickets`}?</DialogTitle>
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
