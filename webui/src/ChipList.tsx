import { Tag, Tooltip, Typography } from "antd";
export function ChipList({
  items,
  max = 3,
  emptyText,
}: {
  items: string[];
  max?: number;
  emptyText: string;
}) {
  if (!items.length)
    return <Typography.Text type="secondary">{emptyText}</Typography.Text>;
  return (
    <div className="chip-list">
      {items.slice(0, max).map((item) => (
        <Tag key={item}>{item}</Tag>
      ))}
      {items.length > max && (
        <Tooltip title={items.slice(max).join(", ")}>
          <Tag>+{items.length - max}</Tag>
        </Tooltip>
      )}
    </div>
  );
}
