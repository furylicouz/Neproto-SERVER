import "server-only";

import { parseUpdateStatus, type UpdateStatus } from "@/lib/update-status-core.mjs";

import { constants } from "node:fs";
import { mkdir, open, readFile, stat } from "node:fs/promises";
import path from "node:path";

const DEFAULT_STATE_DIRECTORY = "/var/lib/neproto/update";
const MAX_STATUS_BYTES = 16 * 1024;
const VERSION = /^np2-(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

export async function readUpdateStatus(): Promise<UpdateStatus> {
  const statusPath = path.join(/* turbopackIgnore: true */ stateDirectory(), "status.json");
  try {
    const info = await stat(statusPath);
    if (!info.isFile() || info.size <= 0 || info.size > MAX_STATUS_BYTES) {
      throw new Error("invalid update status file");
    }
    return parseUpdateStatus(await readFile(statusPath, "utf8"));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
      throw error;
    }
    const currentVersion = process.env.NEPROTO_VERSION || "np2-0.4.0";
    if (!VERSION.test(currentVersion)) {
      throw new Error("invalid running NeProto version");
    }
    return {
      schema: 1,
      state: "idle",
      current_version: currentVersion,
      update_available: false,
      progress: 0,
      message: "Update check scheduled",
      updated_at: new Date(0).toISOString(),
    };
  }
}

export async function requestUpdateAction(action: "check" | "apply"): Promise<boolean> {
  const inbox = path.join(/* turbopackIgnore: true */ stateDirectory(), "inbox");
  await mkdir(inbox, { recursive: true, mode: 0o700 });
  const requestPath = path.join(inbox, action);
  try {
    const file = await open(requestPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    try {
      await file.writeFile(`${action}\n`, "utf8");
      await file.sync();
    } finally {
      await file.close();
    }
    return true;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "EEXIST") {
      return false;
    }
    throw error;
  }
}

function stateDirectory(): string {
  if (process.env.NODE_ENV === "production") {
    return DEFAULT_STATE_DIRECTORY;
  }
  const configured = process.env.NEPROTO_DEV_UPDATE_STATE_DIR;
  if (configured) {
    if (!path.isAbsolute(configured)) {
      throw new Error("development update state directory must be absolute");
    }
    return configured;
  }
  return path.join(/* turbopackIgnore: true */ process.cwd(), ".runtime", "update");
}
