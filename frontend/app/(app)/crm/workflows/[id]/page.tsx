"use client";

import { useParams } from "next/navigation";
import { WorkflowEditorPage } from "@/components/crm/workflows-panel";

export default function EditWorkflowPage() {
  const { id } = useParams<{ id: string }>();
  return <WorkflowEditorPage id={id} />;
}
