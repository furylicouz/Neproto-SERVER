"use client";

import * as React from "react";

import { Globe2, LockKeyhole, Network, ShieldCheck } from "lucide-react";
import { toast } from "sonner";

import { AdminError, AdminLoading, PageHeader, StateBadge } from "@/components/admin/admin-ui";
import { JobDialog } from "@/components/admin/job-dialog";
import { Badge } from "@/components/ui/badge";
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
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useAdminResource } from "@/hooks/use-admin-resource";
import { adminFetch, type ControlJob, type SettingsState, waitForAdminJob } from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";

export function ServerSettings({ locale }: { locale: AppLocale }) {
  const ru = locale === "ru";
  const settings = useAdminResource<SettingsState>("settings", 15_000);
  const [domainOpen, setDomainOpen] = React.useState(false);
  const [domain, setDomain] = React.useState("");
  const [policy, setPolicy] = React.useState<"production" | "compatibility" | null>(null);
  const [job, setJob] = React.useState<ControlJob | null>(null);
  const [busy, setBusy] = React.useState(false);

  async function changeDomain() {
    setBusy(true);
    try {
      const started = await adminFetch<ControlJob>("settings/domain", {
        method: "POST",
        json: { domain, confirm: "CHANGE DOMAIN" },
      });
      setDomainOpen(false);
      setJob(started);
      await waitForAdminJob(started.id, setJob);
      await settings.refresh();
      toast.success(ru ? "Домен изменён" : "Domain changed");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function changePolicy() {
    if (!policy) return;
    setBusy(true);
    try {
      await adminFetch("settings/policy", { method: "POST", json: { mode: policy, confirm: policy.toUpperCase() } });
      setPolicy(null);
      await settings.refresh();
      toast.success(ru ? "Политика NP/2 обновлена" : "NP/2 policy updated");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  if (settings.loading && !settings.data) return <AdminLoading />;
  if (settings.error || !settings.data) return <AdminError message={settings.error || "Settings unavailable"} />;
  const data = settings.data;
  let policyDescription = "";
  if (policy === "production") {
    policyDescription = ru
      ? "Будут включены обязательные продуктовые расширения NP/2."
      : "Required NP/2 production extensions will be enabled.";
  } else if (policy === "compatibility") {
    policyDescription = ru
      ? "Совместимость снижает требования к старым клиентам."
      : "Compatibility lowers requirements for older clients.";
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={ru ? "Настройки сервера" : "Server settings"}
        description={
          ru
            ? "Публичная идентичность, транспортная политика и параметры установки"
            : "Public identity, transport policy and installation parameters"
        }
      />
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <div className="rounded-lg border p-2">
                <Globe2 className="size-5" />
              </div>
              <Badge variant="outline">{data.deployment}</Badge>
            </div>
            <CardTitle className="pt-3">{ru ? "Публичный VPN-домен" : "Public VPN domain"}</CardTitle>
            <CardDescription>
              {ru ? "Домен участвует в TLS и идентичности NP/2" : "The domain is part of TLS and the NP/2 identity"}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="rounded-xl border p-4">
              <div className="font-mono text-sm">{data.domain}</div>
              <div className="mt-2 text-muted-foreground text-xs">{data.server_addresses.join(", ") || "—"}</div>
            </div>
            <Button
              variant="outline"
              onClick={() => {
                setDomain(data.domain);
                setDomainOpen(true);
              }}
            >
              {ru ? "Сменить домен" : "Change domain"}
            </Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <div className="w-fit rounded-lg border p-2">
              <Network className="size-5" />
            </div>
            <CardTitle className="pt-3">NeProto Web</CardTitle>
            <CardDescription>
              {ru ? "Адрес текущей панели администратора" : "Current administrator dashboard address"}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center justify-between rounded-xl border p-4">
              <span>{ru ? "Состояние" : "State"}</span>
              <StateBadge state={data.web_enabled ? "active" : "disabled"} />
            </div>
            <div className="rounded-xl border p-4 font-mono text-sm">
              {data.web_domain || `${data.server_addresses[0] || "127.0.0.1"}:${data.web_port || 3000}`}
            </div>
          </CardContent>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="size-5" />
            {ru ? "Политика протокола" : "Protocol policy"}
          </CardTitle>
          <CardDescription>
            {ru
              ? "Те же защищённые профили, которые доступны в консоли np"
              : "The same guarded profiles exposed by the np console"}
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-2">
          <button
            type="button"
            className="rounded-xl border p-5 text-left transition-colors hover:bg-muted/40"
            onClick={() => setPolicy("production")}
          >
            <div className="flex items-center justify-between">
              <div className="font-medium">{ru ? "Продуктовая" : "Production"}</div>
              <LockKeyhole className="size-5" />
            </div>
            <p className="mt-2 text-muted-foreground text-sm">
              {ru
                ? "Constellation и обязательная прямая секретность включены."
                : "Constellation and forward secrecy are required."}
            </p>
          </button>
          <button
            type="button"
            className="rounded-xl border p-5 text-left transition-colors hover:bg-muted/40"
            onClick={() => setPolicy("compatibility")}
          >
            <div className="flex items-center justify-between">
              <div className="font-medium">{ru ? "Совместимость" : "Compatibility"}</div>
              <Network className="size-5" />
            </div>
            <p className="mt-2 text-muted-foreground text-sm">
              {ru ? "Переходный режим для старых клиентов." : "Transitional mode for older clients."}
            </p>
          </button>
        </CardContent>
      </Card>
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Constellation</CardTitle>
          </CardHeader>
          <CardContent>
            <StateBadge state={data.enable_constellation ? "enabled" : "disabled"} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>{ru ? "Прямая секретность" : "Forward secrecy"}</CardTitle>
          </CardHeader>
          <CardContent>
            <StateBadge state={data.enable_forward_secrecy ? "enabled" : "disabled"} />
          </CardContent>
        </Card>
      </div>

      <Dialog open={domainOpen} onOpenChange={setDomainOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Смена публичного домена" : "Change public domain"}</DialogTitle>
            <DialogDescription>
              {ru
                ? "Будет создана резервная копия, обновлены TLS и учётные данные, затем выполнена проверка готовности."
                : "A backup is created before TLS and credentials are updated and readiness is verified."}
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>{ru ? "Новый домен" : "New domain"}</FieldLabel>
            <Input value={domain} onChange={(event) => setDomain(event.target.value.trim())} />
          </Field>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDomainOpen(false)}>
              {ru ? "Отмена" : "Cancel"}
            </Button>
            <Button disabled={busy || !domain || domain === data.domain} onClick={() => void changeDomain()}>
              {ru ? "Изменить домен" : "Change domain"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={policy !== null} onOpenChange={(open) => !open && setPolicy(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Подтвердите изменение политики" : "Confirm policy change"}</DialogTitle>
            <DialogDescription>{policyDescription}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPolicy(null)}>
              {ru ? "Отмена" : "Cancel"}
            </Button>
            <Button disabled={busy} onClick={() => void changePolicy()}>
              {ru ? "Применить" : "Apply"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <JobDialog
        job={job}
        title={ru ? "Смена домена" : "Domain change"}
        onOpenChange={(open) => !open && setJob(null)}
      />
    </div>
  );
}
