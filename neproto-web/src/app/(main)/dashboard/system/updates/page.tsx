import { APP_MESSAGES } from "@/lib/i18n";
import { getRequestLocale } from "@/server/request-locale";

import { UpdatePanel } from "./update-panel";

export default async function Page() {
  const locale = await getRequestLocale();
  return <UpdatePanel locale={locale} messages={APP_MESSAGES[locale].updates} />;
}
