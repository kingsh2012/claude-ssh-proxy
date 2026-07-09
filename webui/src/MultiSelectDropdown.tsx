import { useEffect, useRef, useState } from "react";

export interface MultiSelectOption {
  id: number;
  label: string;
  sublabel?: string;
}

// MultiSelectDropdown 是一个"看起来跟原生 <select> 一样"的多选下拉框:
// 收起时用 chip 展示已选项(可以直接点 x 移除),点击整个框展开一个勾选列表。
// 用来让"单选用原生 select、多选用常驻勾选列表"这两种长得不一样的控件统一成同一种下拉风格。
export function MultiSelectDropdown({
  options,
  selectedIds,
  onToggle,
  placeholder,
  emptyText,
}: {
  options: MultiSelectOption[];
  selectedIds: Set<number>;
  onToggle: (id: number) => void;
  placeholder: string;
  emptyText?: string;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onClickOutside);
    return () => document.removeEventListener("mousedown", onClickOutside);
  }, [open]);

  const selected = options.filter((o) => selectedIds.has(o.id));

  return (
    <div ref={rootRef} className="relative">
      <div
        className="input flex min-h-[2.5rem] cursor-pointer items-center justify-between gap-2"
        onClick={() => setOpen((v) => !v)}
      >
        <div className="flex flex-1 flex-wrap items-center gap-1">
          {selected.length === 0 ? (
            <span className="text-slate-400">{placeholder}</span>
          ) : (
            selected.map((o) => (
              <span
                key={o.id}
                className="flex items-center gap-1 rounded bg-slate-100 px-1.5 py-0.5 text-xs whitespace-nowrap dark:bg-slate-800"
              >
                #{o.id} {o.label}
                <button
                  type="button"
                  className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
                  onClick={(e) => {
                    e.stopPropagation();
                    onToggle(o.id);
                  }}
                >
                  ×
                </button>
              </span>
            ))
          )}
        </div>
        <span className={`shrink-0 text-slate-400 transition-transform ${open ? "rotate-180" : ""}`}>▼</span>
      </div>

      {open && (
        <div className="absolute z-10 mt-1 max-h-48 w-full overflow-y-auto rounded-md border border-slate-300 bg-white p-2 shadow-lg dark:border-slate-700 dark:bg-slate-900">
          {options.length === 0 && <p className="text-sm text-slate-400">{emptyText}</p>}
          {options.map((o) => (
            <label
              key={o.id}
              className="flex items-center gap-2 rounded px-1 py-1 text-sm text-slate-700 hover:bg-slate-50 dark:text-slate-300 dark:hover:bg-slate-800"
            >
              <input type="checkbox" checked={selectedIds.has(o.id)} onChange={() => onToggle(o.id)} />
              <span>
                #{o.id} {o.label}
              </span>
              {o.sublabel && <span className="text-xs text-slate-400">{o.sublabel}</span>}
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

// SingleSelectDropdown 是同一套外观的单选版本:收起时显示选中项(或占位文字),
// 点击展开一个列表,点其中一项直接选中并收起——用来替代原生 <select>,
// 让单选和 MultiSelectDropdown 长得完全一样,不再是"一个原生一个自定义"。
export function SingleSelectDropdown({
  options,
  value,
  onChange,
  placeholder,
  noneLabel = "(不设置)",
  emptyText,
}: {
  options: MultiSelectOption[];
  value: number | null;
  onChange: (id: number | null) => void;
  placeholder: string;
  noneLabel?: string;
  emptyText?: string;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onClickOutside);
    return () => document.removeEventListener("mousedown", onClickOutside);
  }, [open]);

  const selected = options.find((o) => o.id === value);
  const disabled = options.length === 0;

  return (
    <div ref={rootRef} className="relative">
      <div
        className={`input flex min-h-[2.5rem] items-center justify-between gap-2 ${
          disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer"
        }`}
        onClick={() => !disabled && setOpen((v) => !v)}
      >
        <span className={selected ? "" : "text-slate-400"}>
          {selected ? `#${selected.id} ${selected.label}` : placeholder}
        </span>
        <span className={`shrink-0 text-slate-400 transition-transform ${open ? "rotate-180" : ""}`}>▼</span>
      </div>

      {open && (
        <div className="absolute z-10 mt-1 max-h-48 w-full overflow-y-auto rounded-md border border-slate-300 bg-white p-2 shadow-lg dark:border-slate-700 dark:bg-slate-900">
          {options.length === 0 && <p className="text-sm text-slate-400">{emptyText}</p>}
          <div
            className="cursor-pointer rounded px-1 py-1 text-sm text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800"
            onClick={() => {
              onChange(null);
              setOpen(false);
            }}
          >
            {noneLabel}
          </div>
          {options.map((o) => (
            <div
              key={o.id}
              className={`cursor-pointer rounded px-1 py-1 text-sm hover:bg-slate-50 dark:hover:bg-slate-800 ${
                o.id === value ? "bg-indigo-50 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300" : "text-slate-700 dark:text-slate-300"
              }`}
              onClick={() => {
                onChange(o.id);
                setOpen(false);
              }}
            >
              #{o.id} {o.label}
              {o.sublabel && <span className="ml-1 text-xs text-slate-400">{o.sublabel}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// SelectDropdown 是给固定枚举(比如认证方式:密码/私钥)用的单选下拉框,永远有值、
// 不需要"(不设置)"这个选项,外观跟 SingleSelectDropdown/MultiSelectDropdown 保持一致。
export function SelectDropdown<T extends string>({
  options,
  value,
  onChange,
}: {
  options: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onClickOutside);
    return () => document.removeEventListener("mousedown", onClickOutside);
  }, [open]);

  const selected = options.find((o) => o.value === value);

  return (
    <div ref={rootRef} className="relative">
      <div
        className="input flex min-h-[2.5rem] cursor-pointer items-center justify-between gap-2"
        onClick={() => setOpen((v) => !v)}
      >
        <span>{selected?.label}</span>
        <span className={`shrink-0 text-slate-400 transition-transform ${open ? "rotate-180" : ""}`}>▼</span>
      </div>

      {open && (
        <div className="absolute z-10 mt-1 w-full overflow-y-auto rounded-md border border-slate-300 bg-white p-2 shadow-lg dark:border-slate-700 dark:bg-slate-900">
          {options.map((o) => (
            <div
              key={o.value}
              className={`cursor-pointer rounded px-1 py-1 text-sm hover:bg-slate-50 dark:hover:bg-slate-800 ${
                o.value === value ? "bg-indigo-50 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300" : "text-slate-700 dark:text-slate-300"
              }`}
              onClick={() => {
                onChange(o.value);
                setOpen(false);
              }}
            >
              {o.label}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
