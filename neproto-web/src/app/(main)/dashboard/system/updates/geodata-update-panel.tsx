"use client";

import * as React from "react";

import { Database, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { AdminError, AdminLoading, StateBadge } from "@/components/admin/admin-ui";
import { JobDialog } from "@/components/admin/job-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldLabel } from "@/components/ui/field";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useAdminResource } from "@/hooks/use-admin-resource";
import { adminFetch, type ControlJob, type GeoDataState, waitForAdminJob } from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";

export function GeoDataUpdatePanel({ locale }: { readonly locale: AppLocale }) {
  const ru = locale === "ru";
  const geodata = useAdminResource<GeoDataState>("geodata", 12_000);
  const [job, setJob] = React.useState<ControlJob | null>(null);
  const [busy, setBusy] = React.useState(false);

  async function updateGeoData() {
    setBusy(true);
    try {
      const started = await adminFetch<ControlJob>("geodata/update", { method: "POST", json: { cluster: true } });
      setJob(started);
      await waitForAdminJob(started.id, setJob);
      await geodata.refresh();
      toast.success(ru ? "GeoIP и GeoSite обновлены на всех узлах" : "GeoIP and GeoSite updated across all nodes");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function setSchedule(value: string) {
    try {
      await adminFetch("geodata/schedule", { method: "POST", json: { preset: value } });
      await geodata.refresh();
      toast.success(ru ? "Расписание GeoData обновлено" : "GeoData schedule updated");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (geodata.loading && !geodata.data) return <AdminLoading />;
  if (geodata.error || !geodata.data) return <AdminError message={geodata.error || "GeoData unavailable"} />;

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Database className="size-5" />
            GeoIP / GeoSite
          </CardTitle>
          <CardDescription>
            {ru
              ? "Проверенные базы маршрутизации синхронизируются со всеми узлами кластера"
              : "Verified routing databases are synchronized across every cluster node"}
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-[1fr_220px_auto] md:items-end">
          <div>
            <div className="text-muted-foreground text-sm">{ru ? "Состояние баз" : "Database state"}</div>
            <div className="mt-2">
              <StateBadge state={geodata.data.status.state} />
            </div>
            {geodata.data.status.updated_at && (
              <div className="mt-2 text-muted-foreground text-xs">
                {new Date(geodata.data.status.updated_at).toLocaleString(locale)}
              </div>
            )}
          </div>
          <Field>
            <FieldLabel>{ru ? "Автообновление" : "Automatic update"}</FieldLabel>
            <Select value={geodata.data.schedule} onValueChange={(value) => void setSchedule(value)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="off">{ru ? "Выключено" : "Off"}</SelectItem>
                <SelectItem value="daily">{ru ? "Ежедневно" : "Daily"}</SelectItem>
                <SelectItem value="weekly">{ru ? "Еженедельно" : "Weekly"}</SelectItem>
                <SelectItem value="monthly">{ru ? "Ежемесячно" : "Monthly"}</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Button variant="outline" disabled={busy} onClick={() => void updateGeoData()}>
            <RefreshCw className={busy ? "animate-spin" : undefined} />
            {ru ? "Обновить весь кластер" : "Update entire cluster"}
          </Button>
        </CardContent>
      </Card>
      <JobDialog
        job={job}
        title={ru ? "Обновление GeoIP / GeoSite" : "GeoIP / GeoSite update"}
        onOpenChange={(open) => !open && setJob(null)}
      />
    </>
  );
}
