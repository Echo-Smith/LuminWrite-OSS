/**
 * BillingResult Page — 支付结果页
 *
 * 支付宝支付完成后通过 return_url 跳转到此页面。
 * 从 URL 参数中提取订单号，轮询后端订单状态，展示支付结果。
 */
import { useEffect, useState, useRef } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Check, X, Loader2, ArrowLeft } from "lucide-react";
import { useBillingStore } from "@/stores/billing-store";

type Status = "loading" | "success" | "pending" | "failed";

export default function BillingResultPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { checkOrderStatus } = useBillingStore();

  const [status, setStatus] = useState<Status>("loading");
  const [orderInfo, setOrderInfo] = useState<{
    order_type?: string;
    amount?: number;
    point_amount?: number;
    plan_id?: string;
    period?: string;
  } | null>(null);
  const [errorMsg, setErrorMsg] = useState("");
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // 从 URL 获取订单号（支付宝 return_url 会带 out_trade_no 参数）
  const orderID = searchParams.get("out_trade_no") || searchParams.get("order_id") || "";

  useEffect(() => {
    if (!orderID) {
      setStatus("failed");
      setErrorMsg("缺少订单号参数");
      return;
    }

    let attempts = 0;
    const maxAttempts = 30; // 最多轮询 30 次（约 60 秒）

    const poll = async () => {
      attempts++;
      try {
        const order = await checkOrderStatus(orderID);

        if (order.status === "paid") {
          setOrderInfo({
            order_type: order.order_type,
            amount: order.amount,
            point_amount: order.point_amount,
            plan_id: order.plan_id,
            period: order.period,
          });
          setStatus("success");
          if (timerRef.current) {
            clearInterval(timerRef.current);
            timerRef.current = null;
          }
          return;
        }

        if (order.status === "failed" || order.status === "expired") {
          setStatus("failed");
          setErrorMsg(order.status === "expired" ? "订单已过期" : "支付失败");
          if (timerRef.current) {
            clearInterval(timerRef.current);
            timerRef.current = null;
          }
          return;
        }

        // pending — 继续轮询
        if (attempts >= maxAttempts) {
          setStatus("pending");
          if (timerRef.current) {
            clearInterval(timerRef.current);
            timerRef.current = null;
          }
        }
      } catch {
        if (attempts >= maxAttempts) {
          setStatus("failed");
          setErrorMsg("查询订单状态失败，请稍后在积分管理中查看");
          if (timerRef.current) {
            clearInterval(timerRef.current);
            timerRef.current = null;
          }
        }
      }
    };

    // 立即查询一次
    poll();
    // 之后每 2 秒轮询
    timerRef.current = setInterval(poll, 2000);

    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [orderID, checkOrderStatus]);

  const formatAmount = (amount?: number) => {
    if (amount == null) return "";
    return `¥${amount.toFixed(2)}`;
  };

  const getOrderTypeLabel = (type?: string) => {
    switch (type) {
      case "subscription": return "套餐订阅";
      case "upgrade": return "套餐升级";
      case "recharge": return "积分充值";
      default: return "订单";
    }
  };

  return (
    <div className="min-h-screen bg-gradient-to-b from-slate-50 to-slate-100 dark:from-slate-950 dark:to-slate-900 flex items-center justify-center px-4">
      <div className="max-w-md w-full bg-white dark:bg-slate-800 rounded-2xl shadow-lg p-8 text-center">
        {/* Loading */}
        {status === "loading" && (
          <>
            <div className="flex justify-center mb-6">
              <Loader2 className="w-16 h-16 text-indigo-500 animate-spin" />
            </div>
            <h2 className="text-xl font-semibold mb-2">正在确认支付结果...</h2>
            <p className="text-sm text-muted-foreground">
              订单号：{orderID.slice(0, 8)}...
              <br />
              请稍候，正在等待支付宝回调确认
            </p>
          </>
        )}

        {/* Success */}
        {status === "success" && (
          <>
            <div className="flex justify-center mb-6">
              <div className="w-16 h-16 rounded-full bg-emerald-100 dark:bg-emerald-900 flex items-center justify-center">
                <Check className="w-8 h-8 text-emerald-600 dark:text-emerald-400" />
              </div>
            </div>
            <h2 className="text-xl font-semibold mb-2 text-emerald-600 dark:text-emerald-400">
              支付成功
            </h2>
            <p className="text-sm text-muted-foreground mb-4">
              {getOrderTypeLabel(orderInfo?.order_type)}已完成
            </p>
            {orderInfo?.amount != null && (
              <p className="text-lg font-medium mb-6">
                {formatAmount(orderInfo.amount)}
              </p>
            )}
            {orderInfo?.order_type === "recharge" && orderInfo?.point_amount != null && (
              <p className="text-sm text-muted-foreground mb-6">
                已充入 {Math.floor(orderInfo.point_amount)} 积分
              </p>
            )}
            <button
              onClick={() => navigate("/profile")}
              className="w-full py-3 rounded-lg bg-indigo-500 hover:bg-indigo-600 text-white font-medium transition-colors"
            >
              查看积分管理
            </button>
          </>
        )}

        {/* Pending (timeout but not confirmed) */}
        {status === "pending" && (
          <>
            <div className="flex justify-center mb-6">
              <Loader2 className="w-16 h-16 text-amber-500" />
            </div>
            <h2 className="text-xl font-semibold mb-2 text-amber-600">
              支付结果确认中
            </h2>
            <p className="text-sm text-muted-foreground mb-6">
              您的支付正在处理中，积分将在确认后到账。
              <br />
              如果长时间未到账，请联系客服。
            </p>
            <button
              onClick={() => navigate("/profile")}
              className="w-full py-3 rounded-lg bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 font-medium transition-colors"
            >
              返回个人中心
            </button>
          </>
        )}

        {/* Failed */}
        {status === "failed" && (
          <>
            <div className="flex justify-center mb-6">
              <div className="w-16 h-16 rounded-full bg-red-100 dark:bg-red-900 flex items-center justify-center">
                <X className="w-8 h-8 text-red-600 dark:text-red-400" />
              </div>
            </div>
            <h2 className="text-xl font-semibold mb-2 text-red-600 dark:text-red-400">
              支付未完成
            </h2>
            <p className="text-sm text-muted-foreground mb-6">
              {errorMsg || "支付未成功或已取消"}
            </p>
            <button
              onClick={() => navigate("/pricing")}
              className="w-full py-3 rounded-lg bg-indigo-500 hover:bg-indigo-600 text-white font-medium transition-colors mb-2"
            >
              重新选择套餐
            </button>
            <button
              onClick={() => navigate("/profile")}
              className="w-full py-3 rounded-lg bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 font-medium transition-colors"
            >
              返回个人中心
            </button>
          </>
        )}

        {/* Back link */}
        <div className="mt-8">
          <button
            onClick={() => navigate("/")}
            className="text-sm text-muted-foreground hover:text-foreground transition-colors inline-flex items-center gap-1"
          >
            <ArrowLeft className="w-3 h-3" />
            返回首页
          </button>
        </div>
      </div>
    </div>
  );
}
