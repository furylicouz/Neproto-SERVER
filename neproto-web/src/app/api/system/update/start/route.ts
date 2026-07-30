import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { isActiveUpdateState } from "@/lib/update-status-core.mjs";
import { isSameOrigin, requestHasAdminSession } from "@/server/admin-auth";
import { hasNonEmptyBody } from "@/server/bounded-body";
import { readUpdateStatus, requestUpdateAction } from "@/server/update-service";

export async function POST(request: NextRequest) {
  if (!(await requestHasAdminSession(request))) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  if (!isSameOrigin(request) || hasNonEmptyBody(request)) {
    return NextResponse.json({ error: "invalid_request" }, { status: 400 });
  }
  try {
    const status = await readUpdateStatus();
    if (isActiveUpdateState(status.state)) {
      return NextResponse.json({ error: "update_in_progress" }, { status: 409 });
    }
    const created = await requestUpdateAction("apply");
    return NextResponse.json({ accepted: created, pending: !created }, { status: 202 });
  } catch {
    return NextResponse.json({ error: "update_service_unavailable" }, { status: 503 });
  }
}
