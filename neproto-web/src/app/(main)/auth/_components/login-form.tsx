"use client";

import { useRouter } from "next/navigation";

import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldContent, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

const formSchema = z.object({
  password: z.string().min(32),
  remember: z.boolean().optional(),
});

interface LoginMessages {
  password: string;
  remember: string;
  submit: string;
  submitting: string;
  failed: string;
}

export function LoginForm({ messages }: { readonly messages: LoginMessages }) {
  const router = useRouter();
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      password: "",
      remember: false,
    },
  });

  async function onSubmit(data: z.infer<typeof formSchema>) {
    try {
      const response = await fetch("/api/auth/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: data.password, remember: Boolean(data.remember) }),
      });
      if (!response.ok) {
        throw new Error("authentication failed");
      }
      router.replace("/dashboard");
      router.refresh();
    } catch {
      form.setError("password", { message: messages.failed });
    }
  }

  return (
    <form noValidate onSubmit={form.handleSubmit(onSubmit)} className="flex flex-col gap-4">
      <FieldGroup className="gap-4">
        <Controller
          control={form.control}
          name="password"
          render={({ field, fieldState }) => (
            <Field className="gap-1.5" data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor="login-password">{messages.password}</FieldLabel>
              <Input
                {...field}
                id="login-password"
                type="password"
                placeholder="••••••••"
                autoComplete="current-password"
                aria-invalid={fieldState.invalid}
              />
              {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
            </Field>
          )}
        />
        <Controller
          control={form.control}
          name="remember"
          render={({ field, fieldState }) => (
            <Field orientation="horizontal" data-invalid={fieldState.invalid}>
              <Checkbox
                id="login-remember"
                name={field.name}
                checked={field.value}
                onCheckedChange={(checked) => field.onChange(Boolean(checked))}
                aria-invalid={fieldState.invalid}
              />
              <FieldContent>
                <FieldLabel htmlFor="login-remember" className="font-normal">
                  {messages.remember}
                </FieldLabel>
                {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
              </FieldContent>
            </Field>
          )}
        />
      </FieldGroup>
      <Button className="w-full" type="submit" disabled={form.formState.isSubmitting}>
        {form.formState.isSubmitting ? messages.submitting : messages.submit}
      </Button>
    </form>
  );
}
