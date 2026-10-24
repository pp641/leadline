"use client";

import Link from "next/link";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, CheckCircle2, FileSpreadsheet, Loader2, Upload } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { PageHeader, StatusBadge, dateTime, num } from "@/components/shared";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api";
import type { Import } from "@/types/api";

const FIELDS = [
  "company_name", "website", "email", "first_name", "last_name", "job_title", "phone", "industry", "description",
  "country", "state", "city", "address", "linkedin_url", "employee_count", "founded_year", "business_email", "tags", "source_url",
];
const CONSENT = [
  { value: "legitimate_interest", label: "Legitimate interest (B2B)" },
  { value: "public_business_contact", label: "Publicly listed business contact" },
  { value: "consent", label: "Consent obtained" },
  { value: "contract", label: "Existing customer / contract" },
];

export default function ImportPage() {
  const qc = useQueryClient();
  const [importId, setImportId] = useState<string | null>(null);
  const [file, setFile] = useState<File | null>(null);

  const imp = useQuery({
    queryKey: ["import", importId],
    queryFn: () => api.get<Import>(`/imports/${importId}`),
    enabled: !!importId,
    refetchInterval: (q) => (["queued", "processing"].includes(q.state.data?.status ?? "") ? 1500 : false),
  });
  const history = useQuery({ queryKey: ["imports"], queryFn: () => api.get<Import[]>("/imports") });

  const upload = useMutation({
    mutationFn: async () => {
      const fd = new FormData();
      fd.append("file", file!);
      return api.post<Import>("/imports", fd);
    },
    onSuccess: (i) => { setImportId(i.id); qc.setQueryData(["import", i.id], i); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remap = useMutation({
    mutationFn: (body: { mapping?: Record<string, string>; source?: string; consent_basis?: string }) =>
      api.put<Import>(`/imports/${importId}/mapping`, { mapping: imp.data!.mapping, source: imp.data!.source, consent_basis: imp.data!.consent_basis, ...body }),
    onSuccess: (i) => qc.setQueryData(["import", i.id], i),
    onError: (e) => toast.error(errorMessage(e)),
  });
  const commit = useMutation({
    mutationFn: () => api.post<Import>(`/imports/${importId}/commit`),
    onSuccess: (i) => { qc.setQueryData(["import", i.id], i); qc.invalidateQueries({ queryKey: ["imports"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  const i = imp.data;
  const newRows = i ? i.valid_rows - i.duplicate_rows : 0;
  const busy = i && ["queued", "processing"].includes(i.status);

  return (
    <>
      <Button variant="ghost" size="sm" asChild className="mb-2 -ml-2"><Link href="/leads"><ArrowLeft /> Leads</Link></Button>
      <PageHeader title="Import leads" description="Upload a CSV, map its columns, review the preview, then import." />

      {!i && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">1. Upload a CSV file</CardTitle>
            <CardDescription>
              UTF-8 CSV up to 20 MB / 50,000 rows. Columns such as <code>company_name, website, email, first_name, industry, city</code> are matched automatically.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center">
            <Input type="file" accept=".csv,text/csv" onChange={(e) => setFile(e.target.files?.[0] ?? null)} className="sm:max-w-sm" />
            <Button disabled={!file || upload.isPending} onClick={() => upload.mutate()}>
              {upload.isPending ? <Loader2 className="animate-spin" /> : <Upload />} Upload & preview
            </Button>
          </CardContent>
        </Card>
      )}

      {i && i.status !== "completed" && !busy && i.status !== "failed" && (
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Stat label="Rows in file" value={i.total_rows} />
            <Stat label="New leads" value={newRows} tone="good" />
            <Stat label="Duplicates (skipped)" value={i.duplicate_rows} />
            <Stat label="Invalid (skipped)" value={i.invalid_rows} tone={i.invalid_rows ? "warn" : undefined} />
          </div>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">2. Map columns</CardTitle>
              <CardDescription>Each column can map to one field. <strong>company_name</strong> is required.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {i.headers.map((h, idx) => (
                <div key={idx} className="space-y-1">
                  <Label className="truncate text-xs text-muted-foreground">Column “{h}”</Label>
                  <FilterSelect className="w-full" value={i.mapping[String(idx)] ?? ""} placeholder="— skip —"
                    options={FIELDS.map((f) => ({ value: f, label: f }))}
                    onChange={(f) => remap.mutate({ mapping: { ...i.mapping, [String(idx)]: f } })} />
                </div>
              ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">3. Source & lawful basis</CardTitle>
              <CardDescription>Recorded on every imported lead so you can show where contact data came from.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label>Source label</Label>
                <Input defaultValue={i.source} onBlur={(e) => e.target.value !== i.source && remap.mutate({ source: e.target.value })} />
              </div>
              <div className="space-y-1">
                <Label>Lawful basis for contact</Label>
                <FilterSelect className="w-full" value={i.consent_basis} placeholder="Choose" options={CONSENT}
                  onChange={(v) => v && remap.mutate({ consent_basis: v })} />
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-base">4. Preview</CardTitle></CardHeader>
            <CardContent className="space-y-4">
              <PreviewTable rows={i.preview?.rows ?? []} />
              {(i.preview?.problems.length ?? 0) > 0 && (
                <details>
                  <summary className="cursor-pointer text-sm font-medium">Skipped rows ({num(i.invalid_rows + i.duplicate_rows)})</summary>
                  <ul className="mt-2 max-h-64 space-y-1 overflow-auto text-sm">
                    {i.preview!.problems.map((p) => (
                      <li key={p.row}>
                        <span className="text-muted-foreground">Row {p.row}:</span> {p.values.company_name || "(no company)"} —{" "}
                        <span className={p.errors?.length ? "text-destructive" : "text-muted-foreground"}>{p.errors?.join("; ") || p.duplicate}</span>
                      </li>
                    ))}
                  </ul>
                </details>
              )}
              <div className="flex flex-wrap gap-2">
                <Button disabled={newRows === 0 || commit.isPending || remap.isPending} onClick={() => commit.mutate()}>
                  Import {num(newRows)} leads
                </Button>
                <Button variant="ghost" onClick={() => { setImportId(null); setFile(null); }}>Start over</Button>
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      {i && (busy || i.status === "completed" || i.status === "failed") && (
        <Card>
          <CardContent className="space-y-4 pt-6">
            {busy && (
              <>
                <p className="flex items-center gap-2 font-medium"><Loader2 className="size-4 animate-spin" /> Importing…</p>
                <Progress value={newRows ? (100 * i.imported_rows) / newRows : 0} />
                <p className="text-sm text-muted-foreground">{num(i.imported_rows)} of {num(newRows)} leads</p>
              </>
            )}
            {i.status === "completed" && (
              <Alert>
                <CheckCircle2 />
                <AlertTitle>Imported {num(i.imported_rows)} leads</AlertTitle>
                <AlertDescription>Email verification and scoring are running in the background.</AlertDescription>
              </Alert>
            )}
            {i.status === "failed" && (
              <Alert variant="destructive">
                <AlertTriangle />
                <AlertTitle>Import failed</AlertTitle>
                <AlertDescription>{i.error}</AlertDescription>
              </Alert>
            )}
            {!busy && (
              <div className="flex gap-2">
                <Button asChild><Link href="/leads">View leads</Link></Button>
                <Button variant="outline" onClick={() => { setImportId(null); setFile(null); }}>Import another file</Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      <h2 className="mt-8 mb-3 text-sm font-medium text-muted-foreground">Recent imports</h2>
      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>File</TableHead><TableHead>Status</TableHead><TableHead className="text-right">Rows</TableHead>
              <TableHead className="text-right">Imported</TableHead><TableHead>Started</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(history.data ?? []).map((h) => (
              <TableRow key={h.id} className="cursor-pointer" onClick={() => setImportId(h.id)}>
                <TableCell className="flex items-center gap-2"><FileSpreadsheet className="size-4 text-muted-foreground" />{h.filename}</TableCell>
                <TableCell><StatusBadge status={h.status} /></TableCell>
                <TableCell className="text-right tabular-nums">{num(h.total_rows)}</TableCell>
                <TableCell className="text-right tabular-nums">{num(h.imported_rows)}</TableCell>
                <TableCell>{dateTime(h.created_at)}</TableCell>
              </TableRow>
            ))}
            {history.data?.length === 0 && <TableRow><TableCell colSpan={5} className="text-center text-muted-foreground">No imports yet</TableCell></TableRow>}
          </TableBody>
        </Table>
      </div>
    </>
  );
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: "good" | "warn" }) {
  return (
    <Card className="py-4">
      <CardContent className="px-4">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className={`text-2xl font-semibold tabular-nums ${tone === "warn" ? "text-amber-600 dark:text-amber-400" : ""}`}>{num(value)}</p>
      </CardContent>
    </Card>
  );
}

function PreviewTable({ rows }: { rows: { row: number; values: Record<string, string> }[] }) {
  const cols = Array.from(new Set(rows.flatMap((r) => Object.keys(r.values)))).sort((a, b) => FIELDS.indexOf(a) - FIELDS.indexOf(b));
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No new rows to import.</p>;
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-12">Row</TableHead>
            {cols.map((c) => <TableHead key={c}>{c}</TableHead>)}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow key={r.row}>
              <TableCell className="text-muted-foreground">{r.row}</TableCell>
              {cols.map((c) => <TableCell key={c} className="max-w-48 truncate">{r.values[c] ?? ""}</TableCell>)}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
