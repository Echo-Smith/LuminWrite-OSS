/**
 * ─── 动画 Hooks ─────────────────────────────────────────────
 *
 * 可复用的动画相关 React Hooks。
 */
import { useState, useEffect, useRef, useCallback } from "react";

/**
 * 检测元素是否进入视口（用于触发入场动画）
 *
 * @example
 * const { ref, inView } = useInView({ threshold: 0.1 });
 * return <div ref={ref} className={inView ? "anim-fade-up" : "opacity-0"} />;
 */
export function useInView<T extends HTMLElement = HTMLDivElement>(
  options?: IntersectionObserverInit & { once?: boolean }
) {
  const ref = useRef<T>(null);
  const [inView, setInView] = useState(false);
  const once = options?.once ?? true;

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    // SSR / 无 IntersectionObserver 支持
    if (typeof IntersectionObserver === "undefined") {
      setInView(true);
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setInView(true);
          if (once) observer.disconnect();
        } else if (!once) {
          setInView(false);
        }
      },
      {
        threshold: options?.threshold ?? 0.1,
        rootMargin: options?.rootMargin ?? "0px",
      }
    );

    observer.observe(el);
    return () => observer.disconnect();
  }, [once, options?.threshold, options?.rootMargin]);

  return { ref, inView };
}

/**
 * 组件挂载检测（用于确保入场动画在 hydration 后触发）
 */
export function useMounted(delay = 0) {
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    if (delay > 0) {
      const timer = setTimeout(() => setMounted(true), delay);
      return () => clearTimeout(timer);
    }
    setMounted(true);
  }, [delay]);

  return mounted;
}

/**
 * 检测用户是否偏好减少动画
 */
export function usePrefersReducedMotion() {
  const [reduced, setReduced] = useState(false);

  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    setReduced(mq.matches);

    const handler = (e: MediaQueryListEvent) => setReduced(e.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);

  return reduced;
}

/**
 * 生成 stagger（错开）延迟
 *
 * @example
 * const delays = useStagger(items.length, 50);
 * items.map((item, i) => (
 *   <div style={{ animationDelay: `${delays[i]}ms` }} className="anim-fade-up" />
 * ));
 */
export function useStagger(count: number, interval = 50, start = 0) {
  return Array.from({ length: count }, (_, i) => start + i * interval);
}

/**
 * 闪烁/高亮效果（用于新消息出现时的高亮）
 */
export function useFlash(duration = 1000) {
  const [flashing, setFlashing] = useState(false);

  const flash = useCallback(() => {
    setFlashing(true);
    setTimeout(() => setFlashing(false), duration);
  }, [duration]);

  return { flashing, flash };
}

/**
 * 平滑滚动到底部
 */
export function useScrollToBottom<T extends HTMLElement = HTMLDivElement>(
  deps: unknown[]
) {
  const ref = useRef<T>(null);

  useEffect(() => {
    if (ref.current) {
      ref.current.scrollTo({
        top: ref.current.scrollHeight,
        behavior: "smooth",
      });
    }
  }, deps);

  return ref;
}
