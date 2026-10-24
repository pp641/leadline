"use client";

import { useState } from "react";
import { initialTab } from "@/lib/url";
import { PageHeader } from "@/components/shared";
import { AgencyTab } from "@/components/settings/agency-tab";
import { DeveloperTab } from "@/components/settings/developer-tab";
import { ComplianceTab, MembersTab, OrganizationTab } from "@/components/settings/org-tabs";
import { MailboxesTab } from "@/components/settings/mailboxes-tab";
import { ScoringTab } from "@/components/settings/scoring-tab";
import { SuppressionTab } from "@/components/settings/suppression-tab";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

const TABS = ["organization", "mailboxes", "members", "suppression", "scoring", "agency", "compliance", "developer"] as const;

export default function SettingsPage() {
  const [tab, setTab] = useState<string>(() => initialTab(TABS, "organization"));
  return (
    <>
      <PageHeader title="Settings" />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4 flex-wrap">
          {TABS.map((t) => <TabsTrigger key={t} value={t} className="capitalize">{t}</TabsTrigger>)}
        </TabsList>
        <TabsContent value="organization"><OrganizationTab /></TabsContent>
        <TabsContent value="mailboxes"><MailboxesTab /></TabsContent>
        <TabsContent value="members"><MembersTab /></TabsContent>
        <TabsContent value="suppression"><SuppressionTab /></TabsContent>
        <TabsContent value="scoring"><ScoringTab /></TabsContent>
        <TabsContent value="agency"><AgencyTab /></TabsContent>
        <TabsContent value="compliance"><ComplianceTab /></TabsContent>
        <TabsContent value="developer"><DeveloperTab /></TabsContent>
      </Tabs>
    </>
  );
}
