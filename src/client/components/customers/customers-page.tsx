import { useCallback, useEffect, useRef, useState } from "react";
import { AlarmClock, Check, PhoneCall, TriangleAlert, CalendarCheck, CalendarClock } from "lucide-react";
import { useCrm } from "@/context";
import { api } from "@/api";
import { EntityIcon, EmptyState, PageHeader } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Eyebrow, HEALTH, HealthPill, StatusPill, daysAgo, sightNotice, tzOffset } from "@/components/health";
import { cn, formatDate } from "@/lib/utils";
import type { CustomersOverview, FocusItem } from "@/types";

const FOCUS_ICON: Record<FocusItem["kind"], typeof AlarmClock> = {
  overdue: AlarmClock,
  due_today: CalendarCheck,
  reach_out: PhoneCall,
  renewal: CalendarClock,
  risk: TriangleAlert,
};

/**
 * Customers: how each account is doing, worst first, with the reasons in plain
 * words, and the few things worth doing first. A company is a customer from
 * its "Customer since" day (set on its page, or from the close date of its
 * first won deal).
 */
export function CustomersPage({ navigate }: { navigate: (to: string) => void }) {
  const { setError, changes } = useCrm();
  const [data, setData] = useState<CustomersOverview | null>(null);

  const load = useCallback(async () => {
    try {
      setData(await api<CustomersOverview>("GET", `/api/customers?tz=${tzOffset()}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not load customers");
    }
  }, [setError]);

  const seen = useRef(changes);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (seen.current === changes) return;
    seen.current = changes;
    void load();
  }, [changes, load]);

  const done = async (taskId: string) => {
    try {
      await api("PUT", `/api/tasks/${encodeURIComponent(taskId)}`, { done: true });
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save the task");
    }
  };

  const open = (id: string) => navigate(`/companies/${encodeURIComponent(id)}`);
  const notice = data ? sightNotice(data.sight, "customers") : null;

  if (!data) {
    return (
      <>
        <PageHeader title="Customers" />
        <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>
      </>
    );
  }

  return (
    <>
      <PageHeader title="Customers" count={data.customers.length}>
        {data.customers.length > 0 && (
          <div className="flex items-center gap-1.5">
            {(["red", "yellow", "unknown", "green"] as const).map((s) => data.counts[s] > 0 && (
              <StatusPill key={s} tone={HEALTH[s].tone}><span className="tabular">{data.counts[s]}</span> {HEALTH[s].label.toLowerCase()}</StatusPill>
            ))}
          </div>
        )}
      </PageHeader>

      {data.customers.length === 0 ? (
        <EmptyState
          title="No customers yet. A company becomes a customer when one of its deals is won (from that deal's close date), or when you set Customer since on its page."
          action={<Button size="sm" variant="outline" onClick={() => navigate("/companies")}>Go to companies</Button>}
        />
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="flex flex-col gap-8 p-6">
            {notice && <p className="rounded-md bg-info-tint px-3 py-2 text-[0.8125rem] text-info">{notice}</p>}

            {data.focus.length > 0 && (
              <section className="flex flex-col gap-3">
                <Eyebrow>Today</Eyebrow>
                <div className="grid gap-3 md:grid-cols-3">
                  {data.focus.map((f, i) => {
                    const Icon = FOCUS_ICON[f.kind];
                    return (
                      <div key={`${f.kind}-${f.task_id ?? f.company_id}-${i}`} className="flex flex-col gap-3 rounded-md bg-card p-3.5 shadow-edge">
                        <div className="flex items-center gap-2 text-[0.8125rem] text-muted-foreground">
                          <Icon className={cn("size-3.5", (f.kind === "overdue" || f.kind === "risk") && "text-destructive")} />
                          <button type="button" className="truncate hover:text-foreground hover:underline" onClick={() => open(f.company_id)}>{f.company_name}</button>
                        </div>
                        <p className="text-sm">{f.text}</p>
                        <div className="mt-auto flex gap-2">
                          {f.task_id ? (
                            <Button size="sm" variant="outline" className="h-7" onClick={() => void done(f.task_id!)}><Check className="size-3.5" strokeWidth={2.5} /> Done</Button>
                          ) : (
                            <Button size="sm" variant="outline" className="h-7" onClick={() => open(f.company_id)}>Open</Button>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </section>
            )}

            <section className="flex flex-col gap-3">
              <Eyebrow>Accounts</Eyebrow>
              <div className="overflow-hidden rounded-md shadow-edge">
                <Table grid>
                  <TableHeader>
                    <TableRow>
                      <TableHead pinned width={220}>Company</TableHead>
                      <TableHead width={340}>Status</TableHead>
                      <TableHead width={120}>Last contact</TableHead>
                      <TableHead width={120}>Renews</TableHead>
                      <TableHead width={130} className="text-right">Our promises</TableHead>
                      <TableHead width={120} className="text-right">Waiting on them</TableHead>
                      <TableHead aria-hidden="true" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.customers.map((c) => (
                      <TableRow key={c.id} className="cursor-pointer" onClick={() => open(c.id)}>
                        <TableCell pinned>
                          <a href={`/companies/${encodeURIComponent(c.id)}`} onClick={(e) => { e.preventDefault(); e.stopPropagation(); open(c.id); }} className="flex min-w-0 items-center gap-2 font-medium">
                            <EntityIcon name={c.name} domain={c.domain} className="size-5" />
                            <span className="truncate">{c.name}</span>
                          </a>
                        </TableCell>
                        <TableCell>
                          <div className="flex min-w-0 items-center gap-2" title={c.reasons.join("\n") || undefined}>
                            <HealthPill status={c.status} />
                            <span className="truncate text-muted-foreground">{c.reasons[0] ?? ""}</span>
                          </div>
                        </TableCell>
                        <TableCell className="tabular">{daysAgo(c.days_quiet)}</TableCell>
                        <TableCell className="tabular">{c.renewal_date ? formatDate(c.renewal_date) : <span className="text-faint">Not set</span>}</TableCell>
                        <TableCell className="text-right tabular">
                          {c.ours_open}
                          {c.ours_overdue > 0 && <span className="text-destructive"> · {c.ours_overdue} late</span>}
                        </TableCell>
                        <TableCell className="text-right tabular">{c.theirs_open || <span className="text-faint">0</span>}</TableCell>
                        <TableCell aria-hidden="true" />
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>
          </div>
        </div>
      )}
    </>
  );
}
