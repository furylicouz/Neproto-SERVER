import { DollarSign, TrendingDown, TrendingUp, UserPlus, Users, Waves } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { APP_MESSAGES, type AppLocale } from "@/lib/i18n";

export function MetricCards({ locale }: { locale: AppLocale }) {
  const messages = APP_MESSAGES[locale].dashboard.metrics;

  return (
    <div className="grid grid-cols-1 gap-4 *:data-[slot=card]:bg-linear-to-t *:data-[slot=card]:from-primary/5 *:data-[slot=card]:to-card *:data-[slot=card]:shadow-xs xl:grid-cols-4 dark:*:data-[slot=card]:bg-card">
      <Card>
        <CardHeader>
          <CardTitle>
            <div className="flex size-7 items-center justify-center rounded-lg border bg-muted text-muted-foreground">
              <DollarSign className="size-4" />
            </div>
          </CardTitle>
          <CardDescription>{messages.revenue}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <div className="font-medium text-3xl tabular-nums leading-none tracking-tight">$1,250.00</div>
            <Badge>
              <TrendingUp className="size-3" />
              +12.5%
            </Badge>
          </div>
          <p className="text-muted-foreground text-sm">{messages.revenueHint}</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>
            <div className="flex size-7 items-center justify-center rounded-lg border bg-muted text-muted-foreground">
              <UserPlus className="size-4" />
            </div>
          </CardTitle>
          <CardDescription>{messages.customers}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <div className="font-medium text-3xl tabular-nums leading-none tracking-tight">1,234</div>
            <Badge variant="destructive">
              <TrendingDown className="size-3" />
              -20%
            </Badge>
          </div>
          <p className="text-muted-foreground text-sm">{messages.customersHint}</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>
            <div className="flex size-7 items-center justify-center rounded-lg border bg-muted text-muted-foreground">
              <Users className="size-4" />
            </div>
          </CardTitle>
          <CardDescription>{messages.accounts}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <div className="font-medium text-3xl tabular-nums leading-none tracking-tight">45,678</div>
            <Badge>
              <TrendingUp className="size-3" />
              +12.5%
            </Badge>
          </div>
          <p className="text-muted-foreground text-sm">{messages.accountsHint}</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>
            <div className="flex size-7 items-center justify-center rounded-lg border bg-muted text-muted-foreground">
              <Waves className="size-4" />
            </div>
          </CardTitle>
          <CardDescription>{messages.growth}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <div className="font-medium text-3xl tabular-nums leading-none tracking-tight">4.5%</div>
            <Badge>
              <TrendingUp className="size-3" />
              +4.5%
            </Badge>
          </div>
          <p className="text-muted-foreground text-sm">{messages.growthHint}</p>
        </CardContent>
      </Card>
    </div>
  );
}
