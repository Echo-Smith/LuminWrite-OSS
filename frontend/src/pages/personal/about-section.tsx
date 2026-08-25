/**
 * 关于笔润智谈子页面
 */
import { useState } from "react";
import {
  Info, Heart, BookOpen, Zap, ScrollText, Sparkles,
  Palette, Shield, Brain, ChevronRight, Mail, FileText, Github,
} from "lucide-react";
import { BrandIcon } from "@/components/brand-icon";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

interface ChangelogEntry {
  version: string;
  date: string;
  highlights: string[];
}

const CHANGELOG: ChangelogEntry[] = [
  {
    version: "0.1.2",
    date: "2026-08-23",
    highlights: [
      "修复 Passkey 注册后未正确绑定到当前登录用户的问题",
      "修复 Passkey 登录时无法找到凭据的问题",
      "Passkey 注册成功后不再倒计时，直接显示成功并可关闭",
      "「关于笔润智谈」页面改进：面向用户的产品描述、版本日志、核心能力卡片",
    ],
  },
  {
    version: "0.1.1",
    date: "2026-08-23",
    highlights: [
      "新增积分与额度系统，写作消耗透明可控",
      "新增兑换码功能，可兑换积分或会员时长",
      "新增素材文件夹管理，写作素材可分组整理",
      "新增设备管理页面，可查看多端登录状态",
      "新增「关于笔润智谈」页面，含产品介绍与版本日志",
      "写作风格支持版本管理，可查看历史版本并回退",
      "素材库支持批量导入，提升创作效率",
      "编辑部模式重构，选题/研究/写作/审校角色分工更清晰",
      "事实核查结果独立展示，存疑信息一目了然",
      "安全审计页面增强，支持安全事件可视化与趋势分析",
      "RBAC 权限管理完善，支持更细粒度的角色与权限配置",
      "登录体验优化，支持 Passkey 无密码登录",
      "前端动画与交互细节优化，整体体验更流畅",
    ],
  },
  {
    version: "0.1.0",
    date: "2026-08-22",
    highlights: [
      "笔润智谈正式上线，支持智能会话、流水线和编辑部三种写作模式",
      "内置素材库自动检索，写作时可引用精选文章",
      "支持自定义写作风格，可保存并复用个人风格偏好",
      "编辑部模式提供选题、研究、写作、审校多角色协作",
      "事实核查守护内容准确性，自动标注存疑信息",
      "记忆系统持续学习用户偏好，越用越懂你",
    ],
  },
];

