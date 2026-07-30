import { APP_MESSAGES } from "@/lib/i18n";
import { getRequestLocale } from "@/server/request-locale";

import { MetricCards } from "./default/_components/metric-cards";
import { PerformanceOverview } from "./default/_components/performance-overview";
import { SubscriberOverview } from "./default/_components/subscriber-overview";

export default async function Page() {
  const locale = await getRequestLocale();
  const title = APP_MESSAGES[locale].navigation.dashboard;

  return (
    <div className="@container/main flex flex-col gap-4 md:gap-6">
      <h1 className="font-semibold text-2xl tracking-tight">{title}</h1>
      <MetricCards locale={locale} />
      <PerformanceOverview locale={locale} />
      <SubscriberOverview locale={locale} />
    </div>
  );
}
