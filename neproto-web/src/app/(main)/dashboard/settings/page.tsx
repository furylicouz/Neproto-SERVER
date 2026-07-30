import { getRequestLocale } from "@/server/request-locale";

import { ServerSettings } from "./server-settings";

export default async function Page() {
  return <ServerSettings locale={await getRequestLocale()} />;
}
