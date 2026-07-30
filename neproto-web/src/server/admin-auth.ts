import "server-only";

import { cookies } from "next/headers";
import type { NextRequest } from "next/server";

import {
  createAdminSession,
  normalizeAdminSecret,
  secureEqual,
  verifyAdminSession,
} from "@/lib/admin-session-core.mjs";

import { randomBytes } from "node:crypto";
import { open } from "node:fs/promises";

export const ADMIN_SESSION_COOKIE = "neproto_admin";

const DEFAULT_SECRET_FILE = "/etc/neproto/web-admin.secret";
const SHORT_SESSION_SECONDS = 8 * 60 * 60;
const REMEMBERED_SESSION_SECONDS = 30 * 24 * 60 * 60;
const MAX_SECRET_BYTES = 512;

export async function readAdminSecret(): Promise<string> {
  if (process.env.NODE_ENV !== "production") {
    const developmentSecret = process.env.NEPROTO_DEV_ADMIN_SECRET;
    if (developmentSecret && developmentSecret.length >= 32 && developmentSecret.length <= 256) {
      return developmentSecret;
    }
  }
  const file = await open(DEFAULT_SECRET_FILE, "r");
  try {
    const stats = await file.stat();
    if (!stats.isFile() || stats.size < 32 || stats.size > MAX_SECRET_BYTES) {
      throw new Error("invalid administrator secret");
    }
    const buffer = Buffer.alloc(Number(stats.size));
    const { bytesRead } = await file.read(buffer, 0, buffer.length, 0);
    const secret = buffer.subarray(0, bytesRead).toString("utf8").trim();
    if (secret.length < 32 || secret.length > 256 || /[\r\n\0]/.test(secret)) {
      throw new Error("invalid administrator secret");
    }
    return secret;
  } finally {
    await file.close();
  }
}

export async function createSessionToken(remember: boolean): Promise<{ token: string; maxAge: number }> {
  const secret = await readAdminSecret();
  const maxAge = remember ? REMEMBERED_SESSION_SECONDS : SHORT_SESSION_SECONDS;
  const token = createAdminSession(secret, Math.floor(Date.now() / 1000), maxAge, randomBytes(16));
  return { token, maxAge };
}

export async function credentialsMatch(candidate: string): Promise<boolean> {
  const secret = await readAdminSecret();
  return secureEqual(normalizeAdminSecret(candidate), secret);
}

export async function hasAdminSession(): Promise<boolean> {
  const cookieStore = await cookies();
  const token = cookieStore.get(ADMIN_SESSION_COOKIE)?.value;
  return verifyToken(token);
}

export async function requestHasAdminSession(request: NextRequest): Promise<boolean> {
  return verifyToken(request.cookies.get(ADMIN_SESSION_COOKIE)?.value);
}

async function verifyToken(token: string | undefined): Promise<boolean> {
  if (!token) {
    return false;
  }
  try {
    const secret = await readAdminSecret();
    return verifyAdminSession(token, secret, Math.floor(Date.now() / 1000));
  } catch {
    return false;
  }
}

export function isSameOrigin(request: NextRequest): boolean {
  const origin = request.headers.get("origin");
  if (!origin) {
    return false;
  }
  const forwardedHost = singleForwardedValue(request.headers.get("x-forwarded-host"));
  const host = forwardedHost || request.headers.get("host");
  const forwardedProtocol = singleForwardedValue(request.headers.get("x-forwarded-proto"));
  const protocol = forwardedProtocol || new URL(request.url).protocol.replace(":", "");
  if (!host || !/^[A-Za-z0-9.:[\]-]+$/.test(host) || (protocol !== "http" && protocol !== "https")) {
    return false;
  }
  try {
    return new URL(origin).origin === `${protocol}://${host}`;
  } catch {
    return false;
  }
}

function singleForwardedValue(value: string | null): string {
  if (!value || value.includes(",")) {
    return "";
  }
  return value.trim();
}
