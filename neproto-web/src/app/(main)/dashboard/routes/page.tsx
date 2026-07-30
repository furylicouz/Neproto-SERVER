import { getRequestLocale } from "@/server/request-locale";

import { RouteManagement } from "./route-management";

export default async function Page() {
  return <RouteManagement locale={await getRequestLocale()} />;
}
