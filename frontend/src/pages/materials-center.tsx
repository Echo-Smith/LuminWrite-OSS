/**
 * 知识库（原素材库）— 独立访问域
 *
 * 与选题中心（/topics）拆分：本页面承载素材库的全部能力，
 * 由侧边栏「知识库」入口直达。
 */
import { useNavigate } from "react-router-dom";
import { Database } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { MaterialsTab } from "@/components/topic/materials-tab";

export function MaterialsCenter() {
  const navigate = useNavigate();

  return (
    <div className="flex h-screen flex-col">
      <header className="flex items-center justify-between border-b px-6 py-3">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => navigate("/write")}>← 返回</Button>
          <Separator orientation="vertical" className="h-5" />
          <div className="flex items-center gap-1.5 px-1 py-1.5 text-sm font-medium">
            <Database className="h-4 w-4" />
            知识库
          </div>
        </div>
      </header>

      <div className="flex-1 overflow-y-auto">
        <MaterialsTab />
      </div>
    </div>
  );
}
