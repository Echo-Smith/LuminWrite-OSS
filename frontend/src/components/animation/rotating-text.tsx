/**
 * RotatingText — 词组轮换（物理弹簧曲线）
 *
 * "老虎机"式滚动：旧词向上滑出遮罩、新词带过冲滑入（--anim-ease-spring），
 * 容器宽度随当前词长以同一条弹簧曲线过渡。
 * 每个词的自然宽度通过隐藏测量层获取，字体加载与视口变化时重测。
 */
import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";

export function RotatingText({
  words,
  interval = 2600,
  className,
}: {
  words: string[];
  interval?: number;
  className?: string;
}) {
  const [index, setIndex] = useState(0);
  const [outgoing, setOutgoing] = useState<string | null>(null);
  const indexRef = useRef(0);
  const measureRef = useRef<HTMLSpanElement | null>(null);
  const [width, setWidth] = useState<number | null>(null);

  // 轮换：旧词只保留一份作为离场副本向上滑出，新词同时弹簧滑入（各渲染一次，无重影）
  useEffect(() => {
    if (words.length < 2) return;
    let outTimer: number | undefined;
    const timer = window.setInterval(() => {
      const from = indexRef.current;
      const next = (from + 1) % words.length;
      setOutgoing(words[from]);
      setIndex(next);
      indexRef.current = next;
      outTimer = window.setTimeout(() => setOutgoing(null), 400);
    }, interval);
    return () => {
      window.clearInterval(timer);
      if (outTimer) window.clearTimeout(outTimer);
    };
  }, [interval, words]);

  // 宽度测量（当前词），随字体加载与视口变化重测
  useEffect(() => {
    const measure = () => {
      const el = measureRef.current?.children[index] as HTMLElement | undefined;
      if (el) setWidth(el.offsetWidth);
    };
    measure();
    document.fonts?.ready.then(measure).catch(() => {});
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [index, words]);

  return (
    <span
      className={cn("rt-window", className)}
      style={width ? { width: `${width}px` } : undefined}
      aria-hidden="true"
    >
      {/* 测量层：不可见，仅用于取每个词的自然宽度 */}
      <span ref={measureRef} className="rt-measure">
        {words.map((w) => (
          <span key={w}>{w}</span>
        ))}
      </span>
      {outgoing && (
        <span className="rt-word rt-word-out">{outgoing}</span>
      )}
      <span className="rt-word rt-word-in" key={index}>
        {words[index]}
      </span>
    </span>
  );
}
