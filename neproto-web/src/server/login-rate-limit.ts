import "server-only";

import type { NextRequest } from "next/server";

const MAX_CLIENTS = 256;
const WINDOW_MS = 15 * 60 * 1000;
const BLOCK_MS = 60 * 1000;
const MAX_FAILURES = 5;

interface Attempt {
  failures: number;
  firstFailure: number;
  blockedUntil: number;
}

const attempts = new Map<string, Attempt>();

export function loginClientKey(request: NextRequest): string {
  const forwarded = request.headers.get("x-forwarded-for")?.split(",", 1)[0]?.trim();
  const value = forwarded || request.headers.get("x-real-ip") || "direct";
  return value.slice(0, 128);
}

export function isLoginBlocked(key: string, now = Date.now()): boolean {
  prune(now);
  return (attempts.get(key)?.blockedUntil || 0) > now;
}

export function recordLoginFailure(key: string, now = Date.now()): void {
  prune(now);
  const current = attempts.get(key);
  const attempt =
    !current || now - current.firstFailure > WINDOW_MS ? { failures: 0, firstFailure: now, blockedUntil: 0 } : current;
  attempt.failures += 1;
  if (attempt.failures >= MAX_FAILURES) {
    attempt.blockedUntil = now + BLOCK_MS;
  }
  attempts.delete(key);
  attempts.set(key, attempt);
  while (attempts.size > MAX_CLIENTS) {
    attempts.delete(attempts.keys().next().value as string);
  }
}

export function clearLoginFailures(key: string): void {
  attempts.delete(key);
}

function prune(now: number): void {
  for (const [key, attempt] of attempts) {
    if (now - attempt.firstFailure > WINDOW_MS && attempt.blockedUntil <= now) {
      attempts.delete(key);
    }
  }
}
