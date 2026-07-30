import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { isAllowedAdminControlRequest, normalizeAdminControlPath } from "@/lib/admin-control-core.mjs";
import { isSameOrigin, requestHasAdminSession } from "@/server/admin-auth";
import { requestAdminControl } from "@/server/admin-control-client";
import { readBoundedJSON } from "@/server/bounded-body";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const MAXIMUM_REQUEST_BYTES = 64 * 1024;

interface RouteContext {
  params: Promise<{ path: string[] }>;
}

async function proxyAdminRequest(request: NextRequest, context: RouteContext) {
  if (!(await requestHasAdminSession(request))) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  const method = request.method.toUpperCase();
  if (method !== "GET" && !isSameOrigin(request)) {
    return NextResponse.json({ error: "invalid_request" }, { status: 403 });
  }
  const segments = (await context.params).path;
  if (!isAllowedAdminControlRequest(method, segments)) {
    return NextResponse.json({ error: "not_found" }, { status: 404 });
  }

  let pathname: string;
  try {
    pathname = normalizeAdminControlPath(segments);
  } catch {
    return NextResponse.json({ error: "not_found" }, { status: 404 });
  }

  if (method === "GET") {
    if (segments.length === 3 && segments[0] === "users" && segments[2] === "export") {
      const format = request.nextUrl.searchParams.get("format") || "uri";
      if (
        !new Set(["uri", "json", "manual", "qr"]).has(format) ||
        [...request.nextUrl.searchParams.keys()].some((key) => key !== "format")
      ) {
        return NextResponse.json({ error: "invalid_request" }, { status: 400 });
      }
      pathname += `?format=${encodeURIComponent(format)}`;
    } else if (request.nextUrl.search) {
      return NextResponse.json({ error: "invalid_request" }, { status: 400 });
    }
  }

  try {
    const body = method === "GET" ? undefined : JSON.stringify(await readBoundedJSON(request, MAXIMUM_REQUEST_BYTES));
    const response = await requestAdminControl(method, pathname, body);
    const responseBody = Uint8Array.from(response.body).buffer;
    return new NextResponse(responseBody, {
      status: response.status,
      headers: { "Content-Type": response.contentType, "Cache-Control": "no-store" },
    });
  } catch {
    return NextResponse.json({ error: "control_service_unavailable" }, { status: 503 });
  }
}

export const GET = proxyAdminRequest;
export const POST = proxyAdminRequest;
export const PATCH = proxyAdminRequest;
export const DELETE = proxyAdminRequest;
