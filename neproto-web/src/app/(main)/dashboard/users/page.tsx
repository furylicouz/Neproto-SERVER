import { getRequestLocale } from "@/server/request-locale";

import { NeProtoUsers } from "./_components/neproto-users";

export default async function Page() {
  return <NeProtoUsers locale={await getRequestLocale()} />;
}
