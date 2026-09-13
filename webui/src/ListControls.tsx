import { SearchOutlined } from "@ant-design/icons";
import { Input, Tooltip, Button, Space } from "antd";
import type { ReactNode } from "react";
import { useEffect, useState } from "react";

export function ToolbarIconAction({
  label,
  icon,
  onClick,
  disabled,
}: {
  label: string;
  icon: ReactNode;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <Tooltip title={label}>
      <button
        type="button"
        className="toolbar-icon"
        aria-label={label}
        disabled={disabled}
        onClick={onClick}
      >
        {icon}
      </button>
    </Tooltip>
  );
}
export function ListToolbarSearch({
  value,
  onSearch,
  placeholder,
}: {
  value: string;
  onSearch: (value: string) => void;
  placeholder: string;
}) {
  const [input, setInput] = useState(value);
  useEffect(() => setInput(value), [value]);
  return (
    <div className="list-search">
      <Input
        allowClear
        prefix={<SearchOutlined />}
        placeholder={placeholder}
        aria-label={placeholder}
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onPressEnter={() => onSearch(input.trim())}
        onClear={() => onSearch("")}
      />
      <span className="search-hint">
        {value ? "已按关键词筛选" : "回车搜索"}
      </span>
    </div>
  );
}

export function TextColumnFilter({
  selectedKeys,
  setSelectedKeys,
  confirm,
  clearFilters,
}: import("antd/es/table/interface").FilterDropdownProps) {
  return (
    <div style={{ padding: 12 }}>
      <Input
        aria-label="筛选关键词"
        placeholder="输入筛选关键词"
        value={String(selectedKeys[0] || "")}
        onChange={(e) =>
          setSelectedKeys(e.target.value ? [e.target.value] : [])
        }
        onPressEnter={() => confirm()}
      />
      <Space style={{ marginTop: 8 }}>
        <Button type="primary" size="small" onClick={() => confirm()}>
          确定
        </Button>
        <Button size="small" onClick={() => clearFilters?.()}>
          重置
        </Button>
      </Space>
    </div>
  );
}
