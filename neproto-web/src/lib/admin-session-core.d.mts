export function secureEqual(left: string, right: string): boolean;
export function normalizeAdminSecret(value: unknown): string;
export function createAdminSession(secret: string, nowSeconds: number, ttlSeconds: number, nonce: Buffer): string;
export function verifyAdminSession(token: string, secret: string, nowSeconds: number): boolean;
