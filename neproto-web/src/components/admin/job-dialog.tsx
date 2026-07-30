"use client";

import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Progress } from "@/components/ui/progress";
import type { ControlJob } from "@/lib/admin-api";

export function JobDialog({
  job,
  title,
  onOpenChange,
}: {
  job: ControlJob | null;
  title: string;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog
      open={job !== null}
      onOpenChange={(open) => job?.state !== "running" && job?.state !== "queued" && onOpenChange(open)}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{job?.stage}</DialogDescription>
        </DialogHeader>
        <Progress value={job?.progress || 0} />
        {job?.error && (
          <p className="rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-destructive text-sm">
            {job.error}
          </p>
        )}
        <div className="text-muted-foreground text-xs tabular-nums">
          {job?.progress || 0}% · {job?.state}
        </div>
      </DialogContent>
    </Dialog>
  );
}
