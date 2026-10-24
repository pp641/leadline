"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { FieldError } from "@/components/auth-card";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, errorMessage } from "@/lib/api";
import { leadsService } from "@/services/leads";
import type { Lead } from "@/types/api";

const optionalInt = (min: number, max: number) =>
  z.string().trim().refine((v) => v === "" || (/^\d+$/.test(v) && +v >= min && +v <= max), `Enter a number from ${min} to ${max}`);

const schema = z.object({
  company_name: z.string().trim().min(1, "Required").max(255),
  website: z.string().trim().max(500),
  industry: z.string().trim().max(100),
  city: z.string().trim().max(100),
  state: z.string().trim().max(100),
  country: z.string().trim().max(100),
  phone: z.string().trim().max(50),
  employee_count: optionalInt(0, 10_000_000),
  founded_year: optionalInt(1800, new Date().getFullYear() + 1),
  description: z.string().max(5000),
  tags: z.string(),
  contact_first_name: z.string().trim().max(100),
  contact_last_name: z.string().trim().max(100),
  contact_email: z.union([z.literal(""), z.email("Enter a valid email")]),
  contact_title: z.string().trim().max(150),
});
type Values = z.infer<typeof schema>;

const blank: Values = {
  company_name: "", website: "", industry: "", city: "", state: "", country: "", phone: "", employee_count: "",
  founded_year: "", description: "", tags: "", contact_first_name: "", contact_last_name: "", contact_email: "", contact_title: "",
};

/** Create a lead (with an optional first contact), or edit an existing lead's company fields. */
export function LeadFormDialog({ open, onOpenChange, lead }: { open: boolean; onOpenChange: (o: boolean) => void; lead?: Lead }) {
  const qc = useQueryClient();
  const router = useRouter();
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: blank });

  useEffect(() => {
    if (!open) return;
    form.reset(lead ? {
      ...blank, company_name: lead.company_name, website: lead.website, industry: lead.industry, city: lead.city,
      state: lead.state, country: lead.country, phone: lead.phone, description: lead.description, tags: lead.tags.join(", "),
      employee_count: lead.employee_count?.toString() ?? "", founded_year: lead.founded_year?.toString() ?? "",
    } : blank);
  }, [open, lead, form]);

  const save = useMutation({
    mutationFn: (v: Values) => {
      const body = {
        ...(lead ?? {}),
        company_name: v.company_name, website: v.website, industry: v.industry, city: v.city, state: v.state,
        country: v.country, phone: v.phone, description: v.description,
        employee_count: v.employee_count ? Number(v.employee_count) : null,
        founded_year: v.founded_year ? Number(v.founded_year) : null,
        tags: v.tags.split(",").map((t) => t.trim()).filter(Boolean),
        business_email: lead?.business_email ?? "",
        contact: !lead && (v.contact_email || v.contact_first_name)
          ? { first_name: v.contact_first_name, last_name: v.contact_last_name, email: v.contact_email, job_title: v.contact_title }
          : undefined,
      };
      return lead ? leadsService.update(lead.id, body) : leadsService.create(body);
    },
    onSuccess: (saved) => {
      toast.success(lead ? "Lead updated" : "Lead created");
      qc.invalidateQueries({ queryKey: ["leads"] });
      qc.invalidateQueries({ queryKey: ["lead", saved.id] });
      onOpenChange(false);
      if (!lead) router.push(`/leads/${saved.id}`);
    },
    onError: (e) => {
      if (e instanceof ApiError) {
        for (const [f, m] of Object.entries(e.fields)) {
          if (f in blank) form.setError(f as keyof Values, { message: m });
        }
      }
      toast.error(errorMessage(e));
    },
  });

  const field = (name: keyof Values, label: string, props: React.ComponentProps<typeof Input> = {}) => (
    <div className="space-y-1.5">
      <Label htmlFor={name}>{label}</Label>
      <Input id={name} {...props} {...form.register(name)} />
      <FieldError message={form.formState.errors[name]?.message} />
    </div>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{lead ? "Edit lead" : "Add lead"}</DialogTitle>
          <DialogDescription>Company details. Contacts can be managed on the lead page.</DialogDescription>
        </DialogHeader>
        <form onSubmit={form.handleSubmit((v) => save.mutate(v))} className="space-y-4" noValidate>
          <div className="grid gap-3 sm:grid-cols-2">
            {field("company_name", "Company name")}
            {field("website", "Website", { placeholder: "example.com" })}
            {field("industry", "Industry")}
            {field("phone", "Phone")}
            {field("city", "City")}
            {field("state", "State / region")}
            {field("country", "Country")}
            {field("tags", "Tags (comma-separated)")}
            {field("employee_count", "Employees", { inputMode: "numeric" })}
            {field("founded_year", "Founded / opened (year)", { inputMode: "numeric" })}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="description">Description</Label>
            <Textarea id="description" rows={2} {...form.register("description")} />
          </div>
          {!lead && (
            <fieldset className="space-y-3 rounded-lg border p-3">
              <legend className="px-1 text-sm font-medium">First contact (optional)</legend>
              <div className="grid gap-3 sm:grid-cols-2">
                {field("contact_first_name", "First name")}
                {field("contact_last_name", "Last name")}
                {field("contact_email", "Email", { type: "email" })}
                {field("contact_title", "Job title")}
              </div>
            </fieldset>
          )}
          <DialogFooter>
            <Button type="submit" disabled={save.isPending}>{lead ? "Save" : "Create lead"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
