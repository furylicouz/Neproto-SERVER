import "server-only";

import { headers } from "next/headers";

import { resolveAppLocale } from "@/lib/i18n";

export async function getRequestLocale() {
  return resolveAppLocale((await headers()).get("accept-language"));
}
