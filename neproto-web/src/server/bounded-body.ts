import "server-only";

export async function readBoundedJSON(request: Request, maximumBytes: number): Promise<unknown> {
  const declaredLength = Number(request.headers.get("content-length") || "0");
  if (!Number.isFinite(declaredLength) || declaredLength < 0 || declaredLength > maximumBytes) {
    throw new Error("request body exceeds size limit");
  }
  if (!request.body) {
    throw new Error("request body is required");
  }
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    total += value.byteLength;
    if (total > maximumBytes) {
      await reader.cancel();
      throw new Error("request body exceeds size limit");
    }
    chunks.push(value);
  }
  const merged = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(merged));
}

export function hasNonEmptyBody(request: Request): boolean {
  const declared = request.headers.get("content-length");
  if (declared !== null && declared !== "0") {
    return true;
  }
  return request.headers.has("transfer-encoding");
}
