"use client";

import * as React from "react";

import Image from "next/image";

import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Copy,
  Gauge,
  KeyRound,
  MoreHorizontal,
  Plus,
  QrCode,
  RefreshCw,
  RotateCcw,
  ShieldOff,
  Smartphone,
  Trash2,
  UsersRound,
} from "lucide-react";
import { toast } from "sonner";

import { AdminError, AdminLoading, PageHeader, StateBadge } from "@/components/admin/admin-ui";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useAdminResource } from "@/hooks/use-admin-resource";
import { adminFetch, type ClusterState, type NP2User } from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";

interface CredentialView {
  name: string;
  uri: string;
  manual?: string;
  qr?: string;
}

export function NeProtoUsers({ locale }: { locale: AppLocale }) {
  const ru = locale === "ru";
  const users = useAdminResource<{ users: NP2User[] }>("users", 5_000);
  const cluster = useAdminResource<ClusterState>("cluster");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [credential, setCredential] = React.useState<CredentialView | null>(null);
  const [deleteUser, setDeleteUser] = React.useState<NP2User | null>(null);
  const [policyUser, setPolicyUser] = React.useState<NP2User | null>(null);
  const [deviceUser, setDeviceUser] = React.useState<NP2User | null>(null);
  const [resetUser, setResetUser] = React.useState<NP2User | null>(null);
  const [maxDevices, setMaxDevices] = React.useState("0");

  async function refresh() {
    await Promise.all([users.refresh(), cluster.refresh()]);
  }

  async function createUser() {
    setBusy(true);
    try {
      const result = await adminFetch<{ user: NP2User; uri: string }>("users", {
        method: "POST",
        json: { name },
      });
      const [manual, qr] = await Promise.all([
        adminFetch<{ value: string }>(`users/${encodeURIComponent(result.user.id)}/export?format=manual`),
        adminFetch<{ mime: string; base64: string }>(
          `users/${encodeURIComponent(result.user.id)}/export?format=qr`,
        ).catch(() => null),
      ]);
      setCreateOpen(false);
      setName("");
      setCredential({
        name: result.user.name,
        uri: result.uri,
        manual: manual.value,
        qr: qr ? `data:${qr.mime};base64,${qr.base64}` : undefined,
      });
      await refresh();
      toast.success(ru ? "Пользователь создан" : "User created");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function exportUser(user: NP2User) {
    setBusy(true);
    try {
      const [uri, manual, qr] = await Promise.all([
        adminFetch<{ value: string }>(`users/${encodeURIComponent(user.id)}/export?format=uri`),
        adminFetch<{ value: string }>(`users/${encodeURIComponent(user.id)}/export?format=manual`),
        adminFetch<{ mime: string; base64: string }>(`users/${encodeURIComponent(user.id)}/export?format=qr`).catch(
          () => null,
        ),
      ]);
      setCredential({
        name: user.name,
        uri: uri.value,
        manual: manual.value,
        qr: qr ? `data:${qr.mime};base64,${qr.base64}` : undefined,
      });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function userAction(user: NP2User, action: "rotate" | "revoke") {
    setBusy(true);
    try {
      await adminFetch(`users/${encodeURIComponent(user.id)}/${action}`, { method: "POST", json: {} });
      await refresh();
      let message: string;
      if (action === "rotate") message = ru ? "Ключ обновлён" : "Credential rotated";
      else message = ru ? "Доступ отозван" : "Access revoked";
      toast.success(message);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function removeUser() {
    if (!deleteUser) return;
    setBusy(true);
    try {
      await adminFetch(`users/${encodeURIComponent(deleteUser.id)}`, { method: "DELETE", json: { confirm: "DELETE" } });
      setDeleteUser(null);
      await refresh();
      toast.success(ru ? "Пользователь удалён" : "User deleted");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function setClusterAccess(user: NP2User, enabled: boolean) {
    try {
      await adminFetch(`users/${encodeURIComponent(user.id)}/cluster-access`, { method: "POST", json: { enabled } });
      await cluster.refresh();
      toast.success(ru ? "Доступ к кластеру обновлён" : "Cluster access updated");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function saveDeviceLimit() {
    if (!policyUser) return;
    setBusy(true);
    try {
      await adminFetch(`users/${encodeURIComponent(policyUser.id)}/policy`, {
        method: "PATCH",
        json: { max_devices: Number(maxDevices) },
      });
      setPolicyUser(null);
      await users.refresh();
      toast.success(ru ? "Лимит устройств обновлён" : "Device limit updated");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function resetTraffic() {
    if (!resetUser) return;
    setBusy(true);
    try {
      await adminFetch(`users/${encodeURIComponent(resetUser.id)}/traffic-reset`, { method: "POST", json: {} });
      setResetUser(null);
      await users.refresh();
      toast.success(ru ? "Счётчики трафика сброшены" : "Traffic counters reset");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function removeDevice(user: NP2User, deviceID: string) {
    setBusy(true);
    try {
      await adminFetch(`users/${encodeURIComponent(user.id)}/devices/${encodeURIComponent(deviceID)}`, {
        method: "DELETE",
        json: {},
      });
      const refreshed = await users.refresh();
      setDeviceUser(refreshed.users.find((entry) => entry.id === user.id) ?? null);
      toast.success(ru ? "Устройство удалено" : "Device removed");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  if (users.loading && !users.data) return <AdminLoading />;
  if (users.error || !users.data) return <AdminError message={users.error || "Users unavailable"} />;
  const accessUsers = new Set((cluster.data?.access || []).map((entry) => entry.user_id));
  const totalUpload = users.data.users.reduce((total, user) => total + user.upload_bytes, 0);
  const totalDownload = users.data.users.reduce((total, user) => total + user.download_bytes, 0);
  const onlineUsers = users.data.users.filter((user) => user.online).length;
  const onlineDevices = users.data.users.reduce((total, user) => total + user.online_devices, 0);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={ru ? "Пользователи" : "Users"}
        description={
          ru
            ? "Учётные данные NP/2; статус, устройства и трафик текущего узла."
            : "NP/2 credentials; current-node status, devices, and traffic."
        }
        actions={
          <>
            <Button variant="outline" onClick={() => void refresh()} disabled={busy}>
              <RefreshCw />
              {ru ? "Обновить" : "Refresh"}
            </Button>
            <Button onClick={() => setCreateOpen(true)}>
              <Plus />
              {ru ? "Создать" : "Create user"}
            </Button>
          </>
        }
      />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          icon={UsersRound}
          label={ru ? "Пользователи онлайн" : "Users online"}
          value={`${onlineUsers} / ${users.data.users.filter((user) => user.status === "active").length}`}
        />
        <MetricCard
          icon={Smartphone}
          label={ru ? "Устройства онлайн" : "Devices online"}
          value={String(onlineDevices)}
        />
        <MetricCard
          icon={ArrowUpFromLine}
          label={ru ? "Отправлено" : "Uploaded"}
          value={formatBytes(totalUpload, ru)}
        />
        <MetricCard
          icon={ArrowDownToLine}
          label={ru ? "Получено" : "Downloaded"}
          value={formatBytes(totalDownload, ru)}
        />
      </div>
      <Card>
        <CardHeader>
          <CardTitle>{ru ? "Доступ NP/2" : "NP/2 access"}</CardTitle>
          <CardDescription>
            {users.data.users.length} {ru ? "учётных записей" : "credentials"}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{ru ? "Имя" : "Name"}</TableHead>
                <TableHead>{ru ? "Режим" : "Mode"}</TableHead>
                <TableHead>{ru ? "Статус" : "Status"}</TableHead>
                <TableHead>{ru ? "Трафик" : "Traffic"}</TableHead>
                <TableHead>{ru ? "Устройства" : "Devices"}</TableHead>
                <TableHead>{ru ? "Кластер" : "Cluster"}</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.data.users.map((user) => (
                <TableRow key={user.id}>
                  <TableCell>
                    <div className="font-medium">{user.name}</div>
                    <div className="font-mono text-muted-foreground text-xs">{user.id}</div>
                  </TableCell>
                  <TableCell>{ru ? "Автоматически" : "Automatic"}</TableCell>
                  <TableCell>
                    <div className="flex flex-col items-start gap-1">
                      <StateBadge state={connectionState(user)} label={connectionStateLabel(user, ru)} />
                      {user.last_seen && (
                        <span className="text-muted-foreground text-xs">
                          {ru ? "Активность" : "Last seen"}: {formatDate(user.last_seen, locale)}
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="space-y-1 text-xs">
                      <div className="flex items-center gap-1.5">
                        <ArrowDownToLine className="size-3.5 text-emerald-500" />
                        {formatBytes(user.download_bytes, ru)}
                      </div>
                      <div className="flex items-center gap-1.5">
                        <ArrowUpFromLine className="size-3.5 text-sky-500" />
                        {formatBytes(user.upload_bytes, ru)}
                      </div>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Button
                      className="h-auto justify-start px-2 py-1"
                      variant="ghost"
                      onClick={() => setDeviceUser(user)}
                    >
                      <Smartphone />
                      <span>
                        {user.online_devices} / {user.enrolled_devices}
                        {user.max_devices > 0 ? ` · ${ru ? "лимит" : "limit"} ${user.max_devices}` : ""}
                      </span>
                    </Button>
                  </TableCell>
                  <TableCell>
                    <Switch
                      disabled={user.status !== "active"}
                      checked={accessUsers.has(user.id)}
                      onCheckedChange={(value) => void setClusterAccess(user, value)}
                    />
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button size="icon" variant="ghost">
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          disabled={user.status !== "active" || busy}
                          onClick={() => void exportUser(user)}
                        >
                          <QrCode />
                          {ru ? "Экспорт и QR" : "Export and QR"}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          disabled={user.status !== "active" || busy}
                          onClick={() => void userAction(user, "rotate")}
                        >
                          <KeyRound />
                          {ru ? "Сменить ключ" : "Rotate credential"}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          disabled={user.status !== "active" || busy}
                          onClick={() => void userAction(user, "revoke")}
                        >
                          <ShieldOff />
                          {ru ? "Отозвать" : "Revoke"}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          disabled={busy}
                          onClick={() => {
                            setMaxDevices(String(user.max_devices));
                            setPolicyUser(user);
                          }}
                        >
                          <Gauge />
                          {ru ? "Лимит устройств" : "Device limit"}
                        </DropdownMenuItem>
                        <DropdownMenuItem disabled={busy} onClick={() => setResetUser(user)}>
                          <RotateCcw />
                          {ru ? "Сбросить трафик" : "Reset traffic"}
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          variant="destructive"
                          disabled={user.status !== "revoked" || busy}
                          onClick={() => setDeleteUser(user)}
                        >
                          <Trash2 />
                          {ru ? "Удалить" : "Delete"}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Новый пользователь NP/2" : "New NP/2 user"}</DialogTitle>
            <DialogDescription>
              {ru
                ? "NP/2 автоматически адаптирует транспорт и маскировку под текущий трафик и сеть."
                : "NP/2 automatically adapts transport and cover behavior to current traffic and network conditions."}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{ru ? "Имя устройства или пользователя" : "Device or user name"}</FieldLabel>
              <Input value={name} maxLength={96} onChange={(event) => setName(event.target.value)} />
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {ru ? "Отмена" : "Cancel"}
            </Button>
            <Button disabled={busy || name.trim().length === 0} onClick={() => void createUser()}>
              <Plus />
              {ru ? "Создать" : "Create"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={credential !== null} onOpenChange={(open) => !open && setCredential(null)}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{credential?.name}</DialogTitle>
            <DialogDescription>
              {ru
                ? "Сохраните настройки сейчас. Секрет повторно показывается только через явный экспорт."
                : "Save these settings now. Secrets are shown again only through explicit export."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 md:grid-cols-[220px_1fr]">
            <div className="flex min-h-52 items-center justify-center rounded-xl border bg-white p-3">
              {credential?.qr ? (
                <Image
                  alt="NP/2 QR"
                  className="size-full object-contain"
                  height={512}
                  src={credential.qr}
                  unoptimized
                  width={512}
                />
              ) : (
                <QrCode className="size-20 text-muted-foreground" />
              )}
            </div>
            <div className="space-y-3">
              <Textarea
                className="min-h-28 font-mono text-xs"
                readOnly
                value={credential?.manual || credential?.uri || ""}
              />
              <Button
                variant="outline"
                onClick={() => {
                  void navigator.clipboard.writeText(credential?.uri || "");
                  toast.success(ru ? "Скопировано" : "Copied");
                }}
              >
                <Copy />
                {ru ? "Копировать URI" : "Copy URI"}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={policyUser !== null} onOpenChange={(open) => !open && setPolicyUser(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Лимит устройств" : "Device limit"}</DialogTitle>
            <DialogDescription>
              {ru
                ? "Подключённые устройства не отключаются. При заполненном лимите сервер отклоняет только новое устройство."
                : "Connected devices stay online. When the limit is full, only a new device is rejected."}
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>{policyUser?.name}</FieldLabel>
            <Select value={maxDevices} onValueChange={setMaxDevices}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">{ru ? "Без ограничений" : "Unlimited"}</SelectItem>
                {[1, 2, 3, 4, 5, 8, 10, 16].map((value) => (
                  <SelectItem key={value} value={String(value)}>
                    {value}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPolicyUser(null)}>
              {ru ? "Отмена" : "Cancel"}
            </Button>
            <Button disabled={busy} onClick={() => void saveDeviceLimit()}>
              {ru ? "Сохранить" : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={deviceUser !== null} onOpenChange={(open) => !open && setDeviceUser(null)}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{ru ? "Устройства" : "Devices"}</DialogTitle>
            <DialogDescription>{deviceUser?.name}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            {deviceUser?.devices.length ? (
              deviceUser.devices.map((device, index) => (
                <div className="flex items-center justify-between gap-3 rounded-xl border p-3" key={device.device_id}>
                  <div className="min-w-0">
                    <div className="flex items-center gap-2 font-medium">
                      <Smartphone className="size-4" />
                      {ru ? `Устройство ${index + 1}` : `Device ${index + 1}`}
                      <StateBadge
                        state={device.online ? "online" : "offline"}
                        label={deviceStateLabel(device.online, ru)}
                      />
                    </div>
                    <div className="mt-1 truncate font-mono text-muted-foreground text-xs">{device.device_id}</div>
                    <div className="text-muted-foreground text-xs">
                      {ru ? "Добавлено" : "Enrolled"}: {formatDate(device.first_seen, locale)}
                    </div>
                  </div>
                  <Button
                    aria-label={ru ? "Удалить устройство" : "Remove device"}
                    disabled={busy || device.online}
                    onClick={() => void removeDevice(deviceUser, device.device_id)}
                    size="icon"
                    variant="outline"
                  >
                    <Trash2 />
                  </Button>
                </div>
              ))
            ) : (
              <div className="rounded-xl border border-dashed p-6 text-center text-muted-foreground text-sm">
                {ru ? "Нет зарегистрированных устройств" : "No enrolled devices"}
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleteUser !== null} onOpenChange={(open) => !open && setDeleteUser(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {ru ? "Удалить пользователя безвозвратно?" : "Permanently delete user?"}
            </AlertDialogTitle>
            <AlertDialogDescription>{deleteUser?.name}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{ru ? "Отмена" : "Cancel"}</AlertDialogCancel>
            <AlertDialogAction disabled={busy} onClick={() => void removeUser()}>
              {ru ? "Удалить" : "Delete"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={resetUser !== null} onOpenChange={(open) => !open && setResetUser(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{ru ? "Сбросить статистику трафика?" : "Reset traffic statistics?"}</AlertDialogTitle>
            <AlertDialogDescription>
              {resetUser?.name} · {formatBytes(resetUser?.total_bytes || 0, ru)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{ru ? "Отмена" : "Cancel"}</AlertDialogCancel>
            <AlertDialogAction disabled={busy} onClick={() => void resetTraffic()}>
              {ru ? "Сбросить" : "Reset"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function MetricCard({ icon: Icon, label, value }: { icon: React.ElementType; label: string; value: string }) {
  return (
    <Card>
      <CardContent className="flex items-center gap-4 p-5">
        <div className="rounded-xl bg-muted p-2.5">
          <Icon className="size-5" />
        </div>
        <div>
          <div className="text-muted-foreground text-sm">{label}</div>
          <div className="font-semibold text-xl">{value}</div>
        </div>
      </CardContent>
    </Card>
  );
}

function formatBytes(value: number, ru: boolean) {
  if (!Number.isFinite(value) || value <= 0) return "0 Б";
  const units = ru ? ["Б", "КБ", "МБ", "ГБ", "ТБ"] : ["B", "KB", "MB", "GB", "TB"];
  const unit = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** unit;
  return `${new Intl.NumberFormat(ru ? "ru-RU" : "en-US", { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(amount)} ${units[unit]}`;
}

function formatDate(value: string, locale: AppLocale) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "—"
    : new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
        dateStyle: "short",
        timeStyle: "short",
      }).format(date);
}

function connectionState(user: NP2User) {
  if (user.status === "revoked") return "revoked";
  return user.online ? "online" : "offline";
}

function connectionStateLabel(user: NP2User, ru: boolean) {
  if (!ru) return connectionState(user);
  if (user.status === "revoked") return "Отозван";
  return user.online ? "Онлайн" : "Офлайн";
}

function deviceStateLabel(online: boolean, ru: boolean) {
  if (online) return ru ? "Онлайн" : "Online";
  return ru ? "Офлайн" : "Offline";
}
