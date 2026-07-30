import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { isSameOrigin, requestHasAdminSession } from "@/server/admin-auth";
import { requestAdminControl } from "@/server/admin-control-client";
import { hasNonEmptyBody } from "@/server/bounded-body";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: NextRequest) {
  if (!(await requestHasAdminSession(request))) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  if (!isSameOrigin(request) || hasNonEmptyBody(request)) {
    return NextResponse.json({ error: "invalid_request" }, { status: 400 });
  }
  try {
    const response = await requestAdminControl("POST", "/v1/system/update/check");
    return new NextResponse(Uint8Array.from(response.body).buffer, {
      status: response.status,
      headers: { "Content-Type": response.contentType, "Cache-Control": "no-store" },
    });
  } catch {
    return NextResponse.json({ error: "update_service_unavailable" }, { status: 503 });
  }
}
