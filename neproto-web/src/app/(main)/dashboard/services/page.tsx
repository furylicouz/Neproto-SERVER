import { getRequestLocale } from "@/server/request-locale";

import { ServiceManagement } from "./service-management";

export default async function Page() {
  return <ServiceManagement locale={await getRequestLocale()} />;
}
