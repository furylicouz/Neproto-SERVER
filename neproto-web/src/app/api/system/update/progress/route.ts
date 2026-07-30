import type { NextRequest } from "next/server";

import { GET as getUpdateStatus } from "../route";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  return getUpdateStatus(request);
}
