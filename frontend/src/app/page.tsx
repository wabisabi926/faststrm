import { redirect } from "next/navigation";
export default function Home() {
  redirect("/task");
  return null;
}
