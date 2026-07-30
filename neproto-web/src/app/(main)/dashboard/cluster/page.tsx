import { getRequestLocale } from "@/server/request-locale";

import { ClusterManagement } from "./cluster-management";

// The live cluster table reuses the location flag treatment from Infrastructure Overview.
import "@/styles/flag-icons/flags.css";

export default async function Page() {
  return <ClusterManagement locale={await getRequestLocale()} />;
}
