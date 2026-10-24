import type { Metadata } from "next";
import { LegalPage } from "@/components/legal/legal-page";

export const metadata: Metadata = { title: "Terms of Service" };

export default function TermsPage() {
  return (
    <LegalPage title="Terms of Service" sections={[
      ["Who these terms are between", "Identify the operator of this service and the customer organization."],
      ["Using the service", "Describe permitted use, including that outreach must comply with applicable email-marketing and data-protection laws (CAN-SPAM, GDPR/PECR, India's DPDP Act and others)."],
      ["Accounts and security", "Describe account responsibilities, roles within an organization and how access is revoked."],
      ["Customer data", "Describe ownership of the leads, contacts and content customers bring or create, and how it is exported or deleted."],
      ["Third-party integrations", "Describe that connected providers (data, CRM, sending) are governed by their own terms."],
      ["Availability, liability and termination", "Set out service levels, limitations of liability and how either party can end the agreement."],
    ]} />
  );
}
