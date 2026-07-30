"use client";

import {
  Activity,
  ArrowUpDown,
  ChevronDown,
  CircleGauge,
  Clock3,
  Eye,
  EyeOff,
  FileText,
  MapPin,
  MoreHorizontal,
  Network,
  PlusCircle,
  RefreshCw,
  Search,
  Server,
  Trash2,
  Users,
} from "lucide-react";

import { StateBadge } from "@/components/admin/admin-ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { Kbd } from "@/components/ui/kbd";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { ClusterNode, ClusterState } from "@/lib/admin-api";
import {
  buildClusterGroups,
  clusterConnectivity,
  clusterLocationCode,
  clusterSummary,
} from "@/lib/cluster-view-model.mjs";
import { cn } from "@/lib/utils";

interface ClusterInfrastructureProps {
  cluster: ClusterState;
  query: string;
  busy: boolean;
  ru: boolean;
  onQueryChange: (query: string) => void;
  onRefresh: () => void;
  onSyncUsers: () => void;
  onEnrolNode: () => void;
  onSetNodeFlag: (node: ClusterNode, action: "enable" | "publish", enabled: boolean) => void;
  onAssignUser: (node: ClusterNode) => void;
  onRemoveNode: (node: ClusterNode) => void;
}

export function ClusterInfrastructure({
  cluster,
  query,
  busy,
  ru,
  onQueryChange,
  onRefresh,
  onSyncUsers,
  onEnrolNode,
  onSetNodeFlag,
  onAssignUser,
  onRemoveNode,
}: ClusterInfrastructureProps) {
  const summary = clusterSummary(cluster.nodes);
  const groups = buildClusterGroups(cluster.nodes, query);
  const latestCheck = cluster.nodes
    .map((node) => Date.parse(node.checked_at))
    .filter(Number.isFinite)
    .sort((left, right) => right - left)[0];
  const latestCheckLabel = formatLatestCheck(latestCheck, ru);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex min-w-0 flex-col gap-1">
            <h1 className="font-medium text-2xl leading-tight tracking-tight sm:text-3xl sm:leading-none">
              {ru ? "Инфраструктура кластера" : "Cluster Infrastructure"}
            </h1>
            <p className="text-muted-foreground text-sm">
              {cluster.cluster_id} · {ru ? "ревизия" : "revision"} {cluster.revision}
            </p>
          </div>

          <div className="flex w-full items-center justify-between gap-2 sm:w-auto sm:justify-end">
            <span className="whitespace-nowrap text-muted-foreground text-sm">{latestCheckLabel}</span>
            <Button
              variant="outline"
              size="icon-sm"
              disabled={busy}
              onClick={onRefresh}
              aria-label={ru ? "Обновить кластер" : "Refresh cluster"}
            >
              <RefreshCw />
            </Button>
          </div>
        </div>

        <div className="flex flex-wrap gap-2">
          <Badge variant="outline" className="h-auto gap-1 rounded-sm px-1.5 py-0.5">
            <Network />
            {summary.total} {ru ? "узлов" : "nodes"}
          </Badge>
          <Badge variant="outline" className="h-auto gap-1 rounded-sm px-1.5 py-0.5">
            <Server />
            {summary.enabled} {ru ? "принимают трафик" : "accepting traffic"}
          </Badge>
          <Badge variant="outline" className="h-auto gap-1 rounded-sm px-1.5 py-0.5">
            <span className="size-2 rounded-full bg-emerald-500" />
            {summary.healthy} {ru ? "здоровы" : "healthy"}
          </Badge>
          <Badge variant="outline" className="h-auto gap-1 rounded-sm px-1.5 py-0.5">
            <Eye />
            {summary.clientVisible} {ru ? "доступны клиентам" : "client-visible"}
          </Badge>
        </div>
      </div>

      <div className="flex flex-col gap-3 xl:flex-row">
        <InputGroup className="flex-1">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            placeholder={
              ru ? "Поиск по узлу, региону, роли, IP или домену…" : "Search node, region, role, IP, or domain…"
            }
            aria-label={ru ? "Поиск узлов кластера" : "Search cluster nodes"}
          />
          <InputGroupAddon align="inline-end">
            <Kbd>⌘ K</Kbd>
          </InputGroupAddon>
        </InputGroup>

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" disabled={busy} onClick={onSyncUsers}>
            <Users data-icon="inline-start" />
            {ru ? "Синхронизировать пользователей" : "Sync users"}
          </Button>
          <Button variant="outline" onClick={onEnrolNode}>
            <PlusCircle data-icon="inline-start" />
            {ru ? "Добавить сервер" : "Enrol server"}
          </Button>
        </div>
      </div>

      <div className="flex flex-col gap-4">
        {groups.map((group) => (
          <ClusterRegion
            key={group.region}
            region={group.region === "Unassigned" && ru ? "Без региона" : group.region}
            nodes={group.nodes}
            busy={busy}
            ru={ru}
            onSetNodeFlag={onSetNodeFlag}
            onAssignUser={onAssignUser}
            onRemoveNode={onRemoveNode}
          />
        ))}
        {groups.length === 0 && (
          <div className="flex min-h-32 items-center justify-center rounded-xl border bg-muted/30 p-6 text-center">
            <div>
              <Search className="mx-auto mb-2 size-5 text-muted-foreground" />
              <p className="font-medium text-sm">{ru ? "Узлы не найдены" : "No nodes found"}</p>
              <p className="mt-1 text-muted-foreground text-sm">
                {ru ? "Измените поисковый запрос." : "Try a different search query."}
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function ClusterRegion({
  region,
  nodes,
  busy,
  ru,
  onSetNodeFlag,
  onAssignUser,
  onRemoveNode,
}: {
  region: string;
  nodes: ClusterNode[];
  busy: boolean;
  ru: boolean;
  onSetNodeFlag: ClusterInfrastructureProps["onSetNodeFlag"];
  onAssignUser: ClusterInfrastructureProps["onAssignUser"];
  onRemoveNode: ClusterInfrastructureProps["onRemoveNode"];
}) {
  const healthy = clusterSummary(nodes).healthy;
  const countryCode = clusterLocationCode(region);
  return (
    <Collapsible
      defaultOpen
      className="flex flex-col overflow-hidden rounded-xl border bg-card py-3 text-card-foreground data-[state=open]:gap-3 data-[state=open]:pb-0"
    >
      <div className="flex items-center gap-2 px-4">
        <CollapsibleTrigger asChild>
          <Button
            variant="ghost"
            className="group -ml-2 h-auto min-w-0 flex-1 justify-start gap-2 px-2 py-1 hover:bg-transparent aria-expanded:bg-transparent"
          >
            <ChevronDown className="group-data-[state=open]:rotate-180" />
            <LocationIcon countryCode={countryCode} />
            <span className="truncate font-medium leading-none">{region}</span>
            <span className="text-muted-foreground text-sm">
              ({nodes.length} {ru ? "узл." : "nodes"})
            </span>
          </Button>
        </CollapsibleTrigger>
        <Badge variant="outline" className="rounded-sm">
          <Activity />
          {healthy}/{nodes.length} {ru ? "здоровы" : "healthy"}
        </Badge>
      </div>

      <CollapsibleContent>
        <ClusterNodeTable
          nodes={nodes}
          busy={busy}
          ru={ru}
          onSetNodeFlag={onSetNodeFlag}
          onAssignUser={onAssignUser}
          onRemoveNode={onRemoveNode}
        />
      </CollapsibleContent>
    </Collapsible>
  );
}

function ClusterNodeTable({
  nodes,
  busy,
  ru,
  onSetNodeFlag,
  onAssignUser,
  onRemoveNode,
}: {
  nodes: ClusterNode[];
  busy: boolean;
  ru: boolean;
  onSetNodeFlag: ClusterInfrastructureProps["onSetNodeFlag"];
  onAssignUser: ClusterInfrastructureProps["onAssignUser"];
  onRemoveNode: ClusterInfrastructureProps["onRemoveNode"];
}) {
  return (
    <div className="scrollbar-thin overflow-x-auto [scrollbar-color:var(--border)_transparent] **:data-[slot=table-container]:overflow-visible [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-border [&::-webkit-scrollbar-track]:bg-transparent [&::-webkit-scrollbar]:h-1">
      <Table className="min-w-[1780px] table-fixed **:data-[slot='table-cell']:px-5 **:data-[slot='table-head']:px-5">
        <colgroup>
          <col className="w-72" />
          <col className="w-48" />
          <col className="w-64" />
          <col className="w-44" />
          <col className="w-40" />
          <col className="w-36" />
          <col className="w-80" />
          <col className="w-52" />
          <col className="w-44" />
          <col className="w-18" />
        </colgroup>
        <TableHeader className="bg-muted/50 [&_tr]:border-y">
          <TableRow>
            <TableHead className="font-medium">
              <span className="inline-flex items-center gap-1">
                {ru ? "Узел" : "Node"} <ArrowUpDown className="size-4" />
              </span>
            </TableHead>
            <TableHead>{ru ? "Локация" : "Location"}</TableHead>
            <TableHead>{ru ? "NP/2 endpoint" : "NP/2 endpoint"}</TableHead>
            <TableHead>{ru ? "Роли" : "Roles"}</TableHead>
            <TableHead>{ru ? "Состояние" : "Health"}</TableHead>
            <TableHead>{ru ? "Задержка" : "Latency"}</TableHead>
            <TableHead>{ru ? "Качество соединения" : "Connectivity"}</TableHead>
            <TableHead>{ru ? "Последняя проверка" : "Last check"}</TableHead>
            <TableHead>{ru ? "Доступ клиентам" : "Client access"}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody className="**:data-[slot='table-row']:hover:bg-transparent">
          {nodes.map((node) => {
            const master = node.roles.includes("master");
            const countryCode = clusterLocationCode(node.region);
            const connectivity = clusterConnectivity(node);
            return (
              <TableRow key={node.id}>
                <TableCell>
                  <span className="flex items-center gap-3">
                    <span className="rounded-lg border p-2">
                      <Server className="size-4" />
                    </span>
                    <span className="min-w-0">
                      <span className="block truncate font-medium">{node.name}</span>
                      <span className="block truncate text-muted-foreground text-xs">{node.id}</span>
                    </span>
                  </span>
                </TableCell>
                <TableCell>
                  <span className="flex items-center gap-2">
                    <LocationIcon countryCode={countryCode} />
                    <span className="min-w-0">
                      <span className="block truncate font-medium">
                        {node.region || (ru ? "Не указана" : "Unassigned")}
                      </span>
                      <span className="block text-muted-foreground text-xs">{countryCode ?? "—"}</span>
                    </span>
                  </span>
                </TableCell>
                <TableCell>
                  <span className="block truncate font-medium" title={node.np2_endpoint || node.public_identity}>
                    {node.np2_endpoint || node.public_identity}
                  </span>
                  <span className="block truncate text-muted-foreground text-xs">
                    {node.public_addresses.join(", ") || "—"}
                  </span>
                </TableCell>
                <TableCell>
                  <span className="flex flex-wrap gap-1">
                    {node.roles.map((role) => (
                      <Badge key={role} variant="secondary" className="rounded-sm">
                        {role}
                      </Badge>
                    ))}
                  </span>
                </TableCell>
                <TableCell>
                  <StateBadge state={node.enabled ? node.health || "unknown" : "drain"} />
                </TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5 text-muted-foreground tabular-nums">
                    <CircleGauge className="size-4" />
                    {node.latency_ms > 0 ? `${node.latency_ms} ms` : "—"}
                  </span>
                </TableCell>
                <TableCell>
                  <div className="grid grid-cols-3 gap-4">
                    <ConnectivityMeter label="LINK" value={connectivity.link} />
                    <ConnectivityMeter label="SIGNAL" value={connectivity.signal} />
                    <ConnectivityMeter label="ACCESS" value={connectivity.access} />
                  </div>
                </TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5 text-muted-foreground tabular-nums">
                    <Clock3 className="size-4" />
                    {formatCheckedAt(node.checked_at, ru)}
                  </span>
                </TableCell>
                <TableCell>
                  <span className="flex items-center gap-2">
                    <Switch
                      disabled={master || !node.enabled}
                      checked={node.client_visible}
                      onCheckedChange={(value) => onSetNodeFlag(node, "publish", value)}
                      aria-label={ru ? `Доступ клиентов к ${node.name}` : `Client access to ${node.name}`}
                    />
                    <span className="text-muted-foreground text-sm">
                      {clientVisibilityStateLabel(node.client_visible, ru)}
                    </span>
                  </span>
                </TableCell>
                <TableCell className="text-right">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="-mr-2"
                        aria-label={ru ? `Действия узла ${node.name}` : `Actions for ${node.name}`}
                      >
                        <MoreHorizontal />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent className="w-52" align="end">
                      <DropdownMenuGroup>
                        <DropdownMenuItem
                          disabled={master}
                          onClick={() => onSetNodeFlag(node, "enable", !node.enabled)}
                        >
                          <RefreshCw />
                          {nodeTrafficLabel(node.enabled, ru)}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          disabled={master}
                          onClick={() => onSetNodeFlag(node, "publish", !node.client_visible)}
                        >
                          {node.client_visible ? <EyeOff /> : <Eye />}
                          {nodeVisibilityLabel(node.client_visible, ru)}
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => onAssignUser(node)}>
                          <Users />
                          {ru ? "Назначить пользователя" : "Assign user"}
                        </DropdownMenuItem>
                        <DropdownMenuItem disabled>
                          <FileText />
                          {ru ? "Проверено автоматически" : "Health is monitored"}
                        </DropdownMenuItem>
                      </DropdownMenuGroup>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        variant="destructive"
                        disabled={master || busy}
                        onClick={() => onRemoveNode(node)}
                      >
                        <Trash2 />
                        {ru ? "Удалить из кластера" : "Remove from cluster"}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

function LocationIcon({ countryCode }: { countryCode: string | null }) {
  if (!countryCode) {
    return (
      <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/40">
        <MapPin className="size-4 text-muted-foreground" />
      </span>
    );
  }
  return (
    <span
      aria-hidden="true"
      className={cn("shrink-0 rounded-xs text-xl ring-1 ring-foreground/10", `flag:${countryCode}`)}
    />
  );
}

function ConnectivityMeter({ label, value }: { label: string; value: number }) {
  const critical = value < 40;
  const warning = value >= 40 && value < 70;
  return (
    <span className="min-w-0 space-y-1">
      <span className="flex items-baseline justify-between gap-2 text-xs">
        <span className="font-medium text-muted-foreground">{label}</span>
        <span
          className={cn(
            "font-medium text-emerald-600 tabular-nums dark:text-emerald-400",
            warning && "text-amber-600 dark:text-amber-400",
            critical && "text-destructive",
          )}
        >
          {value}%
        </span>
      </span>
      <span className="block h-1.5 overflow-hidden rounded-full bg-muted-foreground/20">
        <span
          className={cn(
            "block h-full rounded-full bg-emerald-500",
            warning && "bg-amber-500",
            critical && "bg-destructive",
          )}
          style={{ width: `${value}%` }}
        />
      </span>
    </span>
  );
}

function nodeTrafficLabel(enabled: boolean, ru: boolean) {
  if (enabled) return ru ? "Вывести из трафика" : "Drain";
  return ru ? "Включить" : "Enable";
}

function nodeVisibilityLabel(visible: boolean, ru: boolean) {
  if (visible) return ru ? "Скрыть от клиентов" : "Hide from clients";
  return ru ? "Опубликовать клиентам" : "Publish to clients";
}

function formatCheckedAt(value: string, ru: boolean) {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return "—";
  return new Intl.DateTimeFormat(ru ? "ru-RU" : "en-US", {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  }).format(timestamp);
}

function formatLatestCheck(timestamp: number | undefined, ru: boolean) {
  if (!timestamp) return ru ? "Проверка ожидается" : "Awaiting health check";
  const value = new Intl.DateTimeFormat(ru ? "ru-RU" : "en-US", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(timestamp);
  return `${ru ? "Проверено" : "Checked"}: ${value}`;
}

function clientVisibilityStateLabel(visible: boolean, ru: boolean) {
  if (visible) return ru ? "Открыт" : "Visible";
  return ru ? "Скрыт" : "Hidden";
}
