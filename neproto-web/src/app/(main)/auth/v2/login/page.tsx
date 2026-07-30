import { redirect } from "next/navigation";

import { Globe } from "lucide-react";

import { APP_CONFIG } from "@/config/app-config";
import { APP_MESSAGES } from "@/lib/i18n";
import { hasAdminSession } from "@/server/admin-auth";
import { getRequestLocale } from "@/server/request-locale";

import { LoginForm } from "../../_components/login-form";

export default async function LoginV2() {
  if (await hasAdminSession()) {
    redirect("/dashboard");
  }
  const locale = await getRequestLocale();
  const messages = APP_MESSAGES[locale].auth;
  return (
    <>
      <div className="mx-auto flex w-full flex-col justify-center space-y-8 sm:w-[350px]">
        <div className="space-y-2 text-center">
          <h1 className="font-medium text-3xl">{messages.title}</h1>
          <p className="text-muted-foreground text-sm">{messages.description}</p>
        </div>
        <LoginForm messages={messages} />
      </div>

      <div className="absolute bottom-5 flex w-full justify-between px-10">
        <div className="text-sm">{APP_CONFIG.copyright}</div>
        <div className="flex items-center gap-1 text-sm">
          <Globe className="size-4 text-muted-foreground" />
          {locale === "ru" ? "RU" : "EN"}
        </div>
      </div>
    </>
  );
}
