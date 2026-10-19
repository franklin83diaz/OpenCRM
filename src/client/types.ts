import type { FilterNode } from "./lib/filters";

export type View = "contacts" | "companies" | "deals" | "partners" | "customer-users" | "tickets";
export type EntityType = "contact" | "company" | "deal" | "partner" | "customer_user" | "ticket";

/** Base storage type for a custom property. Widget flavours ride on top. */
export type AttributeType =
  | "string"
  | "text"
  | "integer"
  | "decimal"
  | "boolean"
  | "date"
  | "datetime"
  | "enumeration"
  | "json"
  | "relation";

/** A relation's side: many_to_one holds one linked record's id in its own
 *  column; one_to_many lists the records whose many_to_one points back here. */
export type RelationType = "many_to_one" | "one_to_many";

/** A linked record as a chip shows it. `domain` is a company's, for its logo. */
export interface RelationRecord {
  id: string;
  label: string;
  domain: string | null;
}

/** A relation's value on a read row (`row.relations[key]`): the linked record,
 *  or for a one_to_many side the first few and how many there are. */
export type RelationValue = RelationRecord | null | { items: RelationRecord[]; total: number };

/** A user-defined field on an entity type. Maps to a real column on the table,
 *  except a relation's one_to_many side, which is read from the other side. */
