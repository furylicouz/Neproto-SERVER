"use client";

import type { ReactNode } from "react";

import { AlertCircle, LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";

export function AdminLoading() {
  const placeholders = ["first", "second", "third", "fourth"];
  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
      {placeholders.map((placeholder) => (
        <Skeleton className="h-32 rounded-xl" key={placeholder} />
      ))}
    </div>
  );
}

export function AdminError({ message }: { message: string }) {
  return (
    <Alert variant="destructive">
      <AlertCircle />
      <AlertTitle>NeProto control API</AlertTitle>
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
}

export function StateBadge({ state }: { state: string }) {
  const normalized = state.toLowerCase();
  const healthy = ["active", "up", "ready", "ok", "current", "succeeded"].includes(normalized);
  const pending = ["queued", "running", "starting", "updating"].includes(normalized);
  let variant: "default" | "secondary" | "outline" = "outline";
  if (healthy) variant = "default";
  else if (pending) variant = "secondary";
  return (
    <Badge variant={variant}>
      {pending && <LoaderCircle className="animate-spin" />}
      {state}
    </Badge>
  );
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description: string;
  actions?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div className="flex flex-col gap-1">
        <h1 className="font-medium text-3xl leading-none tracking-tight">{title}</h1>
        <p className="text-muted-foreground text-sm">{description}</p>
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}
