import { createContext, useContext } from "react";
import type { View, Contact, Company, Deal, Partner, CustomerUser, Ticket, TeamMember, Stats, PaginatedState, StageDef, Activity, EntityType, CustomFieldDef, ImportRow, ImportEntity, ImportResult } from "./types";
import type { FilterNode } from "./lib/filters";

export interface CrmContextValue {
  isAgent: boolean;
  stats: Stats;

  // Contacts
  contacts: Contact[];
  contactsPag: PaginatedState;
  setContactsPage: (page: number) => void;
  setContactsSort: (col: string) => void;
  setContactsSearch: (search: string) => void;
  setContactsFilters: (filters: PaginatedState["filters"]) => void;
  setContactsView: (v: { filters: PaginatedState["filters"]; sort: string; order: "asc" | "desc" }) => void;
  addContact: (data: Partial<Contact>) => Promise<void>;
  updateContact: (id: string, data: Partial<Contact>) => Promise<void>;
  deleteContacts: (ids: string[]) => Promise<void>;
  fetchContact: (id: string) => Promise<Contact | null>;
  fetchCompany: (id: string) => Promise<Company | null>;

  // Companies
  companies: Company[];
  companiesPag: PaginatedState;
  setCompaniesPage: (page: number) => void;
  setCompaniesSort: (col: string) => void;
  setCompaniesSearch: (search: string) => void;
  setCompaniesFilters: (filters: PaginatedState["filters"]) => void;
  setCompaniesView: (v: { filters: PaginatedState["filters"]; sort: string; order: "asc" | "desc" }) => void;
  addCompany: (data: Partial<Company>) => Promise<void>;
  updateCompany: (id: string, data: Partial<Company>) => Promise<void>;
  deleteCompanies: (ids: string[]) => Promise<void>;
  /** Makes the company no longer a customer (it stays a company). */
  removeCustomer: (id: string) => Promise<void>;

  // Partners
  partners: Partner[];
  partnersPag: PaginatedState;
  setPartnersPage: (page: number) => void;
  setPartnersSort: (col: string) => void;
  setPartnersSearch: (search: string) => void;
  setPartnersFilters: (filters: PaginatedState["filters"]) => void;
  setPartnersView: (v: { filters: PaginatedState["filters"]; sort: string; order: "asc" | "desc" }) => void;
  addPartner: (data: Partial<Partner>) => Promise<void>;
  fetchPartner: (id: string) => Promise<Partner | null>;
  updatePartner: (id: string, data: Partial<Partner>) => Promise<void>;
  deletePartners: (ids: string[]) => Promise<void>;

  // Customer users
  customerUsers: CustomerUser[];
  customerUsersPag: PaginatedState;
  setCustomerUsersPage: (page: number) => void;
  setCustomerUsersSort: (col: string) => void;
  setCustomerUsersSearch: (search: string) => void;
  setCustomerUsersFilters: (filters: PaginatedState["filters"]) => void;
  setCustomerUsersView: (v: { filters: PaginatedState["filters"]; sort: string; order: "asc" | "desc" }) => void;
  addCustomerUser: (data: Partial<CustomerUser>) => Promise<void>;
  fetchCustomerUser: (id: string) => Promise<CustomerUser | null>;
  updateCustomerUser: (id: string, data: Partial<CustomerUser>) => Promise<void>;
  deleteCustomerUsers: (ids: string[]) => Promise<void>;

  // Tickets
  tickets: Ticket[];
  ticketsPag: PaginatedState;
  setTicketsPage: (page: number) => void;
  setTicketsSort: (col: string) => void;
  setTicketsSearch: (search: string) => void;
  setTicketsFilters: (filters: PaginatedState["filters"]) => void;
  setTicketsView: (v: { filters: PaginatedState["filters"]; sort: string; order: "asc" | "desc" }) => void;
  addTicket: (data: Partial<Ticket>) => Promise<Ticket>;
  fetchTicket: (id: string) => Promise<Ticket | null>;
  updateTicket: (id: string, data: Partial<Ticket>) => Promise<void>;
  deleteTickets: (ids: string[]) => Promise<void>;

  // Deals
  deals: Deal[];
  dealsPag: PaginatedState;
  dealsTotalValue: number;
  setDealsPage: (page: number) => void;
  setDealsSort: (col: string) => void;
  setDealsSearch: (search: string) => void;
  addDeal: (data: Partial<Deal>) => Promise<Deal>;
  fetchDeal: (id: string) => Promise<Deal | null>;
  updateDeal: (id: string, data: Partial<Deal>) => Promise<void>;
  deleteDeal: (id: string) => Promise<void>;
  boardDeals: Deal[];
  // The board's own filters (the same rule tree as the lists' filters).
  boardFilters: FilterNode[];
  setBoardFilters: (filters: FilterNode[]) => void;

  // Activity timeline
  fetchActivities: (entityType: EntityType, entityId: string) => Promise<Activity[]>;
  addNote: (entityType: EntityType, entityId: string, body: string) => Promise<void>;

  // Contact import (CSV / XLSX)
  importEntity: (entity: ImportEntity, rows: ImportRow[], opts?: { inferCompanyFromEmail?: boolean }) => Promise<ImportResult>;

  // Custom properties (field definitions, shared across screens)
  customFields: CustomFieldDef[];
  refetchCustomFields: () => Promise<void>;

  // Bumped on every record write; recordsChanged() bumps it and refetches the
  // lists, after a write made outside the functions above (linking a record).
  changes: number;
  recordsChanged: () => Promise<void>;

  // Pipeline stages (data-driven vocabulary, ordered by position)
  stages: StageDef[];
  refetchStages: () => Promise<void>;

  // The accounts deals can be assigned to (GET /api/team)
  team: TeamMember[];
  refetchTeam: () => Promise<void>;
  refetchBoard: () => Promise<void>;
  refetchStats: () => Promise<void>;

  loading: boolean;
  error: string | null;
  setError: (msg: string | null) => void;
}

export const CrmContext = createContext<CrmContextValue>(null!);

export function useCrm() {
  return useContext(CrmContext);
}

// The current view is derived from the URL route, not context.
export type { View };
