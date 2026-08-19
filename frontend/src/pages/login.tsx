import { useForm } from "react-hook-form";
import { useNavigate } from "react-router-dom";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Form, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import axiosInstance, { setToken, setUsername } from "@/lib/axios";

interface LoginForm {
  username: string;
  password: string;
}

export default function LoginPage() {
  const navigate = useNavigate();
  const form = useForm<LoginForm>({
    defaultValues: { username: "", password: "" },
  });

  const onSubmit = async (values: LoginForm) => {
    try {
      const response = await axiosInstance.post("/api/auth/login", values);
      const { token, user } = response.data;
      
      setToken(token);
      if (user?.username) {
        setUsername(user.username);
      }
      
      navigate("/");
    } catch {
      alert("登录失败，请检查用户名或密码");
    }
  };

  return (
    <div className="flex h-screen items-center justify-center bg-background">
      <div className="w-full max-w-md bg-card text-card-foreground p-8 rounded-2xl shadow-lg border border-border">
        <div className="flex flex-col items-center mb-6">
          <img
            src="/logo.png"
            alt="Fast Strm Logo"
            width={64}
            height={64}
            className="mb-4"
          />
          <h1 className="text-2xl font-bold text-center">登录</h1>
          <p className="text-sm text-muted-foreground mt-1">Fast Strm 管理系统</p>
        </div>

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
            <FormField
              control={form.control}
              name="username"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>用户名</FormLabel>
                  <Input placeholder="请输入用户名（默认：admin）" {...field} />
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name="password"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>密码</FormLabel>
                  <Input type="password" placeholder="请输入密码（默认：admin）" {...field} />
                  <FormMessage />
                </FormItem>
              )}
            />

            <Button type="submit" className="w-full mt-2">
              登录
            </Button>
          </form>
        </Form>
      </div>
    </div>
  );
}
