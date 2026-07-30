export type UpdateState =
  | "idle"
  | "checking"
  | "downloading"
  | "verifying"
  | "extracting"
  | "backing_up"
  | "installing"
  | "restarting"
  | "succeeded"
  | "failed";

export interface UpdateStatus {
  schema: 1;
  state: UpdateState;
  current_version: string;
  available_version?: string;
  update_available: boolean;
  progress: number;
  message: string;
  error_code?: string;
  updated_at: string;
}

export function isActiveUpdateState(state: string): boolean;
export function parseUpdateStatus(input: string): UpdateStatus;
