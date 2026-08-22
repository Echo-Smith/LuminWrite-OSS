import { useState, useEffect, useCallback } from "react";
import {
  RefreshCw, Bot, Wrench, ArrowRight, Lock, Shield, Layers,
  Network, Cpu, ChevronDown, ChevronRight,
} from "lucide-react";
import { adminFetch } from "@/lib/admin-api";
import { AdminPageHeader, AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// ─── Types ──────────────────────────────────────────────

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

// ─── Colors & Icons ─────────────────────────────────────

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

// ─── Component ───────────────────────────────────────────

export function AgentCardsPage() {
  const [data, setData] = useState<AgentCardsData | null>(null);
  const [loading, setLoading] = useState(true);
  const [expandedCard, setExpandedCard] = useState<string | null>(null);

  const loadData = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<AgentCardsData>("/api/v2/agent-cards", { silent: true });
    if (success && data) {
      setData(data);
    } else {
      setData(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="A2A Agent Cards"
        description="Agent 能力发现协议 — 角色定义、交付物、技能、决策权限"
        action={<Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
        </Button>}
      />

      {loading ? <AdminLoading /> : !data ? (
        <AdminEmptyState icon={Bot} title="无法加载 Agent Cards" description="请检查后端服务状态" />
      ) : (
        <>
          {/* Overview */}
          <div className="flex items-center gap-3 text-sm text-muted-foreground">
            <Badge variant="outline" className="bg-indigo-50 text-indigo-700 border-indigo-200">
              {data.protocol}
            </Badge>
            <span>{data.total_agents} 个 Agent 角色</span>
          </div>

          {/* Agent Cards */}
          <div className="space-y-4">
            {data.cards.map((card, i) => {
              const Icon = ROLE_ICONS[card.role] ?? Bot;
              const isExpanded = expandedCard === card.role;
              const roleColor = ROLE_COLORS[card.role] ?? "bg-muted text-muted-foreground border-border";

              return (
                <Card key={i} className="overflow-hidden">
                  {/* Header */}
                  <button
                    onClick={() => setExpandedCard(isExpanded ? null : card.role)}
                    className="flex w-full items-center gap-3 p-4 text-left hover:bg-muted/30 transition-colors"
                  >
                    <div className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg ${roleColor}`}>
                      <Icon className="h-5 w-5" />
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <h3 className="text-sm font-semibold">{card.name}</h3>
                        <Badge variant="outline" className={`text-[10px] ${roleColor}`}>
                          {card.role}
                        </Badge>
                        {card.requires_isolation && (
                          <Badge variant="outline" className="text-[10px] bg-red-50 text-red-700 border-red-200">
                            <Lock className="h-2.5 w-2.5 mr-1" /> 隔离
                          </Badge>
                        )}
                        <Badge variant="outline" className="text-[10px]">
                          v{card.version}
                        </Badge>
                      </div>
                      <p className="text-xs text-muted-foreground mt-0.5 line-clamp-1">{card.description}</p>
                    </div>
                    <div className="flex items-center gap-2 shrink-0">
                      <span className="text-xs text-muted-foreground hidden sm:inline">
                        {card.skills.length} 技能 · {card.capabilities.produces.length} 产出
                      </span>
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4 text-muted-foreground" />
                      ) : (
                        <ChevronRight className="h-4 w-4 text-muted-foreground" />
                      )}
                    </div>
                  </button>

                  {/* Expanded Content */}
                  {isExpanded && (
                    <div className="border-t px-4 py-3 space-y-4 bg-muted/10">
                      {/* Description */}
                      <div>
                        <p className="text-xs text-muted-foreground mb-1">描述</p>
                        <p className="text-sm">{card.description}</p>
                      </div>

                      {/* Persona */}
                      {card.persona && (
                        <div>
                          <p className="text-xs text-muted-foreground mb-1">角色设定 (Persona)</p>
                          <p className="text-xs text-muted-foreground bg-muted/50 rounded p-2 italic line-clamp-3">
                            {card.persona}
                          </p>
                        </div>
                      )}

                      {/* Capabilities */}
                      <div className="grid gap-3 md:grid-cols-3">
                        {/* Produces */}
                        <div>
                          <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1">
                            <ArrowRight className="h-3 w-3 text-emerald-500" /> 产出交付物
                          </p>
                          <div className="flex flex-wrap gap-1">
                            {card.capabilities.produces.map((p, j) => (
                              <Badge key={j} variant="outline" className="text-[10px] bg-emerald-50 text-emerald-700 border-emerald-200">
                                {data.artifact_labels[p] ?? p}
                              </Badge>
                            ))}
                          </div>
                        </div>

                        {/* Consumes */}
                        <div>
                          <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1">
                            <Layers className="h-3 w-3 text-blue-500" /> 消费交付物
                          </p>
                          <div className="flex flex-wrap gap-1">
                            {card.capabilities.consumes.map((c, j) => (
                              <Badge key={j} variant="outline" className="text-[10px] bg-blue-50 text-blue-700 border-blue-200">
                                {data.artifact_labels[c] ?? c}
                              </Badge>
                            ))}
                          </div>
                        </div>

                        {/* Decisions */}
                        <div>
                          <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1">
                            <Shield className="h-3 w-3 text-amber-500" /> 决策权限
                          </p>
                          <div className="flex flex-wrap gap-1">
                            {card.capabilities.decisions.length === 0 ? (
                              <span className="text-xs text-muted-foreground">无</span>
                            ) : (
                              card.capabilities.decisions.map((d, j) => (
                                <Badge key={j} variant="outline" className="text-[10px] bg-amber-50 text-amber-700 border-amber-200">
                                  {data.decision_labels[d] ?? d}
                                </Badge>
                              ))
                            )}
                          </div>
                        </div>
                      </div>

                      {/* Skills */}
                      <div>
                        <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1">
                          <Wrench className="h-3 w-3 text-purple-500" /> 技能 ({card.skills.length})
                        </p>
                        <div className="grid gap-1.5 sm:grid-cols-2">
                          {card.skills.map((skill, j) => (
                            <div key={j} className="flex items-start gap-2 text-xs rounded border border-border/50 px-2 py-1.5 bg-card">
                              <Badge variant="outline" className="text-[10px] font-mono shrink-0">
                                {skill.name}
                              </Badge>
                              <span className="text-muted-foreground">{skill.description}</span>
                            </div>
                          ))}
                        </div>
                      </div>
                    </div>
                  )}
                </Card>
              );
            })}
          </div>
        </>
      )}
    </div>
  );
}
