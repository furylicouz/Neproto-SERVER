"use client";

import { Download } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { APP_MESSAGES, type AppLocale } from "@/lib/i18n";

import customersData from "./data.json";
import type { RecentCustomerRow } from "./recent-customers-table/schema";
import { RecentCustomersTable } from "./recent-customers-table/table";

const customers = customersData as RecentCustomerRow[];

export function SubscriberOverview({ locale }: { locale: AppLocale }) {
  const messages = APP_MESSAGES[locale].dashboard.customers;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="leading-none">{messages.title}</CardTitle>
        <CardDescription>{messages.description}</CardDescription>
        <CardAction>
          <Button variant="outline" size="sm">
            <Download />
            {messages.export}
          </Button>
        </CardAction>
      </CardHeader>

      <CardContent className="pt-0">
        <RecentCustomersTable data={customers} locale={locale} />
      </CardContent>
    </Card>
  );
}
