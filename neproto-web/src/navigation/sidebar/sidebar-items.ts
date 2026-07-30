import {
  Activity,
  ArchiveRestore,
  Banknote,
  Calendar,
  ChartBar,
  CheckSquare,
  Fingerprint,
  Forklift,
  Gauge,
  GraduationCap,
  Kanban,
  LayoutDashboard,
  LayoutTemplate,
  ListTodo,
  Lock,
  type LucideIcon,
  Mail,
  MessageSquare,
  Network,
  ReceiptText,
  RefreshCw,
  Route,
  Server,
  Settings,
  ShoppingBag,
  SquareArrowUpRight,
  Users,
} from "lucide-react";

import { APP_MESSAGES, type AppLocale } from "@/lib/i18n";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

export function getSidebarItems(locale: AppLocale = "en"): NavGroup[] {
  const { navigation } = APP_MESSAGES[locale];

  return [
    {
      id: 1,
      label: navigation.main,
      items: [
        {
          id: "dashboard",
          title: navigation.dashboard,
          url: "/dashboard",
          icon: LayoutDashboard,
        },
        {
          id: "updates",
          title: navigation.updates,
          url: "/dashboard/system/updates",
          icon: RefreshCw,
        },
      ],
    },
    {
      id: 2,
      label: navigation.management,
      items: [
        { id: "users", title: navigation.users, url: "/dashboard/users", icon: Users },
        { id: "cluster", title: navigation.cluster, url: "/dashboard/cluster", icon: Network },
        { id: "routes", title: navigation.routes, url: "/dashboard/routes", icon: Route },
        { id: "services", title: navigation.services, url: "/dashboard/services", icon: Activity },
        { id: "settings", title: navigation.settings, url: "/dashboard/settings", icon: Settings },
        { id: "backups", title: navigation.backups, url: "/dashboard/backups", icon: ArchiveRestore },
      ],
    },
    {
      id: 3,
      label: navigation.templates,
      items: [
        {
          id: "template-dashboards",
          title: navigation.templateDashboards,
          icon: LayoutTemplate,
          subItems: [
            { id: "default", title: "Default", url: "/dashboard/default", icon: LayoutDashboard },
            { id: "crm", title: "CRM", url: "/dashboard/crm", icon: ChartBar },
            { id: "finance", title: "Finance", url: "/dashboard/finance", icon: Banknote },
            { id: "analytics", title: "Analytics", url: "/dashboard/analytics", icon: Gauge },
            { id: "productivity", title: "Productivity", url: "/dashboard/productivity", icon: ListTodo },
            { id: "ecommerce", title: "E-commerce", url: "/dashboard/ecommerce", icon: ShoppingBag },
            { id: "academy", title: "Academy", url: "/dashboard/academy", icon: GraduationCap },
            { id: "logistics", title: "Logistics", url: "/dashboard/logistics", icon: Forklift },
            { id: "infrastructure", title: "Infrastructure", url: "/dashboard/infrastructure", icon: Server },
          ],
        },
        {
          id: "template-pages",
          title: navigation.templatePages,
          icon: SquareArrowUpRight,
          subItems: [
            { id: "email", title: "Email", url: "/dashboard/mail", icon: Mail },
            { id: "chat", title: "Chat", url: "/dashboard/chat", icon: MessageSquare },
            { id: "calendar", title: "Calendar", url: "/dashboard/calendar", icon: Calendar },
            { id: "kanban", title: "Kanban", url: "/dashboard/kanban", icon: Kanban },
            { id: "tasks", title: "Tasks", url: "/dashboard/tasks", icon: CheckSquare },
            { id: "invoice", title: "Invoice", url: "/dashboard/invoice", icon: ReceiptText },
            { id: "users", title: "Users", url: "/dashboard/users", icon: Users },
            { id: "roles", title: "Roles", url: "/dashboard/roles", icon: Lock },
          ],
        },
        {
          id: "template-authentication",
          title: navigation.authentication,
          icon: Fingerprint,
          subItems: [
            { id: "auth-login-v1", title: "Login v1", url: "/auth/v1/login", newTab: true },
            { id: "auth-login-v2", title: "Login v2", url: "/auth/v2/login", newTab: true },
            { id: "auth-register-v1", title: "Register v1", url: "/auth/v1/register", newTab: true },
            { id: "auth-register-v2", title: "Register v2", url: "/auth/v2/register", newTab: true },
          ],
        },
        {
          id: "template-legacy",
          title: navigation.legacy,
          icon: LayoutDashboard,
          subItems: [
            { id: "legacy-default", title: "Default V1", url: "/dashboard/default-v1" },
            { id: "legacy-crm", title: "CRM V1", url: "/dashboard/crm-v1" },
            { id: "legacy-finance", title: "Finance V1", url: "/dashboard/finance-v1" },
            { id: "legacy-analytics", title: "Analytics V1", url: "/dashboard/analytics-v1" },
          ],
        },
        {
          id: "template-misc",
          title: navigation.misc,
          icon: SquareArrowUpRight,
          subItems: [
            {
              id: "others",
              title: "Others",
              url: "/dashboard/coming-soon",
              badge: "soon",
              disabled: true,
            },
          ],
        },
      ],
    },
  ];
}

export const sidebarItems = getSidebarItems();
