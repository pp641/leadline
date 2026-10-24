import Link from "next/link";
import { Send, TriangleAlert } from "lucide-react";

/** Shell for legal documents. The operator supplies the actual text. */
export function LegalPage({ title, sections }: { title: string; sections: [string, string][] }) {
  return (
    <main className="min-h-svh bg-background">
      <header className="border-b">
        <div className="mx-auto flex h-14 max-w-3xl items-center px-4">
          <Link href="/" className="flex items-center gap-2 font-semibold"><span className="flex size-7 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Send className="size-3.5" /></span>Leadline</Link>
        </div>
      </header>
      <article className="mx-auto max-w-3xl space-y-6 px-4 py-12">
        <h1 className="text-3xl font-semibold tracking-tight">{title}</h1>
        <p className="flex gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-sm">
          <TriangleAlert className="size-4 shrink-0 text-amber-600" />
          This page is a template. The operator of this Leadline installation must replace it with its own reviewed legal text before going live.
        </p>
        {sections.map(([h, p]) => (
          <section key={h} className="space-y-2"><h2 className="text-lg font-semibold">{h}</h2><p className="text-muted-foreground">{p}</p></section>
        ))}
      </article>
    </main>
  );
}
