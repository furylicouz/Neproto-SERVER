"use client";

import { Activity, DatabaseBackup, Network, Route, Server, ShieldCheck, Users } from "lucide-react";

import { AdminError, AdminLoading, StateBadge } from "@/components/admin/admin-ui";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAdminResource } from "@/hooks/use-admin-resource";
import type { ClusterState, Overview } from "@/lib/admin-api";
import type { AppLocale } from "@/lib/i18n";

export function NeProtoOverview({ locale }: { locale: AppLocale }) {
  const overview = useAdminResource<Overview>("overview", 5000);
  const cluster = useAdminResource<ClusterState>("cluster", 10_000);
  const ru = locale === "ru";

  if (overview.loading && !overview.data) {
    return <AdminLoading />;
  }
  if (overview.error || !overview.data) {
    return <AdminError message={overview.error || "Overview unavailable"} />;
  }
  const data = overview.data;
  const metrics = [
    {
      title: ru ? "Пользователи" : "Users",
      value: data.active_users,
      detail: `${data.revoked_users} ${ru ? "отозвано" : "revoked"}`,
      icon: Users,
    },
    {
      title: ru ? "Узлы кластера" : "Cluster nodes",
      value: `${data.healthy_cluster_nodes}/${data.cluster_nodes}`,
      detail: `${ru ? "ревизия" : "revision"} ${data.cluster_revision}`,
      icon: Network,
    },
    {
      title: ru ? "Маршруты" : "Routes",
      value: `${data.enabled_routes}/${data.routes}`,
      detail: `${ru ? "GeoData" : "GeoData"}: ${data.geodata_state}`,
      icon: Route,
    },
    {
      title: ru ? "Резервные копии" : "Backups",
      value: data.backups,
      detail: data.geodata_schedule,
      icon: DatabaseBackup,
    },
  ];

  return (
    <div className="flex flex-col gap-4 md:gap-6">
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {metrics.map((metric) => (
          <Card key={metric.title}>
            <CardHeader className="flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="font-medium text-sm">{metric.title}</CardTitle>
              <metric.icon className="size-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              <div className="font-semibold text-2xl tabular-nums">{metric.value}</div>
              <p className="text-muted-foreground text-xs">{metric.detail}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid gap-4 xl:grid-cols-[1.35fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>{ru ? "Сервисы и узлы" : "Services and nodes"}</CardTitle>
            <CardDescription>
              {data.domain} · {data.deployment} · {data.version}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <div className="grid gap-3 sm:grid-cols-3">
              {[
                ["NP/2", data.services.np2, ShieldCheck],
                ["Web Admin", data.services.web, Server],
                ["Caddy", data.services.ingress, Activity],
              ].map(([name, state, Icon]) => (
                <div className="flex items-center justify-between rounded-lg border p-3" key={String(name)}>
                  <div className="flex items-center gap-2">
                    <Icon className="size-4 text-muted-foreground" />
                    <span className="font-medium text-sm">{String(name)}</span>
                  </div>
                  <StateBadge state={String(state)} />
                </div>
              ))}
            </div>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{ru ? "Узел" : "Node"}</TableHead>
                  <TableHead>{ru ? "Регион" : "Region"}</TableHead>
                  <TableHead>{ru ? "Состояние" : "State"}</TableHead>
                  <TableHead className="text-right">Ping</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(cluster.data?.nodes || []).map((node) => (
                  <TableRow key={node.id}>
                    <TableCell>
                      <div className="font-medium">{node.name}</div>
                      <div className="text-muted-foreground text-xs">{node.public_identity}</div>
                    </TableCell>
                    <TableCell>{node.region}</TableCell>
                    <TableCell>
                      <StateBadge state={node.health || (node.enabled ? "unknown" : "drain")} />
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {node.latency_ms > 0 ? `${node.latency_ms} ms` : "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{ru ? "Система" : "System"}</CardTitle>
            <CardDescription>{data.host.hostname}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {[
              [ru ? "Время работы" : "Uptime", data.host.uptime],
              [ru ? "Нагрузка" : "Load", data.host.load],
              [ru ? "Память" : "Memory", data.host.memory],
              [ru ? "Получено" : "Received", data.host.network_rx],
              [ru ? "Отправлено" : "Transmitted", data.host.network_tx],
              [ru ? "Публичные адреса" : "Public addresses", data.server_addresses.join(", ")],
            ].map(([label, value]) => (
              <div className="flex items-start justify-between gap-4 border-b pb-3 last:border-0 last:pb-0" key={label}>
                <span className="text-muted-foreground text-sm">{label}</span>
                <span className="text-right font-medium text-sm tabular-nums">{value}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
