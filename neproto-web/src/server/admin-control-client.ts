import "server-only";

import { request as httpRequest } from "node:http";
import path from "node:path";

const DEFAULT_CONTROL_SOCKET = "/run/neproto/control.sock";
const MAXIMUM_RESPONSE_BYTES = 1024 * 1024;
const REQUEST_TIMEOUT_MS = 30_000;

export interface AdminControlResponse {
  status: number;
  body: Uint8Array;
  contentType: string;
}

function controlSocketPath() {
  const configured = process.env.NEPROTO_CONTROL_SOCKET || DEFAULT_CONTROL_SOCKET;
  if (!path.isAbsolute(configured) || configured.length > 256) {
    throw new Error("invalid NeProto control socket path");
  }
  return configured;
}

export async function requestAdminControl(
  method: string,
  pathname: string,
  body?: string,
): Promise<AdminControlResponse> {
  return new Promise((resolve, reject) => {
    const request = httpRequest(
      {
        socketPath: controlSocketPath(),
        path: pathname,
        method,
        headers:
          body === undefined
            ? undefined
            : { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(body) },
      },
      (response) => {
        const chunks: Uint8Array[] = [];
        let total = 0;
        response.on("data", (chunk: Buffer) => {
          total += chunk.byteLength;
          if (total > MAXIMUM_RESPONSE_BYTES) {
            response.destroy(new Error("control response exceeds size limit"));
            return;
          }
          chunks.push(chunk);
        });
        response.on("end", () => {
          resolve({
            status: response.statusCode || 502,
            body: Buffer.concat(chunks, total),
            contentType: String(response.headers["content-type"] || "application/json; charset=utf-8"),
          });
        });
        response.on("error", reject);
      },
    );
    request.setTimeout(REQUEST_TIMEOUT_MS, () => request.destroy(new Error("control request timed out")));
    request.on("error", reject);
    if (body !== undefined) {
      request.end(body);
    } else {
      request.end();
    }
  });
}
