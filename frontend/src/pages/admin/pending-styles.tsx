/**
 * 社区风格审核 — Admin Dashboard
 * 审核用户提交的自定义风格
 */
import { useState, useEffect, useCallback } from "react";
import { Check, X, Loader2, Clock } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from "@/components/ui/dialog";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { AdminPageHeader } from "@/components/admin";

interface PendingReview {
  id: string;
  profile_id: string;
  profile_name: string;
  profile_slug: string;
  owner_user_id: string;
  version_number: number;
  version_config: string;
  status: string;
  created_at: string;
}

export function PendingStylesPage() {
  const [reviews, setReviews] = useState<PendingReview[]>([]);
  const [loading, setLoading] = useState(false);
  const [rejectDialog, setRejectDialog] = useState<PendingReview | null>(null);
  const [rejectNote, setRejectNote] = useState("");
  const [actioning, setActioning] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadReviews = useCallback(async () => {
    setLoading(true);
    setError(null);
    const { success, data, error: err } = await adminFetch<{ reviews: PendingReview[] }>("/api/v2/admin/pending-styles", { silent: true });
    if (success && data) {
      setReviews(data.reviews ?? []);
    } else {
      setError(err?.message ?? "加载失败");
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    loadReviews();
  }, [loadReviews]);

  const handleApprove = async (id: string) => {
    setActioning(id);
    const { success } = await adminMutate(`/api/v2/admin/pending-styles/${id}/approve`, {
      method: "POST",
      successTitle: "风格已通过",
      successDesc: "已发布为正式风格",
    });
    if (success) await loadReviews();
    setActioning(null);
  };

  const handleReject = async () => {
    if (!rejectDialog) return;
    setActioning(rejectDialog.id);
    const { success } = await adminMutate(`/api/v2/admin/pending-styles/${rejectDialog.id}/reject`, {
      method: "POST",
      body: JSON.stringify({ note: rejectNote }),
      successTitle: "风格已驳回",
      successDesc: rejectDialog.profile_name,
    });
    if (success) {
      setRejectDialog(null);
      setRejectNote("");
      await loadReviews();
    }
    setActioning(null);
  };

  const parseConfig = (configStr: string): Record<string, unknown> | null => {
    try {
      return JSON.parse(configStr);
    } catch {
      return null;
    }
  };

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="社区风格审核"
        description="审核用户提交的自定义风格"
        action={
          reviews.length > 0 ? (
            <Badge variant="secondary" className="gap-1.5">
              <Clock className="h-3 w-3" />
              {reviews.length} 待审核
            </Badge>
          ) : undefined
        }
      />

      {error && (
        <div className="rounded-md bg-destructive/10 px-4 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      {loading ? (
        <div className="flex justify-center py-12">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      ) : reviews.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-12">
            <p className="text-muted-foreground text-sm">暂无待审核的风格</p>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4">
          {reviews.map((review) => {
            const config = parseConfig(review.version_config);
            return (
              <Card key={review.id}>
                <CardHeader>
                  <div className="flex items-start justify-between">
                    <div className="space-y-1">
                      <CardTitle className="text-base">{review.profile_name}</CardTitle>
                      <div className="flex items-center gap-2 text-xs text-muted-foreground">
                        <span>slug: {review.profile_slug}</span>
                        <span>•</span>
                        <span>v{review.version_number}</span>
                        <span>•</span>
                        <span>用户: {review.owner_user_id.slice(0, 8)}...</span>
                        <span>•</span>
                        <span>{new Date(review.created_at).toLocaleDateString("zh-CN")}</span>
                      </div>
                    </div>
                    <div className="flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => handleApprove(review.id)}
                        disabled={actioning === review.id}
                        className="gap-1.5 text-green-600 hover:text-green-700"
                      >
                        {actioning === review.id ? (
                          <Loader2 className="h-3 w-3 animate-spin" />
                        ) : (
                          <Check className="h-3 w-3" />
                        )}
                        通过
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => {
                          setRejectDialog(review);
                          setRejectNote("");
                        }}
                        disabled={actioning === review.id}
                        className="gap-1.5 text-red-600 hover:text-red-700"
                      >
                        <X className="h-3 w-3" />
                        驳回
                      </Button>
                    </div>
                  </div>
                </CardHeader>
                <CardContent>
                  {config && (
                    <div className="space-y-3">
                      <div>
                        <p className="text-xs font-medium text-muted-foreground mb-1">描述</p>
                        <p className="text-sm">{config.description as string}</p>
                      </div>
                      <div className="grid grid-cols-2 gap-3 text-sm">
                        <div>
                          <span className="text-xs text-muted-foreground">字数范围：</span>
                          {(config.word_range as { min: number; max: number })?.min}-
                          {(config.word_range as { min: number; max: number })?.max}字
                        </div>
                        <div>
                          <span className="text-xs text-muted-foreground">结构类型：</span>
                          {(config.structure as { type: string })?.type}
                        </div>
                      </div>
                      <div>
                        <p className="text-xs font-medium text-muted-foreground mb-1">系统提示词</p>
                        <div className="bg-muted rounded-md p-3 text-xs font-mono max-h-40 overflow-y-auto">
                          {config.system_prompt as string}
                        </div>
                      </div>
                      {Array.isArray(config.tags) && (config.tags as string[]).length > 0 && (
                        <div className="flex items-center gap-1.5">
                          {(config.tags as string[]).map((tag) => (
                            <Badge key={tag} variant="secondary" className="text-xs">
                              {tag}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <Dialog open={!!rejectDialog} onOpenChange={(v) => !v && setRejectDialog(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>驳回风格</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <p className="text-sm text-muted-foreground">
              确定驳回「{rejectDialog?.profile_name}」吗？请填写驳回原因。
            </p>
            <Textarea
              value={rejectNote}
              onChange={(e) => setRejectNote(e.target.value)}
              placeholder="驳回原因..."
              rows={3}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRejectDialog(null)}>
              取消
            </Button>
            <Button variant="destructive" onClick={handleReject} disabled={!rejectNote.trim()}>
              确认驳回
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
