import { getRequestLocale } from "@/server/request-locale";

import { NeProtoOverview } from "./_components/neproto-overview";

export default async function Page() {
  const locale = await getRequestLocale();
  return <NeProtoOverview locale={locale} />;
}
