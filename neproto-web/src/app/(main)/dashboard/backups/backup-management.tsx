"use client";

import * as React from "react";

import { Archive, Clock3, Plus, RotateCcw } from "lucide-react";
import { toast } from "sonner";

import { AdminError, AdminLoading, PageHeader } from "@/components/admin/admin-ui";
import { JobDialog } from "@/components/admin/job-dialog";
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAdminResource } from "@/hooks/use-admin-resource";
import { adminFetch, type BackupEntry, type BackupState, type ControlJob, waitForAdminJob } from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";

export function BackupManagement({ locale }: { locale: AppLocale }) {
  const ru = locale === "ru";
  const backups = useAdminResource<BackupState>("backups", 15_000);
  const [restore, setRestore] = React.useState<BackupEntry | null>(null);
  const [job, setJob] = React.useState<ControlJob | null>(null);
  const [busy, setBusy] = React.useState(false);

  async function createBackup() {
    setBusy(true);
    try {
      await adminFetch("backups", { method: "POST", json: {} });
      await backups.refresh();
      toast.success(ru ? "Резервная копия создана" : "Backup created");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function restoreBackup() {
    if (!restore) return;
    setBusy(true);
    try {
      const started = await adminFetch<ControlJob>("backups/restore", {
        method: "POST",
        json: { id: restore.id, confirm: "RESTORE" },
      });
      setRestore(null);
      setJob(started);
      await waitForAdminJob(started.id, setJob);
      await backups.refresh();
      toast.success(ru ? "Система восстановлена" : "System restored");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  if (backups.loading && !backups.data) return <AdminLoading />;
  if (backups.error || !backups.data) return <AdminError message={backups.error || "Backups unavailable"} />;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={ru ? "Резервные копии" : "Backups"}
        description={
          ru
            ? "Снимки конфигурации, пользователей, маршрутов и кластера"
            : "Snapshots of configuration, users, routes and cluster state"
        }
        actions={
          <Button disabled={busy} onClick={() => void createBackup()}>
            <Plus />
            {ru ? "Создать копию" : "Create backup"}
          </Button>
        }
      />
      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader>
            <div className="w-fit rounded-lg border p-2">
              <Archive className="size-5" />
            </div>
            <CardTitle className="pt-3">{backups.data.backups.length}</CardTitle>
            <CardDescription>{ru ? "Доступно снимков" : "Available snapshots"}</CardDescription>
          </CardHeader>
        </Card>
        <Card className="md:col-span-2">
          <CardHeader>
            <div className="w-fit rounded-lg border p-2">
              <Clock3 className="size-5" />
            </div>
            <CardTitle className="pt-3">{ru ? "Атомарное восстановление" : "Atomic recovery"}</CardTitle>
            <CardDescription>
              {ru
                ? "Перед применением снимок проверяется, при ошибке выполняется откат."
                : "The snapshot is validated first and a rollback is attempted on failure."}
            </CardDescription>
          </CardHeader>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>{ru ? "История восстановления" : "Recovery history"}</CardTitle>
          <CardDescription>
            {ru ? "Снимки хранятся только на сервере" : "Snapshots remain on the server"}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{ru ? "Снимок" : "Snapshot"}</TableHead>
                <TableHead className="w-48">{ru ? "Действие" : "Action"}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {backups.data.backups.map((backup) => (
                <TableRow key={backup.id}>
                  <TableCell>
                    <div className="font-medium">{backup.name}</div>
                    <div className="font-mono text-muted-foreground text-xs">{backup.id}</div>
                  </TableCell>
                  <TableCell>
                    <Button size="sm" variant="outline" onClick={() => setRestore(backup)}>
                      <RotateCcw />
                      {ru ? "Восстановить" : "Restore"}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {backups.data.backups.length === 0 && (
                <TableRow>
                  <TableCell colSpan={2} className="h-32 text-center text-muted-foreground">
                    {ru ? "Резервных копий пока нет" : "No backups yet"}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <Dialog open={restore !== null} onOpenChange={(open) => !open && setRestore(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Восстановить снимок?" : "Restore snapshot?"}</DialogTitle>
            <DialogDescription>
              {ru
                ? `Конфигурация будет заменена данными из ${restore?.name}. Службы перезапустятся автоматически.`
                : `Configuration will be replaced with ${restore?.name}. Services restart automatically.`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRestore(null)}>
              {ru ? "Отмена" : "Cancel"}
            </Button>
            <Button variant="destructive" disabled={busy} onClick={() => void restoreBackup()}>
              {ru ? "Восстановить" : "Restore"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <JobDialog job={job} title={ru ? "Восстановление" : "Recovery"} onOpenChange={(open) => !open && setJob(null)} />
    </div>
  );
}
