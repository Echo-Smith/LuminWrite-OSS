/**
 * 第三方服务 Section — 占位（待接入）
 *
 * 插件形态的第三方能力：搜索源、TTS/ASR、翻译等。当前为占位说明，
 * 接入后在此以卡片形式呈现配置入口。
 */
import { Plug2 } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";

export function ThirdPartySection() {
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        第三方能力以插件形式接入，配置后即可在写作流程中使用。
      </p>
      <Card className="border-dashed">
        <CardContent className="flex flex-col items-center justify-center py-12 text-center">
          <Plug2 className="h-10 w-10 text-muted-foreground/40" />
          <p className="mt-3 text-sm font-medium text-muted-foreground">暂无可接入的第三方服务</p>
          <p className="mt-1 max-w-sm text-xs text-muted-foreground/70">
            搜索源、语音合成、翻译等第三方能力正在接入中。
            如需使用搜索，可先在「服务密钥」中配置 SearXNG 或兼容的搜索 API。
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
