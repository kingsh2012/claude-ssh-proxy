import { useRef, useState } from "react";
import { createPortal } from "react-dom";

// Tooltip 用 fixed 定位 + portal 渲染到 body 上,而不是跟着触发元素走的 absolute 定位。
// 原因:表格外层容器是 overflow-x-auto(横向滚动表格),CSS 规定只要一个轴设了
// auto/scroll,另一个轴哪怕写了 visible 也会被当成 auto 处理——所以最后一行的提示框往下
// 弹出、超出容器底边时,会莫名其妙冒出一条纵向滚动条。用 portal 挂到 body 上就完全跳出了
// 这个容器,不会再触发这个隐藏的滚动条。同时按可用空间自动选择往上还是往下弹。
export function Tooltip({ text, children }: { text: string; children: React.ReactNode }) {
  const [pos, setPos] = useState<{ left: number; top: number; openUp: boolean } | null>(null);
  const triggerRef = useRef<HTMLSpanElement>(null);

  function show() {
    const rect = triggerRef.current?.getBoundingClientRect();
    if (!rect) return;
    const openUp = rect.bottom + 80 > window.innerHeight;
    setPos({
      left: rect.left + rect.width / 2,
      top: openUp ? rect.top - 6 : rect.bottom + 6,
      openUp,
    });
  }

  function hide() {
    setPos(null);
  }

  return (
    <span ref={triggerRef} className="inline-block" onMouseEnter={show} onMouseLeave={hide}>
      {children}
      {pos &&
        createPortal(
          <span
            className="pointer-events-none fixed z-50 w-max max-w-xs rounded-md bg-slate-900
              px-2 py-1 text-xs text-white shadow-lg dark:bg-slate-700"
            style={{ left: pos.left, top: pos.top, transform: `translate(-50%, ${pos.openUp ? "-100%" : "0"})` }}
          >
            {text}
          </span>,
          document.body,
        )}
    </span>
  );
}
