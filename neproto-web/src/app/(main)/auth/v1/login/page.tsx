import { redirect } from "next/navigation";

export default async function LoginV1() {
  redirect("/auth/v2/login");
}
