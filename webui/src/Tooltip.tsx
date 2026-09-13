import { Tooltip as AntTooltip } from "antd";
export function Tooltip({
  text,
  children,
}: {
  text: string;
  children: React.ReactNode;
}) {
  return (
    <AntTooltip
      title={text}
      styles={{
        root: { maxWidth: "min(560px, calc(100vw - 32px))" },
        container: {
          whiteSpace: "pre-wrap",
          overflowWrap: "anywhere",
          maxHeight: "60vh",
          overflowY: "auto",
          fontSize: 12,
          lineHeight: 1.7,
        },
      }}
    >
      <span>{children}</span>
    </AntTooltip>
  );
}
