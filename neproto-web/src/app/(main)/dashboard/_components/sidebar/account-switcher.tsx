"use client";

import { useRouter } from "next/navigation";

import { LogOut } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { AppLocale } from "@/lib/i18n";
import { getInitials } from "@/lib/utils";

export function AccountSwitcher({
  users,
  locale,
}: {
  readonly users: ReadonlyArray<{
    readonly id: string;
    readonly name: string;
    readonly email: string;
    readonly avatar: string;
    readonly role: string;
  }>;
  readonly locale: AppLocale;
}) {
  const router = useRouter();
  const activeUser = users[0];

  if (!activeUser) {
    return null;
  }

  async function logOut() {
    const response = await fetch("/api/auth/session", { method: "DELETE" });
    if (response.ok) {
      router.replace("/auth/v2/login");
      router.refresh();
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Avatar className="size-8 rounded-lg">
          <AvatarImage src={activeUser.avatar || undefined} alt={activeUser.name} />
          <AvatarFallback>{getInitials(activeUser.name)}</AvatarFallback>
        </Avatar>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="min-w-56 space-y-1 rounded-lg" side="bottom" align="end" sideOffset={4}>
        <div className="flex items-center gap-2 px-2 py-1.5">
          <Avatar className="size-9 rounded-lg">
            <AvatarImage src={activeUser.avatar || undefined} alt={activeUser.name} />
            <AvatarFallback>{getInitials(activeUser.name)}</AvatarFallback>
          </Avatar>
          <div className="grid min-w-0 flex-1 text-left text-sm leading-tight">
            <span className="truncate font-semibold">{activeUser.name}</span>
            <span className="truncate text-xs capitalize">{activeUser.role}</span>
          </div>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void logOut()}>
          <LogOut />
          {locale === "ru" ? "Выйти" : "Log out"}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
