import { createHash, createHmac, timingSafeEqual } from "node:crypto";

const MAX_SESSION_SECONDS = 31 * 24 * 60 * 60;

export function secureEqual(left, right) {
  const leftDigest = createHash("sha256").update(String(left)).digest();
  const rightDigest = createHash("sha256").update(String(right)).digest();
  return timingSafeEqual(leftDigest, rightDigest);
}

export function normalizeAdminSecret(value) {
  return typeof value === "string" ? value.trim() : "";
}

export function createAdminSession(secret, nowSeconds, ttlSeconds, nonce) {
  if (typeof secret !== "string" || secret.length < 32 || !Number.isSafeInteger(nowSeconds)) {
    throw new Error("invalid session input");
  }
  if (!Number.isSafeInteger(ttlSeconds) || ttlSeconds < 60 || ttlSeconds > MAX_SESSION_SECONDS) {
    throw new Error("invalid session lifetime");
  }
  if (!Buffer.isBuffer(nonce) || nonce.length < 16 || nonce.length > 32) {
    throw new Error("invalid session nonce");
  }
  const expires = nowSeconds + ttlSeconds;
  const payload = `v1.${expires}.${nonce.toString("base64url")}`;
  const signature = createHmac("sha256", secret).update(payload).digest("base64url");
  return `${payload}.${signature}`;
}

export function verifyAdminSession(token, secret, nowSeconds) {
  if (typeof token !== "string" || token.length > 256 || typeof secret !== "string" || secret.length < 32) {
    return false;
  }
  const parts = token.split(".");
  if (
    parts.length !== 4 ||
    parts[0] !== "v1" ||
    !/^\d{1,12}$/.test(parts[1]) ||
    !/^[A-Za-z0-9_-]{22,43}$/.test(parts[2])
  ) {
    return false;
  }
  const expires = Number(parts[1]);
  if (!Number.isSafeInteger(expires) || expires <= nowSeconds || expires > nowSeconds + MAX_SESSION_SECONDS) {
    return false;
  }
  const payload = parts.slice(0, 3).join(".");
  const expected = createHmac("sha256", secret).update(payload).digest("base64url");
  return secureEqual(parts[3], expected);
}
