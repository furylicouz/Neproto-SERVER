"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { AlertTriangle, CheckCircle2, Download, RefreshCw } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import type { AppLocale } from "@/lib/i18n";
import {
  AUTO_UPDATE_CHECK_INTERVAL_MS,
  isActiveUpdateState,
  parseUpdateStatus,
  shouldAutomaticallyCheckUpdate,
  type UpdateStatus,
} from "@/lib/update-status-core.mjs";

import { GeoDataUpdatePanel } from "./geodata-update-panel";

interface Messages {
  title: string;
  description: string;
  currentVersion: string;
  availableVersion: string;
  noNewVersion: string;
  status: string;
  checkedAt: string;
  check: string;
  checking: string;
  install: string;
  upToDate: string;
  available: string;
  unavailable: string;
  modalTitle: string;
  modalDescription: string;
  close: string;
}

const STAGE_LABELS: Record<AppLocale, Record<string, string>> = {
  en: {
    idle: "Ready",
    checking: "Checking GitHub for updates",
    downloading: "Downloading release bundle",
    verifying: "Verifying SHA-256 checksum",
    extracting: "Extracting verified release",
    backing_up: "Creating transactional backup",
    installing: "Installing server and web application",
    restarting: "Restarting NeProto services",
    succeeded: "Update completed",
    failed: "Update failed",
  },
  ru: {
    idle: "Готово",
    checking: "Проверка обновлений в GitHub",
    downloading: "Загрузка релиза",
    verifying: "Проверка SHA-256",
    extracting: "Распаковка проверенного релиза",
    backing_up: "Создание резервной копии",
    installing: "Установка сервера и веб-приложения",
    restarting: "Перезапуск сервисов NeProto",
    succeeded: "Обновление завершено",
    failed: "Ошибка обновления",
  },
};

const AUTO_CHECK_TIMER_MS = Math.min(60_000, AUTO_UPDATE_CHECK_INTERVAL_MS);
const UPDATE_CHECK_HTTP_TIMEOUT_MS = 15_000;
const UPDATE_POLL_TIMEOUT_MS = 35 * 60_000;

