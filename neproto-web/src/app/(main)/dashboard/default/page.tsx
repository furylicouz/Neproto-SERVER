import { getRequestLocale } from "@/server/request-locale";

import { MetricCards } from "./_components/metric-cards";
import { PerformanceOverview } from "./_components/performance-overview";
import { SubscriberOverview } from "./_components/subscriber-overview";

export default async function Page() {
  const locale = await getRequestLocale();

  return (
    <div className="@container/main flex flex-col gap-4 md:gap-6">
      <MetricCards locale={locale} />
      <PerformanceOverview locale={locale} />
      <SubscriberOverview locale={locale} />
    </div>
  );
}
