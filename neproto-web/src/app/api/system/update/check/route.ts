import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { isSameOrigin, requestHasAdminSession } from "@/server/admin-auth";
import { hasNonEmptyBody } from "@/server/bounded-body";
import { requestUpdateAction } from "@/server/update-service";

export async function POST(request: NextRequest) {
  if (!(await requestHasAdminSession(request))) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  if (!isSameOrigin(request) || hasNonEmptyBody(request)) {
    return NextResponse.json({ error: "invalid_request" }, { status: 400 });
  }
  try {
    const created = await requestUpdateAction("check");
    return NextResponse.json({ accepted: created, pending: !created }, { status: 202 });
  } catch {
    return NextResponse.json({ error: "update_service_unavailable" }, { status: 503 });
  }
}
