import { getRequestLocale } from "@/server/request-locale";

import { ClusterManagement } from "./cluster-management";

export default async function Page() {
  return <ClusterManagement locale={await getRequestLocale()} />;
}
