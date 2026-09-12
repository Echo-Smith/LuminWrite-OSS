/**
 * AgentArchitectureCard — 多 Agent 架构说明区块（概览页）
 *
 * 数据来自公开的 A2A 能力发现端点 /api/v2/agent-cards，
 * 由 editorial.AgentRegistry / BuiltinRoles 实时投影，只读展示；
 * 加载失败时静默隐藏，不影响概览页其它内容。
 */
import { useState, useEffect, useCallback } from "react";
import { Bot, Cpu, Network, Wrench, Shield } from "lucide-react";
import { adminFetch } from "@/lib/admin-api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

interface AgentSkill {
  name: string;
  description: string;
}

interface AgentCapabilities {
  produces: string[];
  consumes: string[];
  decisions: string[];
}

interface AgentCard {
  name: string;
  role: string;
  description: string;
  version: string;
  capabilities: AgentCapabilities;
  skills: AgentSkill[];
  requires_isolation: boolean;
  persona?: string;
  status: string;
}

interface AgentCardsData {
  cards: AgentCard[];
  artifact_labels: Record<string, string>;
  decision_labels: Record<string, string>;
  protocol: string;
  total_agents: number;
}

const ROLE_COLORS: Record<string, string> = {
  orchestrator: "bg-indigo-50 text-indigo-700 border-indigo-200",
  research_agent: "bg-blue-50 text-blue-700 border-blue-200",
  writing_agent: "bg-emerald-50 text-emerald-700 border-emerald-200",
  review_agent: "bg-amber-50 text-amber-700 border-amber-200",
};

const ROLE_ICONS: Record<string, typeof Bot> = {
  orchestrator: Cpu,
  research_agent: Network,
  writing_agent: Wrench,
  review_agent: Shield,
};

export function AgentArchitectureCard() {
  const [data, setData] = useState<AgentCardsData | null>(null);

  const loadData = useCallback(async () => {
    const { success, data } = await adminFetch<AgentCardsData>("/api/v2/agent-cards", { silent: true });
    if (success && data) setData(data);
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  if (!data) return null;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm flex items-center gap-2">
          <Bot className="h-4 w-4" />
          多 Agent 架构
          <Badge variant="outline" className="text-[10px] bg-indigo-50 text-indigo-700 border-indigo-200">
            {data.protocol}
          </Badge>
          <span className="text-xs font-normal text-muted-foreground">
            {data.total_agents} 个 Agent 角色 · A2A 能力发现
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-3 md:grid-cols-2">
        {data.cards.map((card) => {
          const Icon = ROLE_ICONS[card.role] ?? Bot;
          const roleColor = ROLE_COLORS[card.role] ?? "bg-muted text-muted-foreground border-border";
          return (
            <div key={card.role} className="rounded-lg border p-3">
              <div className="flex items-center gap-2">
                <div className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-md border ${roleColor}`}>
                  <Icon className="h-3.5 w-3.5" />
                </div>
                <span className="text-sm font-medium truncate">{card.name}</span>
                <Badge variant="outline" className={`text-[10px] shrink-0 ${roleColor}`}>
                  {card.role}
                </Badge>
                {card.requires_isolation && (
                  <Badge variant="outline" className="text-[10px] bg-red-50 text-red-700 border-red-200 shrink-0">
                    隔离
                  </Badge>
                )}
              </div>
              <p className="text-xs text-muted-foreground mt-1.5 line-clamp-2">{card.description}</p>
              <div className="flex flex-wrap gap-x-3 gap-y-1 mt-2 text-[10px] text-muted-foreground">
                <span className="flex items-center gap-1 flex-wrap">
                  产出：
                  {card.capabilities.produces.map((p) => (
                    <Badge key={p} variant="outline" className="text-[10px] bg-emerald-50 text-emerald-700 border-emerald-200">
                      {data.artifact_labels[p] ?? p}
                    </Badge>
                  ))}
                </span>
                <span className="flex items-center gap-1 flex-wrap">
                  决策：
                  {card.capabilities.decisions.length === 0 ? (
                    <span>无</span>
                  ) : (
                    card.capabilities.decisions.map((d) => (
                      <Badge key={d} variant="outline" className="text-[10px] bg-amber-50 text-amber-700 border-amber-200">
                        {data.decision_labels[d] ?? d}
                      </Badge>
                    ))
                  )}
                </span>
                <span>技能 {card.skills.length} 项</span>
              </div>
            </div>
          );
        })}
      </CardContent>
    </Card>
  );
}
