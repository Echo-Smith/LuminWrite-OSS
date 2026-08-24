/**
 * Pricing Page — 定价套餐选择页
 *
 * 展示所有可用套餐，支持月付/年付切换。
 * 点击订阅后跳转支付宝支付页面。
 */
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useBillingStore, type SubscriptionPlan } from "@/stores/billing-store";
import { useAuthStore } from "@/stores/auth-store";

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

  return (
    <div className="min-h-screen bg-gradient-to-b from-slate-50 to-slate-100 dark:from-slate-950 dark:to-slate-900">
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
          <div className="inline-flex items-center gap-1 p-1 bg-slate-200 dark:bg-slate-800 rounded-lg">
            <button
              onClick={() => setPeriod("monthly")}
              className={`px-6 py-2 rounded-md text-sm font-medium transition-all ${
                period === "monthly"
                  ? "bg-white dark:bg-slate-700 shadow-sm text-slate-900 dark:text-white"
                  : "text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
              }`}
            >
              按月付费
            </button>
            <button
              onClick={() => setPeriod("yearly")}
              className={`px-6 py-2 rounded-md text-sm font-medium transition-all flex items-center gap-2 ${
                period === "yearly"
                  ? "bg-white dark:bg-slate-700 shadow-sm text-slate-900 dark:text-white"
                  : "text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
              }`}
            >
              按年付费
              <span className="text-xs px-2 py-0.5 bg-emerald-100 text-emerald-700 dark:bg-emerald-900 dark:text-emerald-300 rounded-full">
                买10送2
              </span>
            </button>
          </div>
        </div>

        {/* Error Message */}
        {error && (
          <div className="max-w-md mx-auto mb-6 p-4 bg-red-50 dark:bg-red-950 border border-red-200 dark:border-red-800 rounded-lg text-red-600 dark:text-red-400 text-sm text-center">
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
                className={`relative rounded-2xl border-2 p-6 transition-all ${
                  plan.is_popular
                    ? "border-indigo-500 shadow-xl scale-105 bg-white dark:bg-slate-800"
                    : "border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800/50 hover:border-slate-300 dark:hover:border-slate-600"
                }`}
              >
                {plan.is_popular && (
                  <div className="absolute -top-3 left-1/2 -translate-x-1/2">
                    <span className="px-4 py-1 bg-indigo-500 text-white text-xs font-medium rounded-full shadow-lg">
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
                        <p className="text-xs text-emerald-600 mt-1">
                          约合 {formatPrice(plan.price_yearly / 12)}/月，省 {formatPrice(plan.price_monthly * 12 - plan.price_yearly)}
                        </p>
                      )}
                    </>
                  )}
                </div>

                {/* Features */}
                <ul className="space-y-2 mb-6 text-sm">
                  <li className="flex items-center gap-2">
                    <svg className="w-4 h-4 text-emerald-500" fill="currentColor" viewBox="0 0 20 20">
                      <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                    </svg>
                    <span>每月 {formatPoints(plan.point_quota)} 积分</span>
                  </li>
                  <li className="flex items-center gap-2">
                    <svg className="w-4 h-4 text-emerald-500" fill="currentColor" viewBox="0 0 20 20">
                      <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                    </svg>
                    <span>全部写作功能</span>
                  </li>
                  <li className="flex items-center gap-2">
                    <svg className="w-4 h-4 text-emerald-500" fill="currentColor" viewBox="0 0 20 20">
                      <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                    </svg>
                    <span>多角色编辑模式</span>
                  </li>
                  <li className="flex items-center gap-2">
                    <svg className="w-4 h-4 text-emerald-500" fill="currentColor" viewBox="0 0 20 20">
                      <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                    </svg>
                    <span>写作记忆 & 风格学习</span>
                  </li>
                  <li className="flex items-center gap-2">
                    <svg className="w-4 h-4 text-emerald-500" fill="currentColor" viewBox="0 0 20 20">
                      <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                    </svg>
                    <span>素材库 & 事实核查</span>
                  </li>
                  {!isFree && (
                    <li className="flex items-center gap-2">
                      <svg className="w-4 h-4 text-emerald-500" fill="currentColor" viewBox="0 0 20 20">
                        <path fillRule="evenodd" d="M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z" clipRule="evenodd" />
                      </svg>
                      <span>充值积分永久有效</span>
                    </li>
                  )}
                </ul>

                {/* CTA Button */}
                <button
                  onClick={() => handleSubscribe(plan)}
                  disabled={loading || isCurrent}
                  className={`w-full py-3 rounded-lg font-medium transition-all ${
                    isCurrent
                      ? "bg-slate-100 dark:bg-slate-700 text-slate-400 cursor-not-allowed"
                      : plan.is_popular
                      ? "bg-indigo-500 hover:bg-indigo-600 text-white shadow-lg shadow-indigo-500/30"
                      : "bg-slate-900 dark:bg-slate-100 hover:bg-slate-700 dark:hover:bg-slate-300 text-white dark:text-slate-900"
                  } ${loading ? "opacity-50 cursor-wait" : ""}`}
                >
                  {isCurrent ? "当前套餐" : isFree ? "开始使用" : "立即订阅"}
                </button>
              </div>
            );
          })}
        </div>

        {/* FAQ Section */}
        <div className="max-w-2xl mx-auto mt-16">
          <h2 className="text-2xl font-bold text-center mb-8">常见问题</h2>
          <div className="space-y-4">
            <div className="p-4 bg-white dark:bg-slate-800 rounded-lg">
              <h3 className="font-medium mb-1">积分是如何计算的？</h3>
              <p className="text-sm text-muted-foreground">
                套餐积分每月重置（基于注册日），充值积分永久有效。扣减时先扣套餐积分，再扣充值积分。
              </p>
            </div>
            <div className="p-4 bg-white dark:bg-slate-800 rounded-lg">
              <h3 className="font-medium mb-1">年付有什么优惠？</h3>
              <p className="text-sm text-muted-foreground">
                年付按月分发积分（买10送2），相当于8.3折。积分每月到账，不是一次性给完。
              </p>
            </div>
            <div className="p-4 bg-white dark:bg-slate-800 rounded-lg">
              <h3 className="font-medium mb-1">可以升级套餐吗？</h3>
              <p className="text-sm text-muted-foreground">
                可以。升级时按剩余天数等比折算旧套餐价值抵扣新套餐价格，差价部分通过支付宝支付。
              </p>
            </div>
            <div className="p-4 bg-white dark:bg-slate-800 rounded-lg">
              <h3 className="font-medium mb-1">搜索和核查怎么计费？</h3>
              <p className="text-sm text-muted-foreground">
                搜索 5 积分/次，事实核查 10 积分/次，URL 抓取 2 积分/次。写作按 Token 计费。
              </p>
            </div>
          </div>
        </div>

        {/* Back to home */}
        <div className="text-center mt-12">
          <button
            onClick={() => navigate("/")}
            className="text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            ← 返回首页
          </button>
        </div>
      </div>
    </div>
  );
}
