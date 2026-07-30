"use client";

import * as React from "react";

import { Eye, EyeOff, MoreHorizontal, Plus, RefreshCw, Server, Trash2, Users } from "lucide-react";
import { toast } from "sonner";

import { AdminError, AdminLoading, PageHeader, StateBadge } from "@/components/admin/admin-ui";
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
import { useAdminResource } from "@/hooks/use-admin-resource";
import {
  adminFetch,
  type ClusterNode,
  type ClusterState,
  type ControlJob,
  type NP2User,
  waitForAdminJob,
} from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";

interface EnrolmentForm {
  host: string;
  port: string;
  user: string;
  password: string;
  node_id: string;
  name: string;
  region: string;
  domain: string;
  addresses: string;
}

const emptyForm: EnrolmentForm = {
  host: "",
  port: "22",
  user: "root",
  password: "",
  node_id: "",
  name: "",
  region: "",
  domain: "",
  addresses: "",
};

function nodeTrafficLabel(enabled: boolean, ru: boolean) {
  if (enabled) return ru ? "Вывести из трафика" : "Drain";
  return ru ? "Включить" : "Enable";
}

function nodeVisibilityLabel(visible: boolean, ru: boolean) {
  if (visible) return ru ? "Скрыть" : "Hide";
  return ru ? "Опубликовать" : "Publish";
}

