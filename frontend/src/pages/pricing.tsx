/**
 * Pricing Page — 定价套餐选择页
 *
 * 展示所有可用套餐，支持月付/年付切换。
 * 点击订阅后跳转支付宝支付页面。
 * 使用设计系统 hsl(var(--*)) 变量 + shadcn 组件，与主应用视觉统一。
 */
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Check, ArrowLeft, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useBillingStore, type SubscriptionPlan } from "@/stores/billing-store";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";

export default function PricingPage() {
  const navigate = useNavigate();
  const { plans, loadPlans, currentSubscription, loadSubscription, createAlipayPayment } = useBillingStore();
  const { user } = useAuthStore();
  const [period, setPeriod] = useState<"monthly" | "yearly">("monthly");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    loadPlans();
    if (user) {
      loadSubscription();
    }
  }, [loadPlans, loadSubscription, user]);

  const handleSubscribe = async (plan: SubscriptionPlan) => {
    if (!user) {
      navigate("/login?redirect=/pricing");
      return;
    }

    // 免费版不需要支付
    if (plan.name === "free" || plan.price_monthly === 0) {
      navigate("/personal-center/wallet");
      return;
    }

    setLoading(true);
    setError("");
    try {
      const result = await createAlipayPayment({
        order_type: "subscription",
        plan_id: plan.id,
        period,
      });
      // 跳转到支付宝支付页面
      window.location.href = result.payment_url;
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建支付订单失败");
      setLoading(false);
    }
  };

  const formatPrice = (price: number) => {
    return `¥${price.toFixed(1)}`;
  };

  const formatPoints = (points: number) => {
    if (points >= 10000) {
      return `${(points / 10000).toFixed(0)}万`;
    }
    return points.toLocaleString();
  };

  const features = [
    "全部写作功能",
    "多角色编辑模式",
    "写作记忆 & 风格学习",
    "素材库 & 事实核查",
  ];

  return (
    <div className="min-h-screen bg-background">
      <div className="container mx-auto px-4 py-12 max-w-5xl">
        {/* Header */}
        <div className="text-center mb-12">
          <h1 className="text-4xl font-bold tracking-tight mb-3">
            选择适合你的套餐
          </h1>
          <p className="text-lg text-muted-foreground">
            所有套餐功能完全相同，区别仅在积分额度
          </p>
        </div>

        {/* Period Toggle */}
        <div className="flex justify-center mb-10">
          <div className="inline-flex items-center gap-1 p-1 rounded-xl bg-muted">
            <button
              onClick={() => setPeriod("monthly")}
              className={cn(
                "px-6 py-2 rounded-lg text-sm font-medium transition-ui",
                period === "monthly"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground"
              )}
            >
              按月付费
            </button>
            <button
              onClick={() => setPeriod("yearly")}
              className={cn(
                "px-6 py-2 rounded-lg text-sm font-medium transition-ui flex items-center gap-2",
                period === "yearly"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground"
              )}
            >
              按年付费
              <span className="text-xs px-2 py-0.5 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 rounded-full">
                买10送2
              </span>
            </button>
          </div>
        </div>

        {/* Error Message */}
        {error && (
          <div className="max-w-md mx-auto mb-6 p-4 rounded-lg border border-destructive/30 bg-destructive/5 text-destructive text-sm text-center">
            {error}
          </div>
        )}

        {/* Plans Grid */}
        <div className="grid md:grid-cols-3 gap-6 max-w-4xl mx-auto">
          {plans.map((plan) => {
            const price = period === "yearly" ? plan.price_yearly : plan.price_monthly;
            const unit = period === "yearly" ? "/年" : "/月";
            const isCurrent = currentSubscription?.plan_id === plan.id;
            const isFree = plan.name === "free";

            return (
              <div
                key={plan.id}
                className={cn(
                  "relative rounded-xl border p-6 transition-ui",
                  plan.is_popular
                    ? "border-foreground/20 shadow-md bg-card"
                    : "border-border bg-card hover:border-foreground/15"
                )}
              >
                {plan.is_popular && (
                  <div className="absolute -top-3 left-1/2 -translate-x-1/2">
                    <span className="flex items-center gap-1 px-3 py-1 bg-foreground text-background text-xs font-medium rounded-full shadow-md">
                      <Sparkles className="h-3 w-3" />
                      最受欢迎
                    </span>
                  </div>
                )}

                {/* Plan Name */}
                <h3 className="text-xl font-semibold mb-1">{plan.display_name}</h3>
                <p className="text-sm text-muted-foreground mb-4">
                  {isFree ? "体验写作" : `每月 ${formatPoints(plan.point_quota)} 积分`}
                </p>

                {/* Price */}
                <div className="mb-6">
                  {isFree ? (
                    <div className="text-4xl font-bold">免费</div>
                  ) : (
                    <>
                      <div className="flex items-baseline gap-1">
                        <span className="text-4xl font-bold">{formatPrice(price)}</span>
                        <span className="text-sm text-muted-foreground">{unit}</span>
                      </div>
                      {period === "yearly" && (
                        <p className="text-xs text-emerald-600 dark:text-emerald-400 mt-1">
                          约合 {formatPrice(plan.price_yearly / 12)}/月，省 {formatPrice(plan.price_monthly * 12 - plan.price_yearly)}
                        </p>
                      )}
                    </>
                  )}
                </div>

                {/* Features */}
                <ul className="space-y-2 mb-6 text-sm">
                  <li className="flex items-center gap-2">
                    <Check className="h-4 w-4 text-emerald-500 shrink-0" />
                    <span>每月 {formatPoints(plan.point_quota)} 积分</span>
                  </li>
                  {features.map((f) => (
                    <li key={f} className="flex items-center gap-2">
                      <Check className="h-4 w-4 text-emerald-500 shrink-0" />
                      <span>{f}</span>
                    </li>
                  ))}
                  {!isFree && (
                    <li className="flex items-center gap-2">
                      <Check className="h-4 w-4 text-emerald-500 shrink-0" />
                      <span>充值积分永久有效</span>
                    </li>
                  )}
                </ul>

                {/* CTA Button */}
                <Button
                  onClick={() => handleSubscribe(plan)}
                  disabled={loading || isCurrent}
                  variant={isCurrent ? "secondary" : plan.is_popular ? "default" : "outline"}
                  className="w-full"
                >
                  {loading ? "处理中…" : isCurrent ? "当前套餐" : isFree ? "开始使用" : "立即订阅"}
                </Button>
              </div>
            );
          })}
        </div>

        {/* FAQ Section */}
        <div className="max-w-2xl mx-auto mt-16">
          <h2 className="text-2xl font-bold text-center mb-8">常见问题</h2>
          <div className="space-y-3">
            {[
              { q: "积分是如何计算的？", a: "套餐积分每月重置（基于注册日），充值积分永久有效。扣减时先扣套餐积分，再扣充值积分。" },
              { q: "年付有什么优惠？", a: "年付按月分发积分（买10送2），相当于8.3折。积分每月到账，不是一次性给完。" },
              { q: "可以升级套餐吗？", a: "可以。升级时按剩余天数等比折算旧套餐价值抵扣新套餐价格，差价部分通过支付宝支付。" },
              { q: "搜索和核查怎么计费？", a: "搜索 5 积分/次，事实核查 10 积分/次，URL 抓取 2 积分/次。写作按 Token 计费。" },
            ].map((item) => (
              <div key={item.q} className="rounded-lg border border-border bg-card p-4">
                <h3 className="font-medium mb-1">{item.q}</h3>
                <p className="text-sm text-muted-foreground">{item.a}</p>
              </div>
            ))}
          </div>
        </div>

        {/* Back to home */}
        <div className="text-center mt-12">
          <button
            onClick={() => navigate("/write")}
            className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-ui"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            返回写作
          </button>
        </div>
      </div>
    </div>
  );
}
