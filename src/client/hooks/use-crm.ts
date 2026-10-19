import { useState, useCallback, useEffect, type Dispatch, type SetStateAction } from "react";
import { api } from "../api";
import type {
  Contact, Company, Deal, Partner, CustomerUser, Ticket, Stats, PaginatedState, StageDef, TeamMember,
  Activity, EntityType, CustomFieldDef, ImportRow, ImportEntity, ImportResult,
} from "../types";
import { sanitize, type FilterNode } from "@/lib/filters";
import type { CrmContextValue } from "../context";

const defaultPag = (sort: string): PaginatedState => ({
  page: 1, limit: 25, total: 0, sort, order: "desc", search: "", filters: [],
});

function pagParams(pag: PaginatedState): URLSearchParams {
  const p = new URLSearchParams({
    page: String(pag.page), limit: String(pag.limit), sort: pag.sort, order: pag.order,
  });
  if (pag.search) p.set("search", pag.search);
  const filters = sanitize(pag.filters);
  if (filters.length) p.set("filters", JSON.stringify(filters));
  // Date filters work in the viewer's local day.
  p.set("tz", String(-new Date().getTimezoneOffset()));
  return p;
}

export function useCrmState(isAgent: boolean): CrmContextValue {
  const [stats, setStats] = useState<Stats>({ contacts: 0, companies: 0, deals: 0, dealValue: 0, dealRecurringValue: 0, customers: 0, partners: 0, customerUsers: 0, tickets: 0, openTickets: 0 });
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const [contacts, setContacts] = useState<Contact[]>([]);
  const [contactsPag, setContactsPag] = useState<PaginatedState>(defaultPag("created_at"));

  const [companies, setCompanies] = useState<Company[]>([]);
  const [companiesPag, setCompaniesPag] = useState<PaginatedState>(defaultPag("created_at"));

  const [partners, setPartners] = useState<Partner[]>([]);
  const [partnersPag, setPartnersPag] = useState<PaginatedState>(defaultPag("created_at"));

  const [customerUsers, setCustomerUsers] = useState<CustomerUser[]>([]);
  const [customerUsersPag, setCustomerUsersPag] = useState<PaginatedState>(defaultPag("created_at"));

  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [ticketsPag, setTicketsPag] = useState<PaginatedState>(defaultPag("created_at"));

  const [deals, setDeals] = useState<Deal[]>([]);
  const [dealsPag, setDealsPag] = useState<PaginatedState>(defaultPag("created_at"));
  const [dealsTotalValue, setDealsTotalValue] = useState(0);
  const [boardDeals, setBoardDeals] = useState<Deal[]>([]);
  const [boardFilters, setBoardFilters] = useState<FilterNode[]>([]);

  const [customFields, setCustomFields] = useState<CustomFieldDef[]>([]);
  const [stages, setStages] = useState<StageDef[]>([]);
  // The accounts deals can be assigned to.
  const [team, setTeam] = useState<TeamMember[]>([]);
  // Bumped on every record write, so an open record page re-reads itself.
  const [changes, setChanges] = useState(0);

  // ── Fetchers ──

  const fetchStats = useCallback(async () => {
    setStats(await api<Stats>("GET", "/api/stats"));
  }, []);

  const fetchContacts = useCallback(async (pag: PaginatedState) => {
    const data = await api<{ contacts: Contact[]; total: number }>("GET", `/api/contacts?${pagParams(pag)}`);
    setContacts(data.contacts);
    setContactsPag((prev) => ({ ...prev, total: data.total }));
  }, []);

  const fetchCompanies = useCallback(async (pag: PaginatedState) => {
    const data = await api<{ companies: Company[]; total: number }>("GET", `/api/companies?${pagParams(pag)}`);
    setCompanies(data.companies);
    setCompaniesPag((prev) => ({ ...prev, total: data.total }));
  }, []);

  const fetchPartners = useCallback(async (pag: PaginatedState) => {
    const data = await api<{ partners: Partner[]; total: number }>("GET", `/api/partners?${pagParams(pag)}`);
    setPartners(data.partners);
    setPartnersPag((prev) => ({ ...prev, total: data.total }));
  }, []);

  const fetchCustomerUsers = useCallback(async (pag: PaginatedState) => {
    const data = await api<{ customer_users: CustomerUser[]; total: number }>("GET", `/api/customer-users?${pagParams(pag)}`);
    setCustomerUsers(data.customer_users);
    setCustomerUsersPag((prev) => ({ ...prev, total: data.total }));
  }, []);

  const fetchTickets = useCallback(async (pag: PaginatedState) => {
    const data = await api<{ tickets: Ticket[]; total: number }>("GET", `/api/tickets?${pagParams(pag)}`);
    setTickets(data.tickets);
    setTicketsPag((prev) => ({ ...prev, total: data.total }));
  }, []);

  const fetchDeals = useCallback(async (pag: PaginatedState) => {
    const data = await api<{ deals: Deal[]; total: number; totalValue: number }>("GET", `/api/deals?${pagParams(pag)}`);
    setDeals(data.deals);
    setDealsPag((prev) => ({ ...prev, total: data.total }));
    setDealsTotalValue(data.totalValue);
  }, []);

  // The board shows every deal, narrowed only by its own filters (no paging).
  const fetchBoardDeals = useCallback(async () => {
    const p = new URLSearchParams();
    const filters = sanitize(boardFilters);
    if (filters.length) p.set("filters", JSON.stringify(filters));
    p.set("tz", String(-new Date().getTimezoneOffset()));
    const data = await api<{ deals: Deal[] }>("GET", `/api/deals/board?${p}`);
    setBoardDeals(data.deals);
  }, [boardFilters]);

  const refetchCustomFields = useCallback(async () => {
    const data = await api<{ defs: CustomFieldDef[] }>("GET", "/api/custom-fields");
    setCustomFields(data.defs);
  }, []);

  const refetchTeam = useCallback(async () => {
    const data = await api<{ team: TeamMember[] }>("GET", "/api/team");
    setTeam(data.team);
  }, []);

  const refetchStages = useCallback(async () => {
    const data = await api<{ stages: StageDef[] }>("GET", "/api/stages");
    setStages(data.stages);
  }, []);

  // ── Initial load ──

  useEffect(() => {
    (async () => {
      setLoading(true);
      try {
        await Promise.all([
          fetchStats(), fetchContacts(contactsPag), fetchCompanies(companiesPag), fetchPartners(partnersPag), fetchCustomerUsers(customerUsersPag), fetchTickets(ticketsPag),
          fetchDeals(dealsPag), fetchBoardDeals(),
          refetchCustomFields(), refetchStages(), refetchTeam(),
        ]);
      } catch (err) {
        setError((err as Error).message);
      } finally {
        setLoading(false);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ── Per-list refetch when its pagination/sort/search changes ──

  useEffect(() => { fetchContacts(contactsPag).catch((e) => setError((e as Error).message)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [contactsPag.page, contactsPag.sort, contactsPag.order, contactsPag.search, JSON.stringify(contactsPag.filters)]);
  useEffect(() => { fetchCompanies(companiesPag).catch((e) => setError((e as Error).message)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [companiesPag.page, companiesPag.sort, companiesPag.order, companiesPag.search, JSON.stringify(companiesPag.filters)]);
  useEffect(() => { fetchPartners(partnersPag).catch((e) => setError((e as Error).message)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [partnersPag.page, partnersPag.sort, partnersPag.order, partnersPag.search, JSON.stringify(partnersPag.filters)]);
  useEffect(() => { fetchCustomerUsers(customerUsersPag).catch((e) => setError((e as Error).message)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [customerUsersPag.page, customerUsersPag.sort, customerUsersPag.order, customerUsersPag.search, JSON.stringify(customerUsersPag.filters)]);
  useEffect(() => { fetchTickets(ticketsPag).catch((e) => setError((e as Error).message)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [ticketsPag.page, ticketsPag.sort, ticketsPag.order, ticketsPag.search, JSON.stringify(ticketsPag.filters)]);
  useEffect(() => { fetchBoardDeals().catch((e) => setError((e as Error).message)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [JSON.stringify(sanitize(boardFilters))]);
  useEffect(() => { fetchDeals(dealsPag).catch((e) => setError((e as Error).message)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [dealsPag.page, dealsPag.sort, dealsPag.order, dealsPag.search]);

  // ── Pagination setters ──

  const makeSetters = (setter: Dispatch<SetStateAction<PaginatedState>>) => ({
    setPage: (page: number) => setter((p) => ({ ...p, page })),
    setSort: (col: string) => setter((p) => ({ ...p, sort: col, order: p.sort === col && p.order === "asc" ? "desc" : "asc", page: 1 })),
    setSearch: (search: string) => setter((p) => ({ ...p, search, page: 1 })),
    setFilters: (filters: PaginatedState["filters"]) => setter((p) => ({ ...p, filters, page: 1 })),
    // A view's filters and sort at once, so switching views fetches once.
    setView: (v: { filters: PaginatedState["filters"]; sort: string; order: "asc" | "desc" }) =>
      setter((p) => ({ ...p, filters: v.filters, sort: v.sort, order: v.order, page: 1 })),
  });
  const cSet = makeSetters(setContactsPag);
  const coSet = makeSetters(setCompaniesPag);
  const pSet = makeSetters(setPartnersPag);
  const uSet = makeSetters(setCustomerUsersPag);
  const tSet = makeSetters(setTicketsPag);
  const dSet = makeSetters(setDealsPag);

  // ── Contacts CRUD ──

  const addContact = useCallback(async (data: Partial<Contact>) => {
    await api("POST", "/api/contacts", data);
    await Promise.all([fetchContacts(contactsPag), fetchStats()]);
  }, [contactsPag, fetchContacts, fetchStats]);

  // A write can change what other records show (a relation reads from both
  // sides), so both lists refetch and any open record page re-reads.
  const recordsChanged = useCallback(async () => {
    setChanges((n) => n + 1);
    await Promise.all([fetchContacts(contactsPag), fetchCompanies(companiesPag), fetchPartners(partnersPag), fetchCustomerUsers(customerUsersPag), fetchTickets(ticketsPag), fetchBoardDeals(), fetchStats()]);
  }, [contactsPag, companiesPag, partnersPag, customerUsersPag, ticketsPag, fetchContacts, fetchCompanies, fetchPartners, fetchCustomerUsers, fetchTickets, fetchBoardDeals, fetchStats]);

  const updateContact = useCallback(async (id: string, data: Partial<Contact>) => {
    await api("PUT", `/api/contacts/${id}`, data);
    await recordsChanged();
  }, [recordsChanged]);

  const deleteContacts = useCallback(async (ids: string[]) => {
    await api("POST", "/api/contacts/bulk-delete", { ids });
    await Promise.all([fetchContacts(contactsPag), fetchStats()]);
  }, [contactsPag, fetchContacts, fetchStats]);

  const fetchContact = useCallback(async (id: string): Promise<Contact | null> => {
    try {
      const data = await api<{ contact: Contact }>("GET", `/api/contacts/${id}`);
      return data.contact;
    } catch {
      return null;
    }
  }, []);

  // ── Companies CRUD ──

  const addCompany = useCallback(async (data: Partial<Company>) => {
    await api("POST", "/api/companies", data);
    await Promise.all([fetchCompanies(companiesPag), fetchStats()]);
  }, [companiesPag, fetchCompanies, fetchStats]);

  const fetchCompany = useCallback(async (id: string): Promise<Company | null> => {
    try {
      const data = await api<{ company: Company }>("GET", `/api/companies/${id}`);
      return data.company;
    } catch {
      return null;
    }
  }, []);

  const updateCompany = useCallback(async (id: string, data: Partial<Company>) => {
    await api("PUT", `/api/companies/${id}`, data);
    await recordsChanged();
  }, [recordsChanged]);

  // No longer a customer: the company stays, its timeline says it was one.
  const removeCustomer = useCallback(async (id: string) => {
    await api("POST", `/api/companies/${encodeURIComponent(id)}/remove-customer`);
    await recordsChanged();
  }, [recordsChanged]);

  const deleteCompanies = useCallback(async (ids: string[]) => {
    await api("POST", "/api/companies/bulk-delete", { ids });
    await Promise.all([fetchCompanies(companiesPag), fetchStats()]);
  }, [companiesPag, fetchCompanies, fetchStats]);

  // ── Partners CRUD ──

  const addPartner = useCallback(async (data: Partial<Partner>) => {
    await api("POST", "/api/partners", data);
    await Promise.all([fetchPartners(partnersPag), fetchStats()]);
  }, [partnersPag, fetchPartners, fetchStats]);

  const fetchPartner = useCallback(async (id: string): Promise<Partner | null> => {
    try {
      const data = await api<{ partner: Partner }>("GET", `/api/partners/${encodeURIComponent(id)}`);
      return data.partner;
    } catch {
      return null;
    }
  }, []);

  const updatePartner = useCallback(async (id: string, data: Partial<Partner>) => {
    await api("PUT", `/api/partners/${encodeURIComponent(id)}`, data);
    await recordsChanged();
  }, [recordsChanged]);

  // A partner gone leaves its deals, which no longer list it.
  const deletePartners = useCallback(async (ids: string[]) => {
    await api("POST", "/api/partners/bulk-delete", { ids });
    await Promise.all([fetchPartners(partnersPag), fetchDeals(dealsPag), fetchBoardDeals(), fetchStats()]);
  }, [partnersPag, dealsPag, fetchPartners, fetchDeals, fetchBoardDeals, fetchStats]);

  // ── Customer users CRUD ──

  const addCustomerUser = useCallback(async (data: Partial<CustomerUser>) => {
    await api("POST", "/api/customer-users", data);
    await Promise.all([fetchCustomerUsers(customerUsersPag), fetchStats()]);
  }, [customerUsersPag, fetchCustomerUsers, fetchStats]);

  const fetchCustomerUser = useCallback(async (id: string): Promise<CustomerUser | null> => {
    try {
      const data = await api<{ customer_user: CustomerUser }>("GET", `/api/customer-users/${encodeURIComponent(id)}`);
      return data.customer_user;
    } catch {
      return null;
    }
  }, []);

  const updateCustomerUser = useCallback(async (id: string, data: Partial<CustomerUser>) => {
    await api("PUT", `/api/customer-users/${encodeURIComponent(id)}`, data);
    await recordsChanged();
  }, [recordsChanged]);

  const deleteCustomerUsers = useCallback(async (ids: string[]) => {
    await api("POST", "/api/customer-users/bulk-delete", { ids });
    await Promise.all([fetchCustomerUsers(customerUsersPag), fetchStats()]);
  }, [customerUsersPag, fetchCustomerUsers, fetchStats]);

  // ── Tickets CRUD ──

  const addTicket = useCallback(async (data: Partial<Ticket>) => {
    const { ticket } = await api<{ ticket: Ticket }>("POST", "/api/tickets", data);
    await recordsChanged();
    return ticket;
  }, [recordsChanged]);

  const fetchTicket = useCallback(async (id: string): Promise<Ticket | null> => {
    try {
      const data = await api<{ ticket: Ticket }>("GET", `/api/tickets/${encodeURIComponent(id)}`);
      return data.ticket;
    } catch {
      return null;
    }
  }, []);

  const updateTicket = useCallback(async (id: string, data: Partial<Ticket>) => {
    await api("PUT", `/api/tickets/${encodeURIComponent(id)}`, data);
    await recordsChanged();
  }, [recordsChanged]);

  const deleteTickets = useCallback(async (ids: string[]) => {
    await api("POST", "/api/tickets/bulk-delete", { ids });
    await recordsChanged();
  }, [recordsChanged]);

  // ── Deals CRUD ──

  // A deal's partners count it among their deals, so they refetch too.
  const addDeal = useCallback(async (data: Partial<Deal>) => {
    const { deal } = await api<{ deal: Deal }>("POST", "/api/deals", data);
    await Promise.all([fetchDeals(dealsPag), fetchBoardDeals(), fetchPartners(partnersPag), fetchStats()]);
    return deal;
  }, [dealsPag, partnersPag, fetchDeals, fetchBoardDeals, fetchPartners, fetchStats]);

  const fetchDeal = useCallback(async (id: string): Promise<Deal | null> => {
    try {
      // The viewer's offset, so "overdue" and "today" in its progress are their day.
      const data = await api<{ deal: Deal }>("GET", `/api/deals/${encodeURIComponent(id)}?tz=${-new Date().getTimezoneOffset()}`);
      return data.deal;
    } catch {
      return null;
    }
  }, []);

  // A deal open in the panel re-reads on `changes`, so a stage moved on the board shows there too.
  const updateDeal = useCallback(async (id: string, data: Partial<Deal>) => {
    // The board shows the change at once (a dropped card stays where it was
    // dropped); the refetch settles it, and puts it back if the write fails.
    setBoardDeals((ds) => ds.map((d) => (d.id === id ? { ...d, ...data } : d)));
    try {
      await api("PUT", `/api/deals/${id}`, data);
    } finally {
      await Promise.all([fetchDeals(dealsPag), recordsChanged()]);
    }
  }, [dealsPag, fetchDeals, recordsChanged]);

  const deleteDeal = useCallback(async (id: string) => {
    await api("DELETE", `/api/deals/${id}`);
    await Promise.all([fetchDeals(dealsPag), fetchBoardDeals(), fetchPartners(partnersPag), fetchStats()]);
  }, [dealsPag, partnersPag, fetchDeals, fetchBoardDeals, fetchPartners, fetchStats]);

  // ── Timeline ──

  const fetchActivities = useCallback(async (entityType: EntityType, entityId: string) => {
    const data = await api<{ activities: Activity[] }>("GET", `/api/activities?entity_type=${entityType}&entity_id=${entityId}`);
    return data.activities;
  }, []);

  const addNote = useCallback(async (entityType: EntityType, entityId: string, body: string) => {
    await api("POST", "/api/activities", { entity_type: entityType, entity_id: entityId, type: "note", body });
  }, []);

  // ── Bulk import (contacts / companies) ──

  const importEntity = useCallback(
    async (entity: ImportEntity, rows: ImportRow[], opts?: { inferCompanyFromEmail?: boolean }): Promise<ImportResult> => {
      if (entity === "company") {
        const res = await api<ImportResult>("POST", "/api/companies/import", { companies: rows });
        await Promise.all([fetchCompanies(companiesPag), fetchStats()]);
        return res;
      }
      const res = await api<ImportResult>("POST", "/api/contacts/import", {
        contacts: rows,
        inferCompanyFromEmail: opts?.inferCompanyFromEmail ?? false,
      });
      await Promise.all([fetchContacts(contactsPag), fetchStats()]);
      return res;
    },
    [contactsPag, companiesPag, fetchContacts, fetchCompanies, fetchStats],
  );

  return {
    isAgent, stats,
    contacts, contactsPag, setContactsPage: cSet.setPage, setContactsSort: cSet.setSort, setContactsSearch: cSet.setSearch, setContactsFilters: cSet.setFilters, setContactsView: cSet.setView,
    addContact, updateContact, deleteContacts, fetchContact, fetchCompany,
    companies, companiesPag, setCompaniesPage: coSet.setPage, setCompaniesSort: coSet.setSort, setCompaniesSearch: coSet.setSearch, setCompaniesFilters: coSet.setFilters, setCompaniesView: coSet.setView,
    addCompany, updateCompany, deleteCompanies, removeCustomer,
    partners, partnersPag, setPartnersPage: pSet.setPage, setPartnersSort: pSet.setSort, setPartnersSearch: pSet.setSearch, setPartnersFilters: pSet.setFilters, setPartnersView: pSet.setView,
    addPartner, fetchPartner, updatePartner, deletePartners,
    customerUsers, customerUsersPag, setCustomerUsersPage: uSet.setPage, setCustomerUsersSort: uSet.setSort, setCustomerUsersSearch: uSet.setSearch, setCustomerUsersFilters: uSet.setFilters, setCustomerUsersView: uSet.setView,
    addCustomerUser, fetchCustomerUser, updateCustomerUser, deleteCustomerUsers,
    tickets, ticketsPag, setTicketsPage: tSet.setPage, setTicketsSort: tSet.setSort, setTicketsSearch: tSet.setSearch, setTicketsFilters: tSet.setFilters, setTicketsView: tSet.setView,
    addTicket, fetchTicket, updateTicket, deleteTickets,
    deals, dealsPag, dealsTotalValue, setDealsPage: dSet.setPage, setDealsSort: dSet.setSort, setDealsSearch: dSet.setSearch,
    addDeal, fetchDeal, updateDeal, deleteDeal, boardDeals, boardFilters, setBoardFilters,
    fetchActivities, addNote, importEntity,
    customFields, refetchCustomFields,
    changes, recordsChanged,
    stages, refetchStages,
    team, refetchTeam,
    refetchBoard: fetchBoardDeals, refetchStats: fetchStats,
    loading, error, setError,
  };
}
