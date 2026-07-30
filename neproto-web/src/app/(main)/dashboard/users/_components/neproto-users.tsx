"use client";

import * as React from "react";

import Image from "next/image";

import { Copy, KeyRound, MoreHorizontal, Plus, QrCode, RefreshCw, ShieldOff, Trash2 } from "lucide-react";
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
  const users = useAdminResource<{ users: NP2User[] }>("users");
  const cluster = useAdminResource<ClusterState>("cluster");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [profile, setProfile] = React.useState("web");
  const [busy, setBusy] = React.useState(false);
  const [credential, setCredential] = React.useState<CredentialView | null>(null);
  const [deleteUser, setDeleteUser] = React.useState<NP2User | null>(null);

  async function refresh() {
    await Promise.all([users.refresh(), cluster.refresh()]);
  }

  async function createUser() {
    setBusy(true);
    try {
      const result = await adminFetch<{ user: NP2User; uri: string }>("users", {
        method: "POST",
        json: { name, profile },
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

  if (users.loading && !users.data) return <AdminLoading />;
  if (users.error || !users.data) return <AdminError message={users.error || "Users unavailable"} />;
  const accessUsers = new Set((cluster.data?.access || []).map((entry) => entry.user_id));

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={ru ? "Пользователи" : "Users"}
        description={
          ru ? "Учётные данные NP/2, профили и доступ к кластеру." : "NP/2 credentials, profiles, and cluster access."
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
                <TableHead>{ru ? "Профиль" : "Profile"}</TableHead>
                <TableHead>{ru ? "Статус" : "Status"}</TableHead>
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
                  <TableCell>{user.profile}</TableCell>
                  <TableCell>
                    <StateBadge state={user.status} />
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
                ? "Профиль определяет поведение маскировки клиента."
                : "The profile controls client carrier behavior."}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{ru ? "Имя устройства или пользователя" : "Device or user name"}</FieldLabel>
              <Input value={name} maxLength={96} onChange={(event) => setName(event.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{ru ? "Профиль" : "Profile"}</FieldLabel>
              <Select value={profile} onValueChange={setProfile}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="web">Web</SelectItem>
                  <SelectItem value="interactive">Interactive</SelectItem>
                  <SelectItem value="quiet">Quiet</SelectItem>
                </SelectContent>
              </Select>
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
    </div>
  );
}
