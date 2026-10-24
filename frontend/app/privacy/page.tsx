import type { Metadata } from "next";
import { LegalPage } from "@/components/legal/legal-page";

export const metadata: Metadata = { title: "Privacy Policy" };

export default function PrivacyPage() {
  return (
    <LegalPage title="Privacy Policy" sections={[
      ["Data we process", "List account data (name, email), organization data, and the business contact data customers import or collect."],
      ["Why we process it", "State the purposes and legal bases, including the consent basis recorded on every lead."],
      ["Sharing and processors", "List sub-processors such as hosting, email delivery and the integrations a customer connects."],
      ["Retention and deletion", "Explain retention periods and how erasure and suppression requests are honoured."],
      ["Security", "Summarize safeguards such as encrypted credentials, role-based access and audit logging."],
      ["Your rights and contact", "Explain how individuals exercise their rights and how to contact the operator."],
    ]} />
  );
}