export function AboutSection() {
  const appVersion = typeof __APP_VERSION__ !== "undefined" ? __APP_VERSION__ : "0.1.0";
  const [expandedChangelog, setExpandedChangelog] = useState(false);

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">
      {/* 品牌区域 */}
      <div className="flex flex-col items-center text-center py-6">
        <BrandIcon size="xl" />
        <h2 className="text-xl font-semibold tracking-tight">笔润智谈</h2>
        <p className="text-xs text-muted-foreground mt-1">你的私人 AI 写作伙伴</p>
        <Badge variant="secondary" className="mt-2 text-xs">v{appVersion}</Badge>
      </div>

      {/* 产品介绍 */}
      <div className="space-y-2">
        <h3 className="text-sm font-semibold">产品简介</h3>
        <p className="text-xs text-muted-foreground leading-relaxed">
          笔润智谈是一款面向内容创作者的 AI 写作助手。它理解你的写作风格，记住你的创作偏好，
          帮助你从选题构思到成稿审校，全程陪伴每一步创作。
          无论是日常随笔、深度文章还是系列内容，笔润智谈都能成为你得力的写作伙伴。
        </p>
      </div>

      {/* 核心能力 */}
      <div className="space-y-2">
        <h3 className="text-sm font-semibold">核心能力</h3>
        <div className="grid grid-cols-2 gap-2">
          {[
            { icon: Sparkles, title: "智能写作", desc: "从选题到成稿，AI 全程辅助" },
            { icon: Palette, title: "风格定制", desc: "学习并复用你的写作风格" },
            { icon: BookOpen, title: "素材库引用", desc: "精选文章随时引用" },
            { icon: Shield, title: "事实核查", desc: "自动校验关键信息，确保准确" },
            { icon: Brain, title: "创作记忆", desc: "记住你的偏好，越用越懂你" },
            { icon: Zap, title: "多种模式", desc: "会话、流水线、编辑部灵活切换" },
          ].map((f) => {
            const Icon = f.icon;
            return (
              <div key={f.title} className="rounded-lg border border-border/60 p-3 space-y-1">
                <Icon className="h-4 w-4 text-muted-foreground" />
                <p className="text-xs font-medium">{f.title}</p>
                <p className="text-[10px] text-muted-foreground">{f.desc}</p>
              </div>
            );
          })}
        </div>
      </div>

      {/* 版本日志 */}
      <div className="space-y-2">
        <div
          className="flex items-center justify-between cursor-pointer"
          onClick={() => setExpandedChangelog((v) => !v)}
        >
          <div className="flex items-center gap-2">
            <ScrollText className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-sm font-semibold">版本日志</h3>
          </div>
          <ChevronRight
            className={cn(
              "h-4 w-4 text-muted-foreground/50 transition-transform",
              expandedChangelog && "rotate-90",
            )}
          />
        </div>

        {expandedChangelog && (
          <div className="space-y-3 pt-1 max-h-60 overflow-y-auto">
            {CHANGELOG.map((entry) => (
              <div key={entry.version} className="rounded-lg border border-border/60 p-3 space-y-2">
                <div className="flex items-center gap-2">
                  <Badge variant="secondary" className="text-[10px]">v{entry.version}</Badge>
                  <span className="text-[10px] text-muted-foreground">{entry.date}</span>
                </div>
                <ul className="space-y-1">
                  {entry.highlights.map((item, i) => (
                    <li key={i} className="text-[11px] text-muted-foreground leading-relaxed flex gap-1.5">
                      <span className="text-muted-foreground/50 shrink-0">•</span>
                      <span>{item}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}

        {!expandedChangelog && CHANGELOG.length > 0 && (
          <p className="text-[10px] text-muted-foreground">
            最新版本 v{CHANGELOG[0].version}（{CHANGELOG[0].date}）— 点击展开查看详情
          </p>
        )}
      </div>

      {/* 联系方式 */}
      <div className="space-y-2">
        <h3 className="text-sm font-semibold">联系方式</h3>
        <div className="flex items-center gap-2 rounded-lg border border-border/60 px-3 py-2.5">
          <Mail className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm text-muted-foreground">luminbuddy@ericdocmic.top</span>
        </div>
      </div>

      {/* 链接 */}
      <div className="space-y-2">
        <a
          href="/terms"
          className="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2.5 hover:bg-accent/50 transition-ui"
        >
          <div className="flex items-center gap-2">
            <FileText className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm">服务条款</span>
          </div>
          <ChevronRight className="h-4 w-4 text-muted-foreground/50" />
        </a>
        <a
          href="/privacy"
          className="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2.5 hover:bg-accent/50 transition-ui"
        >
          <div className="flex items-center gap-2">
            <Shield className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm">隐私政策</span>
          </div>
          <ChevronRight className="h-4 w-4 text-muted-foreground/50" />
        </a>
        <a
          href="https://github.com/Echo-Smith/luminbuddy-writing-agent-v2"
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2.5 hover:bg-accent/50 transition-ui"
        >
          <div className="flex items-center gap-2">
            <Github className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm">开源项目</span>
          </div>
          <ChevronRight className="h-4 w-4 text-muted-foreground/50" />
        </a>
      </div>

      {/* 致谢 */}
      <div className="flex flex-col items-center text-center pt-4 border-t">
        <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Heart className="h-3 w-3 text-red-400" />
          <span>由 LuminBuddy 团队精心打造</span>
        </div>
        <p className="text-[10px] text-muted-foreground/60 mt-1">
          © {new Date().getFullYear()} LuminBuddy. All rights reserved.
        </p>
      </div>
    </div>
  );
}