export function ClusterManagement({ locale }: { locale: AppLocale }) {
  const ru = locale === "ru";
  const cluster = useAdminResource<ClusterState>("cluster", 10_000);
  const users = useAdminResource<{ users: NP2User[] }>("users");
  const [form, setForm] = React.useState<EnrolmentForm>(emptyForm);
  const [enrolOpen, setEnrolOpen] = React.useState(false);
  const [fingerprint, setFingerprint] = React.useState("");
  const [job, setJob] = React.useState<ControlJob | null>(null);
  const [assignNode, setAssignNode] = React.useState<ClusterNode | null>(null);
  const [selectedUser, setSelectedUser] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const setField = (field: keyof EnrolmentForm, value: string) =>
    setForm((current) => ({ ...current, [field]: value }));

  async function verifyHost() {
    setBusy(true);
    try {
      const started = await adminFetch<ControlJob>("cluster/host-key", {
        method: "POST",
        json: { host: form.host, port: Number(form.port), user: form.user, password: form.password },
      });
      setJob(started);
      const complete = await waitForAdminJob<{ fingerprint: string }>(started.id, setJob);
      setFingerprint(complete.result?.fingerprint || "");
      toast.success(ru ? "SSH-ключ сервера получен" : "SSH host key received");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function enrol() {
    setBusy(true);
    try {
      const started = await adminFetch<ControlJob>("cluster/enrol", {
        method: "POST",
        json: {
          ...form,
          port: Number(form.port),
          fingerprint,
          addresses: form.addresses
            .split(",")
            .map((value) => value.trim())
            .filter(Boolean),
        },
      });
      setEnrolOpen(false);
      setJob(started);
      await waitForAdminJob(started.id, setJob);
      setForm(emptyForm);
      setFingerprint("");
      await cluster.refresh();
      toast.success(ru ? "Узел добавлен в кластер" : "Cluster node enrolled");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function setNodeFlag(node: ClusterNode, action: "enable" | "publish", enabled: boolean) {
    try {
      await adminFetch(`cluster/nodes/${encodeURIComponent(node.id)}/${action}`, { method: "POST", json: { enabled } });
      await cluster.refresh();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function assignUser() {
    if (!assignNode || !selectedUser) return;
    const access = cluster.data?.access.find((entry) => entry.user_id === selectedUser);
    const enabled = !access?.allowed_node_ids.includes(assignNode.id);
    try {
      await adminFetch(`cluster/nodes/${encodeURIComponent(assignNode.id)}/assign-user`, {
        method: "POST",
        json: { user_id: selectedUser, enabled },
      });
      setAssignNode(null);
      await cluster.refresh();
      toast.success(ru ? "Назначение обновлено" : "Assignment updated");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function syncUsers() {
    setBusy(true);
    try {
      await adminFetch("cluster/sync-users", { method: "POST", json: {} });
      toast.success(ru ? "Учётные данные синхронизированы" : "Credentials synchronized");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function removeNode(node: ClusterNode) {
    if (
      !window.confirm(ru ? `Удалить узел «${node.name}» из кластера?` : `Remove node “${node.name}” from the cluster?`)
    )
      return;
    setBusy(true);
    try {
      await adminFetch(`cluster/nodes/${encodeURIComponent(node.id)}`, {
        method: "DELETE",
        json: { confirm: "REMOVE" },
      });
      await cluster.refresh();
      toast.success(ru ? "Узел удалён" : "Node removed");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  if (cluster.loading && !cluster.data) return <AdminLoading />;
  if (cluster.error || !cluster.data) return <AdminError message={cluster.error || "Cluster unavailable"} />;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={ru ? "Кластер" : "Cluster"}
        description={`${cluster.data.cluster_id} · ${ru ? "ревизия" : "revision"} ${cluster.data.revision}`}
        actions={
          <>
            <Button variant="outline" disabled={busy} onClick={() => void syncUsers()}>
              <Users />
              {ru ? "Синхронизировать" : "Sync users"}
            </Button>
            <Button onClick={() => setEnrolOpen(true)}>
              <Plus />
              {ru ? "Добавить узел" : "Enrol node"}
            </Button>
          </>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>{ru ? "Инфраструктура NP/2" : "NP/2 infrastructure"}</CardTitle>
          <CardDescription>
            {cluster.data.nodes.length} {ru ? "узлов" : "nodes"}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{ru ? "Сервер" : "Server"}</TableHead>
                <TableHead>{ru ? "Роли" : "Roles"}</TableHead>
                <TableHead>{ru ? "Состояние" : "State"}</TableHead>
                <TableHead>Ping</TableHead>
                <TableHead>{ru ? "Клиенты" : "Clients"}</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {cluster.data.nodes.map((node) => {
                const master = node.roles.includes("master");
                return (
                  <TableRow key={node.id}>
                    <TableCell>
                      <div className="flex items-center gap-3">
                        <div className="rounded-lg border p-2">
                          <Server className="size-4" />
                        </div>
                        <div>
                          <div className="font-medium">{node.name}</div>
                          <div className="text-muted-foreground text-xs">
                            {node.region} · {node.public_identity}
                          </div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="text-xs">{node.roles.join(", ")}</TableCell>
                    <TableCell>
                      <StateBadge state={node.enabled ? node.health || "unknown" : "drain"} />
                    </TableCell>
                    <TableCell className="tabular-nums">
                      {node.latency_ms > 0 ? `${node.latency_ms} ms` : "—"}
                    </TableCell>
                    <TableCell>
                      <Switch
                        disabled={master || !node.enabled}
                        checked={node.client_visible}
                        onCheckedChange={(value) => void setNodeFlag(node, "publish", value)}
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
                            disabled={master}
                            onClick={() => void setNodeFlag(node, "enable", !node.enabled)}
                          >
                            <RefreshCw />
                            {nodeTrafficLabel(node.enabled, ru)}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            disabled={master}
                            onClick={() => void setNodeFlag(node, "publish", !node.client_visible)}
                          >
                            {node.client_visible ? <EyeOff /> : <Eye />}
                            {nodeVisibilityLabel(node.client_visible, ru)}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => {
                              setAssignNode(node);
                              setSelectedUser("");
                            }}
                          >
                            <Users />
                            {ru ? "Назначить пользователя" : "Assign user"}
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            disabled={master || busy}
                            onClick={() => void removeNode(node)}
                          >
                            <Trash2 />
                            {ru ? "Удалить" : "Remove"}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Dialog open={enrolOpen} onOpenChange={setEnrolOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{ru ? "Добавить сервер по SSH" : "Enrol server over SSH"}</DialogTitle>
            <DialogDescription>
              {ru
                ? "Сначала проверьте fingerprint SSH, затем подтвердите развёртывание."
                : "Verify the SSH fingerprint before confirming deployment."}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup className="grid md:grid-cols-2">
            {(
              [
                ["host", ru ? "IP или хост SSH" : "SSH host or IP"],
                ["port", "SSH port"],
                ["user", ru ? "Логин" : "Login"],
                ["password", ru ? "Временный пароль" : "Temporary password"],
                ["node_id", "Node ID"],
                ["name", ru ? "Название" : "Display name"],
                ["region", ru ? "Регион" : "Region"],
                ["domain", ru ? "Публичный VPN-домен" : "Public VPN domain"],
                ["addresses", ru ? "Публичные IP через запятую" : "Public IPs, comma separated"],
              ] as const
            ).map(([field, label]) => (
              <Field key={field} className={field === "addresses" ? "md:col-span-2" : ""}>
                <FieldLabel>{label}</FieldLabel>
                <Input
                  type={field === "password" ? "password" : "text"}
                  value={form[field]}
                  onChange={(event) => setField(field, event.target.value)}
                />
              </Field>
            ))}
          </FieldGroup>
          {fingerprint && (
            <div className="rounded-lg border p-3">
              <div className="text-muted-foreground text-xs">SSH fingerprint</div>
              <div className="break-all font-mono text-sm">{fingerprint}</div>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={busy} onClick={() => void verifyHost()}>
              {ru ? "Проверить SSH-ключ" : "Verify host key"}
            </Button>
            <Button disabled={busy || !fingerprint} onClick={() => void enrol()}>
              {ru ? "Развернуть и подключить" : "Deploy and enrol"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={assignNode !== null} onOpenChange={(open) => !open && setAssignNode(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Доступ к узлу" : "Node access"}</DialogTitle>
            <DialogDescription>{assignNode?.name}</DialogDescription>
          </DialogHeader>
          <Select value={selectedUser} onValueChange={setSelectedUser}>
            <SelectTrigger>
              <SelectValue placeholder={ru ? "Выберите пользователя" : "Select user"} />
            </SelectTrigger>
            <SelectContent>
              {(users.data?.users || [])
                .filter((user) => user.status === "active")
                .map((user) => (
                  <SelectItem key={user.id} value={user.id}>
                    {user.name}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
          <DialogFooter>
            <Button disabled={!selectedUser} onClick={() => void assignUser()}>
              {ru ? "Изменить назначение" : "Toggle assignment"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <JobDialog
        job={job}
        title={ru ? "Операция с кластером" : "Cluster operation"}
        onOpenChange={(open) => !open && setJob(null)}
      />
    </div>
  );
}
