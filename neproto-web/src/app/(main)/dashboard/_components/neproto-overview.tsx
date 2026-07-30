"use client";

import { Activity, DatabaseBackup, Gauge, Network, RefreshCw, Route, Server, ShieldCheck, Users } from "lucide-react";

import { AdminError, AdminLoading, StateBadge } from "@/components/admin/admin-ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAdminResource } from "@/hooks/use-admin-resource";
import type { ClusterNode, ClusterState, Overview } from "@/lib/admin-api";
import { buildDashboardSnapshot } from "@/lib/dashboard-view-model.mjs";
import type { AppLocale } from "@/lib/i18n";

export function NeProtoOverview({ locale }: { locale: AppLocale }) {
  const overview = useAdminResource<Overview>("overview", 5000);
  const cluster = useAdminResource<ClusterState>("cluster", 10_000);
  const ru = locale === "ru";

  if (overview.loading && !overview.data) return <AdminLoading />;
  if (overview.error || !overview.data) {
    return <AdminError message={overview.error || "Overview unavailable"} />;
  }

  const data = overview.data;
  const snapshot = buildDashboardSnapshot(data);
  const nodes = cluster.data?.nodes || [];
  const refresh = () => {
    void Promise.all([overview.refresh(), cluster.refresh()]).catch(() => undefined);
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="space-y-1">
        <h1 className="text-3xl tracking-tight">{ru ? "Панель управления" : "Dashboard"}</h1>
        <p className="text-muted-foreground text-sm">
          {data.domain} · {data.version} · {data.deployment}
        </p>
      </div>

      <Tabs defaultValue="overview" className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <TabsList className="gap-1">
            <TabsTrigger value="overview">{ru ? "Обзор" : "Overview"}</TabsTrigger>
            <TabsTrigger value="cluster">{ru ? "Кластер" : "Cluster"}</TabsTrigger>
            <TabsTrigger value="system">{ru ? "Система" : "System"}</TabsTrigger>
          </TabsList>
          <Button variant="outline" onClick={refresh} disabled={overview.loading || cluster.loading}>
            <RefreshCw data-icon="inline-start" />
            {ru ? "Обновить" : "Refresh"}
          </Button>
        </div>

        <TabsContent value="overview" className="flex flex-col gap-4">
          <AnalyticsKpiStrip data={data} snapshot={snapshot} ru={ru} />

          <div className="grid grid-cols-1 items-stretch gap-4 xl:grid-cols-12">
            <div className="xl:col-span-7">
              <ServiceHealth data={data} snapshot={snapshot} ru={ru} />
            </div>
            <div className="xl:col-span-5">
              <LiveHost data={data} snapshot={snapshot} ru={ru} />
            </div>
          </div>

          <div className="grid grid-cols-1 items-stretch gap-4 xl:grid-cols-12">
            <div className="xl:col-span-7">
              <ClusterNodes nodes={nodes} loading={cluster.loading} error={cluster.error} ru={ru} />
            </div>
            <div className="xl:col-span-5 xl:col-start-8">
              <RoutingAndSecurity data={data} ru={ru} />
            </div>
          </div>
        </TabsContent>

        <TabsContent value="cluster">
          <ClusterNodes nodes={nodes} loading={cluster.loading} error={cluster.error} ru={ru} expanded />
        </TabsContent>

        <TabsContent value="system">
          <div className="grid gap-4 lg:grid-cols-2">
            <LiveHost data={data} snapshot={snapshot} ru={ru} />
            <ServiceHealth data={data} snapshot={snapshot} ru={ru} />
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}

type DashboardSnapshot = ReturnType<typeof buildDashboardSnapshot>;

function AnalyticsKpiStrip({ data, snapshot, ru }: { data: Overview; snapshot: DashboardSnapshot; ru: boolean }) {
  const metrics = [
    {
      title: ru ? "Активные пользователи" : "Active users",
      value: snapshot.activeUsers,
      detail: `${snapshot.revokedUsers} ${ru ? "отозвано" : "revoked"}`,
      icon: Users,
    },
    {
      title: ru ? "Здоровые узлы" : "Healthy nodes",
      value: `${snapshot.healthyClusterNodes}/${snapshot.clusterNodes}`,
      detail: `${snapshot.clusterHealthPercent}% ${ru ? "кластера доступно" : "cluster availability"}`,
      icon: Network,
    },
    {
      title: ru ? "Маршруты" : "Routes",
      value: `${snapshot.enabledRoutes}/${snapshot.routes}`,
      detail: `${ru ? "GeoData" : "GeoData"}: ${data.geodata_state}`,
      icon: Route,
    },
    {
      title: ru ? "Сервисы" : "Services",
      value: `${snapshot.healthyServices}/${snapshot.totalServices}`,
      detail: ru ? "NP/2, Web и ingress" : "NP/2, Web, and ingress",
      icon: ShieldCheck,
    },
    {
      title: ru ? "Резервные копии" : "Backups",
      value: snapshot.backups,
      detail: ru ? "Доступные точки восстановления" : "Available restore points",
      icon: DatabaseBackup,
    },
  ];

  return (
    <div className="overflow-hidden rounded-xl bg-card shadow-xs ring-1 ring-foreground/10">
      <div className="grid divide-y *:data-[slot=card]:rounded-none *:data-[slot=card]:ring-0 md:grid-cols-2 md:divide-x md:divide-y-0 xl:grid-cols-5">
        {metrics.map((metric) => (
          <Card key={metric.title}>
            <CardHeader>
              <CardTitle className="font-normal text-sm">{metric.title}</CardTitle>
              <CardAction>
                <metric.icon className="size-4 text-muted-foreground" />
              </CardAction>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <div className="text-2xl tabular-nums leading-none tracking-tight">{metric.value}</div>
              <div className="text-muted-foreground text-xs">{metric.detail}</div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}

function ServiceHealth({ data, snapshot, ru }: { data: Overview; snapshot: DashboardSnapshot; ru: boolean }) {
  const services = [
    ["NP/2", data.services.np2, ShieldCheck],
    ["Web Admin", data.services.web, Server],
    ["Caddy", data.services.ingress, Activity],
  ] as const;
  const percent =
    snapshot.totalServices > 0 ? Math.round((snapshot.healthyServices / snapshot.totalServices) * 100) : 0;
  return (
    <Card className="h-full">
      <CardHeader>
        <CardTitle className="font-normal">{ru ? "Состояние сервисов" : "Service Health"}</CardTitle>
        <CardAction>
          <Badge variant="outline">{percent}%</Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-5">
        <div>
          <div className="mb-2 flex items-center justify-between text-sm">
            <span className="text-muted-foreground">{ru ? "Доступность компонентов" : "Component availability"}</span>
            <span className="font-medium tabular-nums">
              {snapshot.healthyServices}/{snapshot.totalServices}
            </span>
          </div>
          <Progress value={percent} className="h-2" />
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          {services.map(([name, state, Icon]) => (
            <div className="flex items-center justify-between gap-3 rounded-lg border p-3" key={name}>
              <div className="flex min-w-0 items-center gap-2">
                <Icon className="size-4 shrink-0 text-muted-foreground" />
                <span className="truncate font-medium text-sm">{name}</span>
              </div>
              <StateBadge state={state} />
            </div>
          ))}
        </div>
        <div className="grid gap-3 text-sm sm:grid-cols-2">
          <SystemDatum label={ru ? "Версия" : "Version"} value={data.version} />
          <SystemDatum label={ru ? "Развёртывание" : "Deployment"} value={data.deployment} />
          <SystemDatum label={ru ? "Домен" : "Domain"} value={data.domain} />
          <SystemDatum label={ru ? "Адреса" : "Addresses"} value={data.server_addresses.join(", ") || "—"} />
        </div>
      </CardContent>
    </Card>
  );
}

function LiveHost({ data, snapshot, ru }: { data: Overview; snapshot: DashboardSnapshot; ru: boolean }) {
  return (
    <Card className="h-full">
      <CardHeader>
        <CardTitle className="font-normal">{ru ? "Сервер в реальном времени" : "Live Host"}</CardTitle>
        <CardAction>
          <span className="flex items-center gap-2 text-muted-foreground text-sm">
            <span className="relative flex size-2">
              <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500 opacity-75" />
              <span className="relative inline-flex size-2 rounded-full bg-emerald-500" />
            </span>
            {ru ? "Онлайн" : "Live"}
          </span>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <div>
          <div className="mb-2 flex items-end justify-between">
            <div>
              <div className="text-muted-foreground text-sm">{ru ? "Использование памяти" : "Memory usage"}</div>
              <div className="mt-1 text-2xl tabular-nums leading-none tracking-tight">{snapshot.memoryPercent}%</div>
            </div>
            <Gauge className="size-5 text-muted-foreground" />
          </div>
          <Progress value={snapshot.memoryPercent} className="h-2" />
        </div>
        <div className="grid grid-cols-2">
          <LiveDatum label={ru ? "Получено" : "Received"} value={data.host.network_rx} border="border-r border-b" />
          <LiveDatum label={ru ? "Отправлено" : "Transmitted"} value={data.host.network_tx} border="border-b" />
          <LiveDatum label={ru ? "Время работы" : "Uptime"} value={data.host.uptime} border="border-r" />
          <LiveDatum label={ru ? "Нагрузка" : "Load"} value={data.host.load} />
        </div>
        <SystemDatum label={ru ? "Хост" : "Host"} value={data.host.hostname} />
        <SystemDatum label={ru ? "Память" : "Memory"} value={data.host.memory} />
      </CardContent>
    </Card>
  );
}

function ClusterNodes({
  nodes,
  loading,
  error,
  ru,
  expanded = false,
}: {
  nodes: ClusterNode[];
  loading: boolean;
  error: string | null;
  ru: boolean;
  expanded?: boolean;
}) {
  return (
    <Card className="h-full gap-2">
      <CardHeader>
        <CardTitle className="font-normal">{ru ? "Узлы кластера" : "Cluster Nodes"}</CardTitle>
        <CardAction>
          <Badge variant="outline">{nodes.length}</Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        <ClusterNodeContent nodes={nodes} loading={loading} error={error} ru={ru} expanded={expanded} />
      </CardContent>
    </Card>
  );
}

function ClusterNodeContent({
  nodes,
  loading,
  error,
  ru,
  expanded,
}: {
  nodes: ClusterNode[];
  loading: boolean;
  error: string | null;
  ru: boolean;
  expanded: boolean;
}) {
  if (error) return <p className="px-4 py-8 text-destructive text-sm">{error}</p>;
  if (loading && nodes.length === 0) {
    return <p className="px-4 py-8 text-muted-foreground text-sm">{ru ? "Загрузка кластера…" : "Loading cluster…"}</p>;
  }
  if (nodes.length === 0) {
    return (
      <p className="px-4 py-8 text-muted-foreground text-sm">
        {ru ? "В кластере пока нет узлов." : "No cluster nodes yet."}
      </p>
    );
  }

  return (
    <Table className="[&_td:first-child]:pl-4 [&_td:last-child]:pr-4 [&_th:first-child]:pl-4 [&_th:last-child]:pr-4">
      <TableHeader className="[&_tr]:border-border/50">
        <TableRow className="hover:bg-transparent">
          <TableHead className="h-8">{ru ? "Узел" : "Node"}</TableHead>
          <TableHead className="h-8">{ru ? "Регион" : "Region"}</TableHead>
          <TableHead className="h-8">{ru ? "Состояние" : "Health"}</TableHead>
          <TableHead className="h-8 text-right">Ping</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody className="[&_tr]:border-border/50">
        {nodes.slice(0, expanded ? nodes.length : 5).map((node) => (
          <TableRow className="hover:bg-transparent" key={node.id}>
            <TableCell className="py-4">
              <div className="font-medium">{node.name}</div>
              <div className="max-w-56 truncate text-muted-foreground text-xs">{node.public_identity}</div>
            </TableCell>
            <TableCell>{node.region || "—"}</TableCell>
            <TableCell>
              <StateBadge state={node.enabled ? node.health || "unknown" : "drain"} />
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {node.latency_ms > 0 ? `${node.latency_ms} ms` : "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function RoutingAndSecurity({ data, ru }: { data: Overview; ru: boolean }) {
  const items = [
    [ru ? "Маршруты" : "Routes", `${data.enabled_routes}/${data.routes}`],
    ["GeoData", data.geodata_state],
    [ru ? "Обновление GeoData" : "GeoData schedule", data.geodata_schedule],
    ["Constellation", featureStateLabel(data.enable_constellation, ru, "Включён", "Выключен")],
    [
      ru ? "Прямая секретность" : "Forward secrecy",
      featureStateLabel(data.enable_forward_secrecy, ru, "Включена", "Выключена"),
    ],
  ];
  return (
    <Card className="h-full gap-2">
      <CardHeader>
        <CardTitle className="font-normal">{ru ? "Маршрутизация и защита" : "Routing & Security"}</CardTitle>
        <CardAction>
          <ShieldCheck className="size-4 text-muted-foreground" />
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-0">
        {items.map(([label, value]) => (
          <SystemDatum key={label} label={String(label)} value={String(value)} />
        ))}
      </CardContent>
    </Card>
  );
}

function featureStateLabel(enabled: boolean, ru: boolean, enabledRu: string, disabledRu: string) {
  if (enabled) return ru ? enabledRu : "Enabled";
  return ru ? disabledRu : "Disabled";
}

function SystemDatum({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b py-3 first:pt-0 last:border-0 last:pb-0">
      <span className="text-muted-foreground text-sm">{label}</span>
      <span className="max-w-[65%] break-words text-right font-medium text-sm tabular-nums">{value}</span>
    </div>
  );
}

function LiveDatum({ label, value, border = "" }: { label: string; value: string; border?: string }) {
  return (
    <div className={`border-border/50 p-4 first:pl-0 even:pr-0 ${border}`}>
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className="mt-1 truncate font-medium text-sm tabular-nums" title={value}>
        {value}
      </div>
    </div>
  );
}
