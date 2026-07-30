import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { requestHasAdminSession } from "@/server/admin-auth";
import { readUpdateStatus } from "@/server/update-service";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  if (!(await requestHasAdminSession(request))) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  try {
    return NextResponse.json(await readUpdateStatus(), {
      headers: { "Cache-Control": "no-store" },
    });
  } catch {
    return NextResponse.json({ error: "status_unavailable" }, { status: 503 });
  }
}
