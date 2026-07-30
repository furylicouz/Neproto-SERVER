import { APP_MESSAGES } from "@/lib/i18n";
import { getRequestLocale } from "@/server/request-locale";

import { NeProtoOverview } from "./_components/neproto-overview";

export default async function Page() {
  const locale = await getRequestLocale();
  const title = APP_MESSAGES[locale].navigation.dashboard;

  return (
    <div className="@container/main flex flex-col gap-4 md:gap-6">
      <h1 className="font-semibold text-2xl tracking-tight">{title}</h1>
      <NeProtoOverview locale={locale} />
    </div>
  );
}
