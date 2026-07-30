import { redirect } from "next/navigation";

import { Command } from "lucide-react";

import { APP_MESSAGES } from "@/lib/i18n";
import { hasAdminSession } from "@/server/admin-auth";
import { getRequestLocale } from "@/server/request-locale";

import { LoginForm } from "../../_components/login-form";

export default async function LoginV1() {
  if (await hasAdminSession()) {
    redirect("/dashboard");
  }
  const messages = APP_MESSAGES[await getRequestLocale()].auth;
  return (
    <div className="flex h-dvh">
      <div className="hidden bg-primary lg:block lg:w-1/3">
        <div className="flex h-full flex-col items-center justify-center p-12 text-center">
          <div className="space-y-6">
            <Command className="mx-auto size-12 text-primary-foreground" />
            <div className="space-y-2">
              <h1 className="font-light text-5xl text-primary-foreground">{messages.welcome}</h1>
              <p className="text-primary-foreground/80 text-xl">{messages.continue}</p>
            </div>
          </div>
        </div>
      </div>

      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <div className="font-medium tracking-tight">{messages.title}</div>
            <div className="mx-auto max-w-xl text-muted-foreground">{messages.description}</div>
          </div>
          <div className="space-y-4">
            <LoginForm messages={messages} />
          </div>
        </div>
      </div>
    </div>
  );
}
