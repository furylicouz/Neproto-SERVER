"use client";

import * as React from "react";

import { Database, MoreHorizontal, Plus, RefreshCw, Route, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { AdminError, AdminLoading, PageHeader, StateBadge } from "@/components/admin/admin-ui";
import { JobDialog } from "@/components/admin/job-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
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
  type ClusterRoute,
  type ClusterState,
  type ControlJob,
  type NP2User,
  type RouteState,
  waitForAdminJob,
} from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";
import { normalizeRouteState, routeMatchLabel } from "@/lib/route-state-view-model.mjs";

interface RouteDraft {
  id: string;
  name: string;
  priority: string;
  matchKind: string;
  matchValue: string;
  actionKind: string;
  nodeIDs: string[];
  userIDs: string[];
}

const emptyDraft: RouteDraft = {
  id: "",
  name: "",
  priority: "100",
  matchKind: "domain",
  matchValue: "",
  actionKind: "node",
  nodeIDs: [],
  userIDs: [],
};

export function RouteManagement({ locale }: { locale: AppLocale }) {
  const ru = locale === "ru";
  const routes = useAdminResource<RouteState>("routes", 12_000);
  const users = useAdminResource<{ users: NP2User[] }>("users");
  const cluster = useAdminResource<ClusterState>("cluster");
  const [draft, setDraft] = React.useState<RouteDraft>(emptyDraft);
  const [createOpen, setCreateOpen] = React.useState(false);
  const [assignRoute, setAssignRoute] = React.useState<ClusterRoute | null>(null);
  const [selectedUser, setSelectedUser] = React.useState("");
  const [job, setJob] = React.useState<ControlJob | null>(null);
  const [busy, setBusy] = React.useState(false);
  const routeState = React.useMemo(() => normalizeRouteState(routes.data), [routes.data]);

  const setField = <K extends keyof RouteDraft>(key: K, value: RouteDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }));

  const toggleList = (field: "nodeIDs" | "userIDs", value: string) => {
    setDraft((current) => ({
      ...current,
      [field]: current[field].includes(value)
        ? current[field].filter((item) => item !== value)
        : [...current[field], value],
    }));
  };

  async function createRoute() {
    setBusy(true);
    try {
      await adminFetch("routes", {
        method: "POST",
        json: {
          id: draft.id,
          name: draft.name,
          priority: Number(draft.priority),
          match: { kind: draft.matchKind, value: draft.matchValue },
          action: { kind: draft.actionKind, node_ids: draft.nodeIDs },
          user_ids: draft.userIDs,
        },
      });
      setDraft(emptyDraft);
      setCreateOpen(false);
      await routes.refresh();
      toast.success(ru ? "Маршрут создан" : "Route created");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function setEnabled(route: ClusterRoute, enabled: boolean) {
    try {
      await adminFetch(`routes/${encodeURIComponent(route.id)}/enable`, { method: "POST", json: { enabled } });
      await routes.refresh();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function toggleAssignment() {
    if (!assignRoute || !selectedUser) return;
    const access = routeState.access.find((entry) => entry.user_id === selectedUser);
    const enabled = !access?.allowed_route_ids.includes(assignRoute.id);
    try {
      await adminFetch(`routes/${encodeURIComponent(assignRoute.id)}/assign-user`, {
        method: "POST",
        json: { user_id: selectedUser, enabled },
      });
      setAssignRoute(null);
      await routes.refresh();
      toast.success(ru ? "Доступ пользователя обновлён" : "User route access updated");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function removeRoute(route: ClusterRoute) {
    if (!window.confirm(ru ? `Удалить маршрут «${route.name}»?` : `Delete route “${route.name}”?`)) return;
    try {
      await adminFetch(`routes/${encodeURIComponent(route.id)}`, { method: "DELETE", json: { confirm: "DELETE" } });
      await routes.refresh();
      toast.success(ru ? "Маршрут удалён" : "Route deleted");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function updateGeoData() {
    setBusy(true);
    try {
      const started = await adminFetch<ControlJob>("geodata/update", { method: "POST", json: { cluster: true } });
      setJob(started);
      await waitForAdminJob(started.id, setJob);
      await routes.refresh();
      toast.success(ru ? "GeoData обновлена на узлах" : "GeoData updated across nodes");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  async function setSchedule(value: string) {
    try {
      await adminFetch("geodata/schedule", { method: "POST", json: { preset: value } });
      await routes.refresh();
      toast.success(ru ? "Расписание обновлено" : "Schedule updated");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (routes.loading && !routes.data) return <AdminLoading />;
  if (routes.error || !routes.data) return <AdminError message={routes.error || "Routes unavailable"} />;

  const selectableNodes = (cluster.data?.nodes || []).filter((node: ClusterNode) => node.enabled);
  let matchPlaceholder = "example.com";
  if (draft.matchKind === "geosite") matchPlaceholder = "openai";
  else if (draft.matchKind === "geoip") matchPlaceholder = "NL";
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={ru ? "Маршрутизация" : "Routing"}
        description={
          ru ? "Правила NP/2 по доменам, сетям, GeoIP и GeoSite" : "NP/2 rules for domains, networks, GeoIP and GeoSite"
        }
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus />
            {ru ? "Создать маршрут" : "Create route"}
          </Button>
        }
      />

      <Card>
        <CardHeader className="flex-row items-center justify-between gap-4">
          <div>
            <CardTitle>{ru ? "Правила" : "Rules"}</CardTitle>
            <CardDescription>
              {routeState.routes.length} {ru ? "маршрутов" : "routes"}
            </CardDescription>
          </div>
          <Badge variant="outline">revision {routeState.revision}</Badge>
        </CardHeader>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{ru ? "Маршрут" : "Route"}</TableHead>
                <TableHead>{ru ? "Условие" : "Match"}</TableHead>
                <TableHead>{ru ? "Действие" : "Action"}</TableHead>
                <TableHead>{ru ? "Приоритет" : "Priority"}</TableHead>
                <TableHead>{ru ? "Активен" : "Enabled"}</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {routeState.routes.map((route) => (
                <TableRow key={route.id}>
                  <TableCell>
                    <div className="font-medium">{route.name}</div>
                    <div className="font-mono text-muted-foreground text-xs">{route.id}</div>
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary">{routeMatchLabel(route)}</Badge>
                  </TableCell>
                  <TableCell>
                    <span className="font-mono text-xs">
                      {route.action.kind}
                      {route.action.node_ids?.length ? ` → ${route.action.node_ids.join(" → ")}` : ""}
                    </span>
                  </TableCell>
                  <TableCell className="tabular-nums">{route.priority}</TableCell>
                  <TableCell>
                    <Switch checked={route.enabled} onCheckedChange={(value) => void setEnabled(route, value)} />
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
                          onClick={() => {
                            setAssignRoute(route);
                            setSelectedUser("");
                          }}
                        >
                          <Route />
                          {ru ? "Пользователи" : "Users"}
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem variant="destructive" onClick={() => void removeRoute(route)}>
                          <Trash2 />
                          {ru ? "Удалить" : "Delete"}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
              {routeState.routes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">
                    {ru ? "Маршрутов пока нет" : "No routes yet"}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Database className="size-5" />
            GeoData
          </CardTitle>
          <CardDescription>
            {ru ? "Одна проверенная база на всех узлах кластера" : "One verified dataset across every cluster node"}
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-[1fr_220px_auto] md:items-end">
          <div>
            <div className="text-muted-foreground text-sm">{ru ? "Состояние" : "State"}</div>
            <div className="mt-2">
              <StateBadge state={routeState.geodata.state} />
            </div>
            {routeState.geodata.updated_at && (
              <div className="mt-2 text-muted-foreground text-xs">
                {new Date(routeState.geodata.updated_at).toLocaleString(locale)}
              </div>
            )}
          </div>
          <Field>
            <FieldLabel>{ru ? "Автообновление" : "Automatic update"}</FieldLabel>
            <Select value={routeState.schedule} onValueChange={(value) => void setSchedule(value)}>
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
            <RefreshCw className={busy ? "animate-spin" : ""} />
            {ru ? "Обновить кластер" : "Update cluster"}
          </Button>
        </CardContent>
      </Card>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{ru ? "Новый маршрут" : "New route"}</DialogTitle>
            <DialogDescription>
              {ru
                ? "Форма использует те же правила валидации, что и консоль np."
                : "This form uses the same validation rules as the np console."}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup className="grid md:grid-cols-2">
            <Field>
              <FieldLabel>ID</FieldLabel>
              <Input value={draft.id} onChange={(event) => setField("id", event.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{ru ? "Название" : "Name"}</FieldLabel>
              <Input value={draft.name} onChange={(event) => setField("name", event.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{ru ? "Приоритет" : "Priority"}</FieldLabel>
              <Input
                type="number"
                value={draft.priority}
                onChange={(event) => setField("priority", event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel>{ru ? "Тип условия" : "Match type"}</FieldLabel>
              <Select value={draft.matchKind} onValueChange={(value) => setField("matchKind", value)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="domain">Domain</SelectItem>
                  <SelectItem value="cidr">IP / CIDR</SelectItem>
                  <SelectItem value="geoip">GeoIP</SelectItem>
                  <SelectItem value="geosite">GeoSite</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field className="md:col-span-2">
              <FieldLabel>{ru ? "Значение условия" : "Match value"}</FieldLabel>
              <Input
                value={draft.matchValue}
                onChange={(event) => setField("matchValue", event.target.value)}
                placeholder={matchPlaceholder}
              />
            </Field>
            <Field>
              <FieldLabel>{ru ? "Действие" : "Action"}</FieldLabel>
              <Select value={draft.actionKind} onValueChange={(value) => setField("actionKind", value)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="node">Node</SelectItem>
                  <SelectItem value="chain">Chain</SelectItem>
                  <SelectItem value="auto">Auto</SelectItem>
                  <SelectItem value="current">Current</SelectItem>
                  <SelectItem value="direct">Direct</SelectItem>
                  <SelectItem value="block">Block</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </FieldGroup>
          {(draft.actionKind === "node" || draft.actionKind === "chain") && (
            <div className="rounded-xl border p-4">
              <div className="mb-3 font-medium text-sm">{ru ? "Узлы назначения" : "Destination nodes"}</div>
              <div className="grid gap-3 md:grid-cols-2">
                {selectableNodes.map((node) => (
                  <label key={node.id} htmlFor={`route-node-${node.id}`} className="flex items-center gap-3 text-sm">
                    <Checkbox
                      id={`route-node-${node.id}`}
                      checked={draft.nodeIDs.includes(node.id)}
                      onCheckedChange={() => toggleList("nodeIDs", node.id)}
                    />
                    {node.name} <span className="text-muted-foreground">{node.region}</span>
                  </label>
                ))}
              </div>
            </div>
          )}
          <div className="rounded-xl border p-4">
            <div className="mb-3 font-medium text-sm">{ru ? "Пользователи" : "Users"}</div>
            <div className="grid gap-3 md:grid-cols-2">
              {(users.data?.users || [])
                .filter((user) => user.status === "active")
                .map((user) => (
                  <label key={user.id} htmlFor={`route-user-${user.id}`} className="flex items-center gap-3 text-sm">
                    <Checkbox
                      id={`route-user-${user.id}`}
                      checked={draft.userIDs.includes(user.id)}
                      onCheckedChange={() => toggleList("userIDs", user.id)}
                    />
                    {user.name}
                  </label>
                ))}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {ru ? "Отмена" : "Cancel"}
            </Button>
            <Button
              disabled={
                busy ||
                !draft.id ||
                !draft.name ||
                !draft.matchValue ||
                draft.userIDs.length === 0 ||
                ((draft.actionKind === "node" || draft.actionKind === "chain") && draft.nodeIDs.length === 0)
              }
              onClick={() => void createRoute()}
            >
              {ru ? "Создать" : "Create"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={assignRoute !== null} onOpenChange={(open) => !open && setAssignRoute(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{ru ? "Доступ к маршруту" : "Route access"}</DialogTitle>
            <DialogDescription>{assignRoute?.name}</DialogDescription>
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
            <Button disabled={!selectedUser} onClick={() => void toggleAssignment()}>
              {ru ? "Изменить назначение" : "Toggle assignment"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <JobDialog
        job={job}
        title={ru ? "Обновление GeoData" : "GeoData update"}
        onOpenChange={(open) => !open && setJob(null)}
      />
    </div>
  );
}