export function UpdatePanel({ locale, messages }: { readonly locale: AppLocale; readonly messages: Messages }) {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);
  const [polling, setPolling] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const requestStartedAt = useRef(0);
  const lastCheckRequestedAt = useRef(0);
  const bootstrapped = useRef(false);

  const loadStatus = useCallback(async () => {
    const response = await fetch("/api/system/update", { cache: "no-store" });
    if (response.status === 401) {
      window.location.assign("/auth/v1/login");
      return null;
    }
    if (!response.ok) {
      throw new Error("status unavailable");
    }
    const nextStatus = parseUpdateStatus(await response.text());
    setStatus(nextStatus);
    setError("");
    return nextStatus;
  }, []);

  const requestAction = useCallback(
    async (action: "check" | "start") => {
      setError("");
      requestStartedAt.current = Date.now();
      if (action === "check") {
        lastCheckRequestedAt.current = requestStartedAt.current;
        setChecking(true);
        const controller = new AbortController();
        const timeout = window.setTimeout(() => controller.abort(), UPDATE_CHECK_HTTP_TIMEOUT_MS);
        try {
          const response = await fetch("/api/system/update/check", { method: "POST", signal: controller.signal });
          if (response.status === 401) {
            window.location.assign("/auth/v1/login");
            return;
          }
          if (!response.ok) {
            throw new Error("check failed");
          }
          setStatus(parseUpdateStatus(await response.text()));
        } catch {
          setError(messages.unavailable);
        } finally {
          window.clearTimeout(timeout);
          setChecking(false);
          setPolling(false);
        }
        return;
      }

      setDialogOpen(true);
      try {
        const response = await fetch("/api/system/update/start", { method: "POST" });
        if (response.status !== 202 && response.status !== 409) {
          throw new Error("request rejected");
        }
        setPolling(true);
      } catch {
        setPolling(false);
        setDialogOpen(false);
        setError(messages.unavailable);
      }
    },
    [messages.unavailable],
  );

  useEffect(() => {
    if (bootstrapped.current) {
      return;
    }
    bootstrapped.current = true;
    let mounted = true;
    const bootstrap = async () => {
      try {
        const nextStatus = await loadStatus();
        if (!mounted || !nextStatus) {
          return;
        }
        if (isActiveUpdateState(nextStatus.state)) {
          requestStartedAt.current = Date.now();
          setChecking(nextStatus.state === "checking");
          setPolling(true);
          return;
        }
        const now = Date.now();
        if (
          shouldAutomaticallyCheckUpdate({
            now,
            updatedAt: nextStatus.updated_at,
            lastRequestedAt: lastCheckRequestedAt.current,
            state: nextStatus.state,
            checking: false,
            polling: false,
            visible: document.visibilityState === "visible",
          })
        ) {
          lastCheckRequestedAt.current = now;
          await requestAction("check");
        }
      } catch {
        if (mounted) {
          setError(messages.unavailable);
        }
      }
    };
    void bootstrap();
    return () => {
      mounted = false;
    };
  }, [loadStatus, messages.unavailable, requestAction]);

  useEffect(() => {
    if (!polling) {
      return;
    }
    const poll = async () => {
      if (requestStartedAt.current > 0 && Date.now() - requestStartedAt.current > UPDATE_POLL_TIMEOUT_MS) {
        setPolling(false);
        setChecking(false);
        setDialogOpen(false);
        setError(messages.unavailable);
        return;
      }
      try {
        const nextStatus = await loadStatus();
        if (!nextStatus) {
          return;
        }
        const statusTime = Date.parse(nextStatus.updated_at);
        const isNewResult = statusTime >= requestStartedAt.current;
        if (isNewResult && !isActiveUpdateState(nextStatus.state)) {
          setPolling(false);
          setChecking(false);
        }
      } catch {
        // A short outage is expected while the unified release restarts the
        // web service. Keep the modal and continue polling the same API.
      }
    };
    const timer = window.setInterval(() => void poll(), 1000);
    void poll();
    return () => window.clearInterval(timer);
  }, [loadStatus, messages.unavailable, polling]);

  useEffect(() => {
    const checkIfStale = () => {
      if (!status) {
        return;
      }
      const now = Date.now();
      if (
        shouldAutomaticallyCheckUpdate({
          now,
          updatedAt: status.updated_at,
          lastRequestedAt: lastCheckRequestedAt.current,
          state: status.state,
          checking,
          polling,
          visible: document.visibilityState === "visible",
        })
      ) {
        lastCheckRequestedAt.current = now;
        void requestAction("check");
      }
    };
    const timer = window.setInterval(checkIfStale, AUTO_CHECK_TIMER_MS);
    document.addEventListener("visibilitychange", checkIfStale);
    window.addEventListener("focus", checkIfStale);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", checkIfStale);
      window.removeEventListener("focus", checkIfStale);
    };
  }, [checking, polling, requestAction, status]);

  const active = status ? isActiveUpdateState(status.state) : false;
  const stageLabel = status ? STAGE_LABELS[locale][status.state] || status.message : "";
  const checkedAt = useMemo(() => {
    if (!status || status.updated_at === new Date(0).toISOString()) {
      return "—";
    }
    return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(new Date(status.updated_at));
  }, [locale, status]);

  if (!status && !error) {
    return (
      <div className="flex flex-col gap-4" aria-busy="true">
        <div className="space-y-2">
          <Skeleton className="h-9 w-48" />
          <Skeleton className="h-5 w-full max-w-2xl" />
        </div>
        <Skeleton className="h-72 w-full" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <h1 className="font-medium text-2xl leading-tight tracking-tight sm:text-3xl sm:leading-none">
          {messages.title}
        </h1>
        <p className="max-w-3xl text-muted-foreground text-sm">{messages.description}</p>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>{messages.unavailable}</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {status && (
        <Card>
          <CardHeader>
            <CardTitle>{messages.status}</CardTitle>
            <CardDescription>{stageLabel}</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-4 md:grid-cols-2">
            <div className="rounded-lg border p-4">
              <div className="text-muted-foreground text-sm">{messages.currentVersion}</div>
              <div className="mt-2 font-medium text-2xl tracking-tight">{status.current_version}</div>
            </div>
            <div className="rounded-lg border p-4">
              <div className="text-muted-foreground text-sm">{messages.availableVersion}</div>
              <div className="mt-2 flex items-center gap-2">
                <span className="font-medium text-2xl tracking-tight">
                  {status.available_version || messages.noNewVersion}
                </span>
                <Badge variant="outline">{status.update_available ? messages.available : messages.upToDate}</Badge>
              </div>
            </div>
            <div className="md:col-span-2">
              <div className="mb-2 flex items-center justify-between text-sm">
                <span>{stageLabel}</span>
                <span className="tabular-nums">{status.progress}%</span>
              </div>
              <Progress value={status.progress} aria-label={stageLabel} />
              <div className="mt-2 text-muted-foreground text-xs">
                {messages.checkedAt}: {checkedAt}
              </div>
            </div>
            {status.state === "failed" && (
              <Alert variant="destructive" className="md:col-span-2">
                <AlertTriangle />
                <AlertTitle>{stageLabel}</AlertTitle>
                <AlertDescription>
                  {status.message}
                  {status.error_code ? ` (${status.error_code})` : ""}
                </AlertDescription>
              </Alert>
            )}
          </CardContent>
          <CardFooter className="justify-end gap-2">
            <Button variant="outline" disabled={checking || active} onClick={() => void requestAction("check")}>
              <RefreshCw data-icon="inline-start" className={checking ? "animate-spin" : undefined} />
              {checking ? messages.checking : messages.check}
            </Button>
            <Button
              disabled={!status.update_available || active || polling}
              onClick={() => void requestAction("start")}
            >
              <Download data-icon="inline-start" />
              {messages.install}
            </Button>
          </CardFooter>
        </Card>
      )}

      <GeoDataUpdatePanel locale={locale} />

      <AlertDialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!active && !polling) {
            setDialogOpen(open);
          }
        }}
      >
        <AlertDialogContent
          onEscapeKeyDown={(event) => {
            if (active || polling) {
              event.preventDefault();
            }
          }}
        >
          <AlertDialogHeader>
            <AlertDialogMedia>
              {status?.state === "succeeded" ? (
                <CheckCircle2 />
              ) : (
                <RefreshCw className={active || polling ? "animate-spin" : undefined} />
              )}
            </AlertDialogMedia>
            <AlertDialogTitle>{messages.modalTitle}</AlertDialogTitle>
            <AlertDialogDescription>{messages.modalDescription}</AlertDialogDescription>
          </AlertDialogHeader>
          <div className="space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span>{stageLabel || messages.checking}</span>
              <span className="tabular-nums">{status?.progress || 0}%</span>
            </div>
            <Progress value={status?.progress || 0} aria-label={stageLabel || messages.checking} />
            {status?.state === "failed" && (
              <p className="text-destructive text-sm">
                {status.message}
                {status.error_code ? ` (${status.error_code})` : ""}
              </p>
            )}
          </div>
          {!active && !polling && (
            <AlertDialogFooter>
              <AlertDialogAction>{messages.close}</AlertDialogAction>
            </AlertDialogFooter>
          )}
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
