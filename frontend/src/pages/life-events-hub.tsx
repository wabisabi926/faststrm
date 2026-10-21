import { useEffect } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { SlidersHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import LifeEventsPage from "@/pages/life-events";

export default function LifeEventsHubPage() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();

  // 兼容旧书签：监控设置已迁移到设置中心
  useEffect(() => {
    if (searchParams.get("tab") === "config") {
      navigate("/settings?tab=monitor", { replace: true });
    }
  }, [searchParams, navigate]);

  return (
    <div className="mx-auto max-w-5xl space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">生活事件</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            115 生活事件处理日志；监控开关与路径映射在设置中心配置。
          </p>
        </div>
        <Button variant="outline" asChild>
          <Link to="/settings?tab=monitor">
            <SlidersHorizontal className="mr-1 h-4 w-4" />
            监控设置
          </Link>
        </Button>
      </div>

      <LifeEventsPage embedded />
    </div>
  );
}
