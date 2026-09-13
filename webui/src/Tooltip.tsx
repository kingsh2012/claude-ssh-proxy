import { Tooltip as AntTooltip } from "antd";
export function Tooltip({
  text,
  children,
}: {
  text: string;
  children: React.ReactNode;
}) {
  return (
    <AntTooltip title={text}>
      <span>{children}</span>
    </AntTooltip>
  );
}