export interface CustomFieldDef {
  id: string;
  entity_type: EntityType;
  key: string;
  label: string;
  field_type: AttributeType;
  custom_field: string; // widget registry uid, or "" for a bare base type
  options: Record<string, unknown>;
  position: number;
  relation_type: RelationType | null;
  target_entity: EntityType | null;
  inverse_def_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface Company {
  id: string;
  name: string;
  domain: string;
  industry: string;
  phone: string;
  email: string;
  notes: string;
  /** The day they became a customer (YYYY-MM-DD); null = not a customer. */
  customer_since?: string | null;
  /** The next renewal (YYYY-MM-DD); null = none to watch. */
  renewal_date?: string | null;
  /** Its main contact, who may work at another company (a building's property manager); null = none chosen. */
  main_contact_id?: string | null;
  /** The main contact as a record (single-company reads); null when none. */
  main_contact?: RelationRecord | null;
  /** The company the main contact works at, this one or another (single-company reads); null when none. */
  main_contact_company?: RelationRecord | null;
  /** The partner who looks after it; null = none chosen. */
  main_partner_id?: string | null;
  /** The main partner as a record (single-company reads); null when none. */
  main_partner?: RelationRecord | null;
  contact_count?: number;
  custom?: Record<string, unknown>; // write payload; on reads, values are flat columns
  relations?: Record<string, RelationValue>; // reads only, keyed by relation field
  created_at: string;
  updated_at: string;
}

/** A firm that works deals with us (a reseller, a referrer, an integrator), kept apart from companies. */
export interface Partner {
  id: string;
  name: string;
  industry: string;
  /** prospect | onboarding | active | inactive */
  status: string;
  phone: string;
  email: string;
  notes: string;
  /** How many deals it takes part in (reads only). */
  deal_count?: number;
  custom?: Record<string, unknown>; // write payload; on reads, values are flat columns
  relations?: Record<string, RelationValue>; // reads only, keyed by relation field
  created_at: string;
  updated_at: string;
}

/** A person who uses the product at a customer (a company with customer_since), kept apart from contacts. */
export interface CustomerUser {
  id: string;
  name: string;
  email: string;
  phone: string;
  company_id: string | null;
  notes: string;
  company_name?: string | null;
  company_domain?: string | null;
  custom?: Record<string, unknown>; // write payload; on reads, values are flat columns
  relations?: Record<string, RelationValue>; // reads only, keyed by relation field
  created_at: string;
  updated_at: string;
}

/** open | in_progress | waiting | resolved | closed: the last two are done. */
export type TicketStatus = "open" | "in_progress" | "waiting" | "resolved" | "closed";
/** low | medium | high | urgent */
export type TicketPriority = "low" | "medium" | "high" | "urgent";

/** A customer's request or problem: for a customer, maybe reported by one of its customer users. */
export interface Ticket {
  id: string;
  subject: string;
  description: string;
  status: TicketStatus;
  priority: TicketPriority;
  /** The customer it is for (a company with customer_since). */
  company_id: string | null;
  /** Who reported it, one of the customer's users; null = nobody named. */
  customer_user_id: string | null;
  /** The account it is assigned to; null = nobody's yet. */
  owner_id: string | null;
  owner?: TeamMember | null;
  /** When it should be done by (YYYY-MM-DD); null = no date. */
  due_date: string | null;
  company_name?: string | null;
  company_domain?: string | null;
  customer_user_name?: string | null;
  customer_user_email?: string | null;
  custom?: Record<string, unknown>; // write payload; on reads, values are flat columns
  relations?: Record<string, RelationValue>; // reads only, keyed by relation field
  created_at: string;
  updated_at: string;
}

export interface Contact {
  id: string;
  first_name: string;
  last_name: string;
  email: string;
  phone: string;
  company_id: string | null;
  title: string;
  status: string;
  /** The newest email with this contact, from when the CRM synced email; read-only. */
  last_contacted_at?: string | null;
  company_name?: string | null;
  company_domain?: string | null;
  custom?: Record<string, unknown>; // write payload; on reads, values are flat columns
  relations?: Record<string, RelationValue>; // reads only, keyed by relation field
  created_at: string;
  updated_at: string;
}

/** An account a deal can be assigned to (GET /api/team). */
export interface TeamMember {
  id: string;
  name: string;
  email: string;
}

/** A partner taking part in a deal, with its share of it. */
export interface DealPartner extends RelationRecord {
  /** The partner's share, once. */
  one_time_value: number;
  /** The partner's share, each month. */
  recurring_value: number;
  /** Whether it is the deal's main partner (one per deal, listed first). */
  is_main?: boolean;
}

export interface Deal {
  id: string;
  name: string;
  contact_id: string | null;
  company_id: string | null;
  /** What the deal is worth once. */
  value: number;
  /** What it is worth each month while they stay a customer. */
  recurring_value?: number;
  /** The account it is assigned to; null = nobody's yet. */
  owner_id?: string | null;
  owner?: TeamMember | null;
  stage: string;
  close_date: string;
  notes: string;
  contact_first_name?: string | null;
  contact_last_name?: string | null;
  company_name?: string | null;
  company_domain?: string | null;
  /** How an open deal is moving (board and single-deal reads); null when won or lost. */
  progress?: DealProgress | null;
  /** The partners taking part in the deal, by name, with their shares (reads only). */
  partners?: DealPartner[];
  /** Writes only: the whole list of its partners' ids, which replaces the one before. */
  partner_ids?: string[];
  custom?: Record<string, unknown>; // write payload; on reads, values are flat columns
  relations?: Record<string, RelationValue>; // reads only, keyed by relation field
  created_at: string;
  updated_at: string;
}

/** A pipeline stage — data, not code. `key` is immutable and stored on
 *  deals.stage; behavior hangs on is_won/is_lost, never on names. */
export interface StageDef {
  key: string;
  label: string;
  color: string; // palette token (sky, emerald, …)
  position: number;
  is_won: number; // 0 | 1
  is_lost: number; // 0 | 1
  created_at: string;
  updated_at: string;
}

export interface Stats {
  contacts: number;
  companies: number;
  deals: number;
  dealValue: number;
  /** What the deals not lost are worth each month. */
  dealRecurringValue: number;
  customers: number;
  partners: number;
  customerUsers: number;
  tickets: number;
  /** Tickets neither resolved nor closed. */
  openTickets: number;
}

export interface PaginatedState {
  page: number;
  limit: number;
  total: number;
  sort: string;
  order: "asc" | "desc";
  search: string;
  filters: FilterNode[];
}

export interface Activity {
  id: string;
  entity_type: EntityType;
  entity_id: string;
  type: string; // note | email | meeting | slack | stage_change
  body: string;
  meta: string; // JSON string
  created_at: string;
}

// Entities that support bulk spreadsheet import.
export type ImportEntity = "contact" | "company";

// A row handed to the import API: a flat bag of built-in columns plus an
// optional `custom` sub-bag of custom-field values. The concrete shape is
// validated server-side per entity, so this is intentionally loose.
export type ImportRow = Record<string, unknown> & { custom?: Record<string, unknown> };

// Import outcome — fields vary by entity (contacts report companiesCreated,
// companies report duplicates skipped).
export interface ImportResult {
  imported: number;
  skipped: number;
  companiesCreated?: number;
  duplicates?: number;
}

export type OwedBy = "us" | "them";

export interface Task {
  id: string;
  title: string;
  company_id: string | null;
  company_name: string | null;
  contact_id: string | null;
  deal_id: string | null;
  meeting_id: string | null;
  meeting_title: string | null;
  meeting_starts_at: string | null;
  owed_by: OwedBy;
  due_date: string | null;
  done: boolean;
  done_at: string | null;
  quote: string | null;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

/** unknown: nothing against the account, but the CRM can't see whether anyone has been in touch. */
export type HealthStatus = "red" | "yellow" | "green" | "unknown";

/** Whether a sync lets the CRM see a channel: on and working, off, still on its first import, or its latest run failed. */
export type SyncState = "seen" | "off" | "importing" | "failing";

/** A customer as the Customers page shows it. The API also sends the call and
 *  insight fields the CRM no longer fills (next call, ideas, risks…). */
export interface CustomerRow {
  id: string;
  name: string;
  domain: string;
  customer_since: string;
  renewal_date: string | null;
  status: HealthStatus;
  reasons: string[];
  last_touch_at: string | null;
  days_quiet: number | null;
  ours_open: number;
  ours_overdue: number;
  theirs_open: number;
  /** With the company, who the customer is: its main contact (maybe another company's) and its main partner. */
  main_contact?: RelationRecord | null;
  main_contact_company?: RelationRecord | null;
  main_partner?: RelationRecord | null;
}

export interface FocusItem {
  kind: "overdue" | "reach_out" | "renewal" | "due_today" | "risk";
  company_id: string;
  company_name: string;
  text: string;
  task_id?: string;
}

export interface CustomersOverview {
  customers: CustomerRow[];
  focus: FocusItem[];
  counts: Record<HealthStatus, number>;
  sight: { calls: SyncState; emails: SyncState };
}

/** What happens next on a deal: its soonest dated task (or a meeting with its company, from a calendar synced before the CRM dropped its integrations). */
export interface NextStep {
  kind: "meeting" | "task";
  title: string;
  /** The meeting's start (ISO 8601) or the task's due day (YYYY-MM-DD). */
  at: string;
  owed_by: OwedBy | null;
  overdue: boolean;
}

export interface DealProgress {
  status: HealthStatus;
  /** Why, worst first, in plain words; empty when on track. */
  reasons: string[];
  next_step: NextStep | null;
  last_touch_at: string | null;
  days_quiet: number | null;
}

export interface DealsProgressOverview {
  deals: Deal[];
  total: number;
  counts: Record<HealthStatus, number>;
  sight: { calls: SyncState; emails: SyncState };
}

// ── Accounts ──

export type Role = "admin" | "user";

/** The signed-in person (GET /api/auth/me). */
export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
}

/** An account as an admin manages it (GET /api/users). Timestamps are SQLite UTC, "YYYY-MM-DD HH:MM:SS". */
export interface Account extends User {
  disabled: boolean;
  created_at: string;
  updated_at: string;
}

/** One of your API tokens. Its secret is shown once, when it is created. */
export interface ApiToken {
  id: string;
  name: string;
  /** The secret's first 12 characters, so you can tell tokens apart. */
  prefix: string;
  last_used_at: string | null;
  created_at: string;
}
