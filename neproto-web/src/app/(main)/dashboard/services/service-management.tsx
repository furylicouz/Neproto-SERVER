"use client";

import * as React from "react";

import { Activity, CircleStop, Play, RefreshCw, ScrollText, Stethoscope } from "lucide-react";
import { toast } from "sonner";

import { AdminError, AdminLoading, PageHeader, StateBadge } from "@/components/admin/admin-ui";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useAdminResource } from "@/hooks/use-admin-resource";
import { adminFetch, type LogState, type ServicesState } from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";

type ServiceAction = "start" | "stop" | "restart" | "validate";

export function ServiceManagement({ locale }: { locale: AppLocale }) {
  const ru = locale === "ru";
  const services = useAdminResource<ServicesState>("services", 5_000);
  const logs = useAdminResource<LogState>("logs", 8_000);
  const [busy, setBusy] = React.useState<ServiceAction | "doctor" | null>(null);
  const [stopOpen, setStopOpen] = React.useState(false);
  const [doctorLines, setDoctorLines] = React.useState<string[] | null>(null);

  async function runAction(action: ServiceAction) {
    setBusy(action);
    try {
      await adminFetch(`services/${action}`, { method: "POST", json: action === "stop" ? { confirm: "STOP" } : {} });
      await Promise.all([services.refresh(), logs.refresh()]);
      setStopOpen(false);
      toast.success(ru ? "Операция выполнена" : "Operation completed");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(null);
    }
  }

  async function runDoctor() {
    setBusy("doctor");
    try {
      const result = await adminFetch<{ ok: boolean; lines: string[] }>("doctor", { method: "POST", json: {} });
      setDoctorLines(result.lines);
      let message: string;
      if (result.ok) message = ru ? "Диагностика пройдена" : "Diagnostics passed";
      else message = ru ? "Найдены проблемы" : "Issues detected";
      toast[result.ok ? "success" : "warning"](message);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Diagnostics failed");
    } finally {
      setBusy(null);
    }
  }

  if (services.loading && !services.data) return <AdminLoading />;
  if (services.error || !services.data) return <AdminError message={services.error || "Services unavailable"} />;

  const entries = [
    {
      key: "np2",
      name: "NP/2",
      description: ru ? "Основной транспорт и туннель" : "Core transport and tunnel",
      state: services.data.services.np2,
    },
    {
      key: "ingress",
      name: "Caddy",
      description: ru ? "TLS и публичный вход" : "TLS and public ingress",
      state: services.data.services.ingress,
    },
    {
      key: "web",
      name: "NeProto Web",
      description: ru ? "Панель администратора" : "Administrator dashboard",
      state: services.data.services.web,
    },
  ];
  let logContent = ru ? "Событий пока нет" : "No events yet";
  if (logs.data?.lines.length) logContent = logs.data.lines.join("\n");

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={ru ? "Службы и журналы" : "Services and logs"}
        description={
          ru
            ? "Управление всем стеком NeProto и встроенная диагностика"
            : "Control the complete NeProto stack and run diagnostics"
        }
        actions={
          <>
            <Button variant="outline" disabled={busy !== null} onClick={() => void runDoctor()}>
              <Stethoscope />
              {ru ? "Диагностика" : "Run doctor"}
            </Button>
            <Button disabled={busy !== null} onClick={() => void runAction("restart")}>
              <RefreshCw className={busy === "restart" ? "animate-spin" : ""} />
              {ru ? "Перезапустить" : "Restart all"}
            </Button>
          </>
        }
      />

      <div className="grid gap-4 md:grid-cols-3">
        {entries.map((entry) => (
          <Card key={entry.key}>
            <CardHeader>
              <div className="flex items-center justify-between">
                <div className="rounded-lg border p-2">
                  <Activity className="size-5" />
                </div>
                <StateBadge state={entry.state} />
              </div>
              <CardTitle className="pt-3">{entry.name}</CardTitle>
              <CardDescription>{entry.description}</CardDescription>
            </CardHeader>
          </Card>
        ))}
      </div>

      <Card>
        <CardHeader className="flex-row items-center justify-between gap-4">
          <div>
            <CardTitle>{ru ? "Управление стеком" : "Stack controls"}</CardTitle>
            <CardDescription>
              {ru
                ? "Действия применяются к NP/2, ingress и веб-панели"
                : "Actions apply to NP/2, ingress and the web dashboard"}
            </CardDescription>
          </div>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-3">
          <Button variant="outline" disabled={busy !== null} onClick={() => void runAction("start")}>
            <Play />
            {ru ? "Запустить" : "Start"}
          </Button>
          <Button variant="outline" disabled={busy !== null} onClick={() => void runAction("validate")}>
            <Stethoscope />
            {ru ? "Проверить конфигурацию" : "Validate configuration"}
          </Button>
          <Button variant="destructive" disabled={busy !== null} onClick={() => setStopOpen(true)}>
            <CircleStop />
            {ru ? "Остановить все" : "Stop all"}
          </Button>
        </CardContent>
      </Card>

      <Card className="min-h-[420px]">
        <CardHeader className="flex-row items-center justify-between gap-4">
          <div>
            <CardTitle className="flex items-center gap-2">
              <ScrollText className="size-5" />
              {ru ? "События служб" : "Service events"}
            </CardTitle>
            <CardDescription>
              {ru ? "Последние безопасно ограниченные строки журнала" : "Latest bounded and sanitized journal lines"}
            </CardDescription>
          </div>
          <Button size="sm" variant="outline" onClick={() => void logs.refresh()}>
            <RefreshCw />
            {ru ? "Обновить" : "Refresh"}
          </Button>
        </CardHeader>
        <CardContent>
          <pre className="max-h-[520px] overflow-auto whitespace-pre-wrap rounded-xl border bg-muted/35 p-4 font-mono text-xs leading-5">
            {logContent}
          </pre>
        </CardContent>
      </Card>

      <Dialog open={stopOpen} onOpenChange={setStopOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Остановить все службы?" : "Stop all services?"}</DialogTitle>
            <DialogDescription>
              {ru
                ? "VPN и веб-панель станут недоступны до ручного запуска через SSH."
                : "VPN and this dashboard will remain unavailable until manually started over SSH."}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setStopOpen(false)}>
              {ru ? "Отмена" : "Cancel"}
            </Button>
            <Button variant="destructive" disabled={busy !== null} onClick={() => void runAction("stop")}>
              {ru ? "Остановить" : "Stop"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={doctorLines !== null} onOpenChange={(open) => !open && setDoctorLines(null)}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{ru ? "Результат диагностики" : "Diagnostics result"}</DialogTitle>
          </DialogHeader>
          <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap rounded-xl border bg-muted/35 p-4 font-mono text-xs leading-5">
            {doctorLines?.join("\n")}
          </pre>
          <DialogFooter>
            <Button onClick={() => setDoctorLines(null)}>{ru ? "Закрыть" : "Close"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
