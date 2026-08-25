/**
 * 积分管理子页面
 */
import { useState, useEffect, useCallback } from "react";
import {
  Wallet, TrendingUp, Clock, AlertCircle, Check, ChevronRight,
  Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useAuthStore } from "@/stores/auth-store";
import { useBillingStore } from "@/stores/billing-store";
import { cn } from "@/lib/utils";

export function WalletSection() {
  const isGuest = useAuthStore((s) => s.isGuest);
  const isGuestUser = isGuest();

  const balance = useBillingStore((s) => s.balance);
  const consumptionLogs = useBillingStore((s) => s.consumptionLogs);
  const consumptionTotal = useBillingStore((s) => s.consumptionTotal);
  const consumptionLoading = useBillingStore((s) => s.consumptionLoading);
  const plans = useBillingStore((s) => s.plans);
  const rechargeOrders = useBillingStore((s) => s.rechargeOrders);
  const loadBalance = useBillingStore((s) => s.loadBalance);
  const loadConsumption = useBillingStore((s) => s.loadConsumption);
  const loadConsumptionSummary = useBillingStore((s) => s.loadConsumptionSummary);
  const loadPlans = useBillingStore((s) => s.loadPlans);
  const loadRechargeOrders = useBillingStore((s) => s.loadRechargeOrders);
  const createAlipayPayment = useBillingStore((s) => s.createAlipayPayment);
  const redeem = useBillingStore((s) => s.redeem);

  const [loading, setLoading] = useState(false);
  const [rechargeAmount, setRechargeAmount] = useState(1000);
  const [selectedPlan, setSelectedPlan] = useState<string | null>(null);
  const [redeemCode, setRedeemCode] = useState("");
  const [redeemLoading, setRedeemLoading] = useState(false);
  const [redeemMsg, setRedeemMsg] = useState<{ type: "success" | "error"; text: string } | null>(null);

  // 消费明细子面板状态
  const [consumptionDays, setConsumptionDays] = useState(30);
  const [consumptionPage, setConsumptionPage] = useState(1);
  const [expandedLogId, setExpandedLogId] = useState<string | null>(null);
  const [summary, setSummary] = useState<{ total_consumed: number; by_category: Record<string, number> } | null>(null);
  const [summaryLoading, setSummaryLoading] = useState(false);

  // 消费明细刷新
  const refreshConsumption = useCallback((days: number, page: number, append = false) => {
    loadConsumption(days, page, 20, append);
    if (!append) {
      setSummaryLoading(true);
      loadConsumptionSummary(days)
        .then((data) => setSummary(data))
        .catch(() => {})
        .finally(() => setSummaryLoading(false));
    }
  }, [loadConsumption, loadConsumptionSummary]);

  useEffect(() => {
    if (isGuestUser) return;
    loadBalance();
    refreshConsumption(30, 1);
    loadPlans();
    loadRechargeOrders(5);
  }, [isGuestUser, loadBalance, refreshConsumption, loadPlans, loadRechargeOrders]);

  // 切换时间范围
  const handleDaysChange = (days: number) => {
    setConsumptionDays(days);
    setConsumptionPage(1);
    refreshConsumption(days, 1);
  };

  // 加载更多
  const handleLoadMore = () => {
    const nextPage = consumptionPage + 1;
    setConsumptionPage(nextPage);
    refreshConsumption(consumptionDays, nextPage, true);
  };

  // task_type 中文映射
  const taskTypeLabel = (taskType: string): string => {
    const labels: Record<string, string> = {
      writing: "写作",
      editing: "编辑",
      search: "搜索",
      analysis: "分析",
      planning: "规划",
      review: "审阅",
      fact_check: "事实核查",
      kb_search: "素材库检索",
      rewrite: "改写",
    };
    return labels[taskType] || taskType;
  };

  // 格式化时间
  const formatDateTime = (iso: string): string => {
    const d = new Date(iso);
    const now = new Date();
    const diffMs = now.getTime() - d.getTime();
    const diffMin = Math.floor(diffMs / 60_000);
    const diffHr = Math.floor(diffMs / 3_600_000);
    const diffDay = Math.floor(diffMs / 86_400_000);
    if (diffMin < 1) return "刚刚";
    if (diffMin < 60) return `${diffMin} 分钟前`;
    if (diffHr < 24) return `${diffHr} 小时前`;
    if (diffDay < 7) return `${diffDay} 天前`;
    return d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  };

  // 已加载的记录总数（用于判断是否还有更多）
  const loadedCount = consumptionLogs.length;
  const hasMore = loadedCount < consumptionTotal;

  const [paymentError, setPaymentError] = useState<string | null>(null);

  const handleRecharge = async () => {
    if (rechargeAmount < 500) {
      setPaymentError("最低充值 500 积分");
      return;
    }
    setLoading(true);
    setPaymentError(null);
    try {
      const result = await createAlipayPayment({
        order_type: "recharge",
        point_amount: rechargeAmount,
      });
      // 跳转到支付宝支付页面
      window.location.href = result.payment_url;
    } catch (e) {
      setPaymentError(e instanceof Error ? e.message : "创建支付订单失败");
      setLoading(false);
    }
  };

  const handleSubscribe = async (planId: string) => {
    setLoading(true);
    setPaymentError(null);
    try {
      const result = await createAlipayPayment({
        order_type: "subscription",
        plan_id: planId,
        period: "monthly",
      });
      window.location.href = result.payment_url;
    } catch (e) {
      setPaymentError(e instanceof Error ? e.message : "创建订阅订单失败");
      setLoading(false);
    }
  };

  const handleRedeem = async () => {
    if (!redeemCode.trim()) return;
    setRedeemLoading(true);
    setRedeemMsg(null);
    try {
      const result = await redeem(redeemCode.trim());
      setRedeemMsg({ type: "success", text: result.message || `兑换成功，获得 ${Math.floor(result.points)} 积分` });
      setRedeemCode("");
      loadRechargeOrders(5);
    } catch (e) {
      setRedeemMsg({ type: "error", text: e instanceof Error ? e.message : "兑换失败" });
    } finally {
      setRedeemLoading(false);
    }
  };

  if (isGuestUser) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-3">
        <div className="flex items-center gap-3 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/20 px-3 py-2.5">
          <AlertCircle className="h-4 w-4 text-amber-600" />
          <div>
            <p className="text-sm font-medium text-amber-700 dark:text-amber-400">注册账号后可管理积分</p>
            <p className="text-xs text-amber-600/70 dark:text-amber-500/70">充值积分、订阅套餐和查看消费记录</p>
          </div>
        </div>
      </div>
    );
  }

  const fmt = (n: number) => n.toFixed(1);

  return (
    <div className="flex h-full flex-col">
      <Tabs defaultValue="overview" className="flex-1 flex flex-col overflow-hidden">
        <div className="px-6 pt-4 pb-2 shrink-0">
          <TabsList className="w-full">
            <TabsTrigger value="overview" className="flex-1">
              <Wallet className="h-3.5 w-3.5 mr-1" />
              概览
            </TabsTrigger>
            <TabsTrigger value="usage" className="flex-1">
              <TrendingUp className="h-3.5 w-3.5 mr-1" />
              使用明细
            </TabsTrigger>
            <TabsTrigger value="recharge" className="flex-1">
              <Clock className="h-3.5 w-3.5 mr-1" />
              充值记录
            </TabsTrigger>
          </TabsList>
        </div>

        <ScrollArea className="flex-1">
          {/* ── 概览 Tab ── */}
          <TabsContent value="overview" className="px-6 pt-2 pb-12 space-y-4 m-0">
            {/* 余额卡片 */}
            <div className="rounded-xl border border-border bg-card p-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-xs text-muted-foreground">当前余额</span>
                <Badge variant="secondary" className="text-[10px]">
                  {balance?.plan_display_name || "免费版"}
                </Badge>
              </div>
              <div className="flex items-baseline gap-1">
                <span className="text-3xl font-bold tracking-tight">{balance ? Math.floor(balance.point_balance) : 0}</span>
                <span className="text-sm text-muted-foreground">积分</span>
              </div>
              {/* 双轨余额明细 */}
              <div className="flex items-center gap-4 mt-2 text-[10px] text-muted-foreground">
                <span>套餐积分 {balance ? Math.floor(balance.plan_balance) : 0}</span>
                <span>充值积分 {balance ? Math.floor(balance.paid_balance) : 0}</span>
              </div>
              {/* 套餐额度 & 重置时间 */}
              {balance && balance.plan_quota > 0 && (
                <div className="flex items-center gap-4 mt-1 text-[10px] text-muted-foreground/70">
                  <span>本月额度 {Math.floor(balance.plan_quota)}</span>
                  {balance.plan_reset_at && (
                    <span>重置时间 {new Date(balance.plan_reset_at).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })}</span>
                  )}
                </div>
              )}
              <div className="flex items-center gap-4 mt-2 text-[10px] text-muted-foreground">
                <span>累计充值 {balance ? Math.floor(balance.total_recharged) : 0} 积分</span>
                <span>累计消费 {balance ? fmt(balance.total_consumed) : "0.0"} 积分</span>
              </div>
            </div>

            {/* 兑换码 */}
            <div className="rounded-lg border border-border p-3 space-y-2">
              <div className="text-xs font-medium">兑换码</div>
              <div className="flex items-center gap-2">
                <Input
                  value={redeemCode}
                  onChange={(e) => setRedeemCode(e.target.value.toUpperCase())}
                  placeholder="输入兑换码，如 ABCD-EFGH-JKMN"
                  className="h-8 text-sm font-mono-sm"
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !redeemLoading && redeemCode.trim()) {
                      handleRedeem();
                    }
                  }}
                />
                <Button
                  size="sm"
                  onClick={handleRedeem}
                  disabled={redeemLoading || !redeemCode.trim()}
                  className="h-8 shrink-0"
                >
                  {redeemLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : "兑换"}
                </Button>
              </div>
              {redeemMsg && (
                <div className={cn(
                  "flex items-center gap-1.5 text-xs",
                  redeemMsg.type === "success" ? "text-green-600" : "text-red-600"
                )}>
                  {redeemMsg.type === "success" ? <Check className="h-3.5 w-3.5" /> : <AlertCircle className="h-3.5 w-3.5" />}
                  {redeemMsg.text}
                </div>
              )}
            </div>

            {/* 快速充值 */}
            <div className="rounded-lg border border-border p-3 space-y-2">
              <div className="text-xs font-medium">快速充值</div>
              <div className="flex items-center gap-2 flex-wrap">
                {[500, 1000, 3000, 5000].map((amt) => (
                  <button
                    key={amt}
                    onClick={() => setRechargeAmount(amt)}
                    className={cn(
                      "rounded-lg border px-2.5 py-1 text-xs transition-ui",
                      rechargeAmount === amt
                        ? "border-foreground bg-foreground text-background"
                        : "border-border/60 text-muted-foreground hover:bg-accent"
                    )}
                  >
                    {amt} 积分
                  </button>
                ))}
              </div>
              {paymentError && (
                <div className="text-[10px] text-red-500 mb-1">{paymentError}</div>
              )}
              <div className="flex items-center gap-2">
                <span className="text-[10px] text-muted-foreground">¥{(rechargeAmount * 0.01).toFixed(2)}</span>
                <Button
                  size="sm"
                  onClick={handleRecharge}
                  disabled={loading || rechargeAmount < 500}
                  className="ml-auto h-7 text-xs"
                >
                  {loading ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
                  确认充值
                </Button>
              </div>
            </div>

            {/* 套餐列表 */}
            {plans.length > 0 && (
              <div className="space-y-2">
                <div className="text-xs font-medium">订阅套餐</div>
                <div className="grid grid-cols-2 gap-2">
                  {plans.map((plan) => (
                    <div
                      key={plan.id}
                      className={cn(
                        "rounded-lg border p-2.5 space-y-1 cursor-pointer transition-ui",
                        plan.is_popular ? "border-foreground/20 ring-1 ring-foreground/10" : "border-border",
                        selectedPlan === plan.id && "border-foreground ring-1 ring-foreground/20"
                      )}
                      onClick={() => setSelectedPlan(plan.id)}
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-xs font-medium">{plan.display_name}</span>
                        {plan.is_popular && <Badge variant="default" className="text-[9px] h-4">推荐</Badge>}
                      </div>
                      <div className="flex items-baseline gap-0.5">
                        <span className="text-lg font-bold">¥{plan.price_monthly}</span>
                        <span className="text-[10px] text-muted-foreground">/月</span>
                        {plan.price_yearly > 0 && (
                          <span className="text-[10px] text-muted-foreground ml-1">¥{plan.price_yearly}/年</span>
                        )}
                      </div>
                      <div className="text-[10px] text-muted-foreground">{Math.floor(plan.point_quota)} 积分/月</div>
                    </div>
                  ))}
                </div>
                {selectedPlan && (
                  <Button
                    size="sm"
                    onClick={() => handleSubscribe(selectedPlan)}
                    disabled={loading}
                    className="w-full"
                  >
                    {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin mr-1" /> : null}
                    确认订阅
                  </Button>
                )}
              </div>
            )}

            {/* 积分说明 */}
            <div className="text-[10px] text-muted-foreground space-y-1.5 pt-2">
              <p>• 套餐积分每月重置（基于注册日），充值积分永久有效</p>
              <p>• 扣减优先级：先扣套餐积分，再扣充值积分</p>
              <p>• 搜索 5 积分/次，核查 10 积分/次，URL 抓取 2 积分/次</p>
              <p>• 充值积分 1 积分 = ¥0.01，500 积分起充</p>
            </div>
          </TabsContent>

          {/* ── 使用明细 Tab ── */}
          <TabsContent value="usage" className="px-6 pt-2 pb-12 space-y-3 m-0">
            {/* 时间筛选 */}
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium">消费记录</span>
              <div className="flex items-center gap-1">
                {[7, 30, 90].map((d) => (
                  <button
                    key={d}
                    onClick={() => handleDaysChange(d)}
                    className={cn(
                      "rounded-lg px-1.5 py-0.5 text-[10px] transition-ui",
                      consumptionDays === d
                        ? "bg-foreground text-background"
                        : "text-muted-foreground hover:bg-accent"
                    )}
                  >
                    {d}天
                  </button>
                ))}
              </div>
            </div>

            {/* 汇总卡片 */}
            <div className="rounded-lg border border-border bg-card p-2.5">
              {summaryLoading ? (
                <div className="flex items-center justify-center py-1.5">
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />
                </div>
              ) : summary ? (
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between">
                    <span className="text-[10px] text-muted-foreground">近 {consumptionDays} 天总消耗</span>
                    <span className="text-sm font-semibold text-foreground">
                      {fmt(summary.total_consumed)} 积分
                    </span>
                  </div>
                  {Object.keys(summary.by_category).length > 0 && (
                    <div className="flex flex-wrap gap-1.5 pt-0.5">
                      {Object.entries(summary.by_category)
                        .sort((a, b) => b[1] - a[1])
                        .map(([cat, pts]) => (
                          <Badge key={cat} variant="outline" className="text-[10px] h-4 gap-0.5">
                            {taskTypeLabel(cat)} {fmt(pts)}
                          </Badge>
                        ))}
                    </div>
                  )}
                </div>
              ) : (
                <div className="text-[10px] text-muted-foreground/60 text-center py-1">暂无汇总数据</div>
              )}
            </div>

            {/* 明细列表 */}
            {consumptionLoading && consumptionLogs.length === 0 ? (
              <div className="flex items-center justify-center py-6">
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
              </div>
            ) : consumptionLogs.length === 0 ? (
              <div className="text-[10px] text-muted-foreground/60 text-center py-4">暂无消费记录</div>
            ) : (
              <div className="space-y-1">
                {consumptionLogs.map((log) => {
                  const isExpanded = expandedLogId === log.id;
                  return (
                    <div
                      key={log.id}
                      className={cn(
                        "rounded-lg border transition-ui overflow-hidden",
                        isExpanded ? "border-border bg-accent/30" : "border-border/40"
                      )}
                    >
                      {/* 摘要行 */}
                      <button
                        onClick={() => setExpandedLogId(isExpanded ? null : log.id)}
                        className="w-full flex items-center justify-between px-2.5 py-1.5 text-xs"
                      >
                        <div className="flex items-center gap-1.5 min-w-0">
                          <ChevronRight
                            className={cn(
                              "h-3 w-3 text-muted-foreground/50 shrink-0 transition-transform",
                              isExpanded && "rotate-90"
                            )}
                          />
                          <span className="font-medium truncate">{taskTypeLabel(log.task_type)}</span>
                          {log.model_name && (
                            <span className="text-[10px] text-muted-foreground/50 truncate hidden sm:inline">{log.model_name}</span>
                          )}
                        </div>
                        <div className="flex items-center gap-2 shrink-0">
                          <span className="text-muted-foreground/60 text-[10px]">{formatDateTime(log.created_at)}</span>
                          <span className="text-muted-foreground font-medium">-{fmt(log.points_used)}</span>
                        </div>
                      </button>
                      {/* 展开详情 */}
                      {isExpanded && (
                        <div className="px-2.5 pb-2 pt-0.5 space-y-1 border-t border-border/30">
                          <div className="grid grid-cols-2 gap-x-3 gap-y-0.5 text-[10px]">
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">输入 Token</span>
                              <span className="font-mono-sm">{log.prompt_tokens.toLocaleString()}</span>
                            </div>
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">输出 Token</span>
                              <span className="font-mono-sm">{log.completion_tokens.toLocaleString()}</span>
                            </div>
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">输入费率</span>
                              <span className="font-mono-sm">{log.input_rate.toFixed(4)}</span>
                            </div>
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">输出费率</span>
                              <span className="font-mono-sm">{log.output_rate.toFixed(4)}</span>
                            </div>
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">扣减前余额</span>
                              <span className="font-mono-sm">{fmt(log.balance_before)}</span>
                            </div>
                            <div className="flex items-center justify-between">
                              <span className="text-muted-foreground">扣减后余额</span>
                              <span className="font-mono-sm">{fmt(log.balance_after)}</span>
                            </div>
                          </div>
                          {log.trace_id && (
                            <div className="flex items-center gap-1 pt-0.5 text-[10px] text-muted-foreground/40">
                              <span className="shrink-0">Trace:</span>
                              <span className="font-mono-sm truncate">{log.trace_id}</span>
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}

                {/* 加载更多 */}
                {hasMore && (
                  <button
                    onClick={handleLoadMore}
                    disabled={consumptionLoading}
                    className="w-full flex items-center justify-center gap-1 py-1.5 text-[10px] text-muted-foreground hover:text-foreground transition-ui"
                  >
                    {consumptionLoading ? (
                      <><Loader2 className="h-3 w-3 animate-spin" /> 加载中...</>
                    ) : (
                      <>加载更多（{loadedCount}/{consumptionTotal}）</>
                    )}
                  </button>
                )}
                {!hasMore && consumptionTotal > 0 && (
                  <div className="text-center text-[10px] text-muted-foreground/40 py-1">
                    已显示全部 {consumptionTotal} 条记录
                  </div>
                )}
              </div>
            )}
          </TabsContent>

          {/* ── 充值记录 Tab ── */}
          <TabsContent value="recharge" className="px-6 pt-2 pb-12 space-y-3 m-0">
            {rechargeOrders.length === 0 ? (
              <div className="text-[10px] text-muted-foreground/60 text-center py-8">暂无充值记录</div>
            ) : (
              <div className="space-y-1">
                {rechargeOrders.map((order) => (
                  <div key={order.id} className="rounded-lg border border-border/40 px-2.5 py-2 text-xs space-y-1">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <span className="text-emerald-600 dark:text-emerald-400 font-medium">+{Math.floor(order.point_amount)} 积分</span>
                        <span className="text-[10px] text-muted-foreground/60">¥{order.amount}</span>
                      </div>
                      <Badge variant={order.status === "paid" ? "default" : "outline"} className="text-[10px] h-4">
                        {order.status === "paid" ? "已支付" : order.status === "pending" ? "待支付" : order.status}
                      </Badge>
                    </div>
                    <div className="flex items-center gap-2 text-[10px] text-muted-foreground/50">
                      <Clock className="h-3 w-3" />
                      {new Date(order.created_at).toLocaleDateString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}
                      <span className="text-muted-foreground/40">·</span>
                      <span>{order.pay_channel === "manual" ? "手动充值" : order.pay_channel === "redeem_code" ? "兑换码" : order.pay_channel === "alipay" ? "支付宝" : order.pay_channel || order.payment_method}</span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </TabsContent>
        </ScrollArea>
      </Tabs>
    </div>
  );
}

