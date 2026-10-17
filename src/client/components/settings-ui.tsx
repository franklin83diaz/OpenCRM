import type { ReactNode } from "react";

// The pieces settings pages are made of (Settings → Account).

export function Section({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-medium">{title}</h2>
      <p className="text-[0.8125rem] text-muted-foreground">{description}</p>
      <div className="flex flex-col gap-2 pt-1">{children}</div>
    </section>
  );
}
