import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

import { ADMIN_SESSION_COOKIE, createSessionToken, credentialsMatch, isSameOrigin } from "@/server/admin-auth";
import { readBoundedJSON } from "@/server/bounded-body";
import { clearLoginFailures, isLoginBlocked, loginClientKey, recordLoginFailure } from "@/server/login-rate-limit";

export async function POST(request: NextRequest) {
  if (!isSameOrigin(request)) {
    return NextResponse.json({ error: "invalid_request" }, { status: 403 });
  }
  const client = loginClientKey(request);
  if (isLoginBlocked(client)) {
    return NextResponse.json({ error: "authentication_failed" }, { status: 429 });
  }
  try {
    const body = await readBoundedJSON(request, 1024);
    if (
      body === null ||
      typeof body !== "object" ||
      Array.isArray(body) ||
      typeof (body as { password?: unknown }).password !== "string" ||
      typeof (body as { remember?: unknown }).remember !== "boolean" ||
      (body as { password: string }).password.length > 256
    ) {
      throw new Error("invalid login request");
    }
    const { password, remember } = body as { password: string; remember: boolean };
    if (!(await credentialsMatch(password))) {
      recordLoginFailure(client);
      return NextResponse.json({ error: "authentication_failed" }, { status: 401 });
    }
    clearLoginFailures(client);
    const { token, maxAge } = await createSessionToken(remember);
    const response = NextResponse.json({ authenticated: true });
    response.cookies.set(ADMIN_SESSION_COOKIE, token, {
      httpOnly: true,
      sameSite: "strict",
      secure: request.nextUrl.protocol === "https:" || request.headers.get("x-forwarded-proto") === "https",
      path: "/",
      maxAge,
    });
    return response;
  } catch {
    recordLoginFailure(client);
    return NextResponse.json({ error: "authentication_failed" }, { status: 401 });
  }
}

export async function DELETE(request: NextRequest) {
  if (!isSameOrigin(request)) {
    return NextResponse.json({ error: "invalid_request" }, { status: 403 });
  }
  const response = NextResponse.json({ authenticated: false });
  response.cookies.set(ADMIN_SESSION_COOKIE, "", {
    httpOnly: true,
    sameSite: "strict",
    secure: request.nextUrl.protocol === "https:" || request.headers.get("x-forwarded-proto") === "https",
    path: "/",
    maxAge: 0,
  });
  return response;
}
