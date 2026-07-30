import { getRequestLocale } from "@/server/request-locale";

import { BackupManagement } from "./backup-management";

export default async function Page() {
  return <BackupManagement locale={await getRequestLocale()} />;
}
