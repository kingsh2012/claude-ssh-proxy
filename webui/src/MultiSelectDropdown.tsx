import { Select } from "antd";
export interface MultiSelectOption {
  id: string | number;
  label: string;
  sublabel?: string;
}
export function MultiSelectDropdown({
  options,
  selectedIds,
  onToggle,
  placeholder,
  emptyText,
}: {
  options: MultiSelectOption[];
  selectedIds: Set<string | number>;
  onToggle: (id: string | number) => void;
  placeholder: string;
  emptyText?: string;
}) {
  return (
    <Select
      mode="multiple"
      style={{ width: "100%" }}
      value={[...selectedIds]}
      placeholder={placeholder}
      notFoundContent={emptyText}
      maxTagCount="responsive"
      optionFilterProp="label"
      options={options.map((o) => ({
        value: o.id,
        label: o.sublabel ? `${o.label} · ${o.sublabel}` : o.label,
      }))}
      onSelect={onToggle}
      onDeselect={onToggle}
    />
  );
}
export function SingleSelectDropdown({
  options,
  value,
  onChange,
  placeholder,
  emptyText,
  noneLabel,
}: {
  options: MultiSelectOption[];
  value: string | number | null;
  onChange: (id: string | number | null) => void;
  placeholder: string;
  emptyText?: string;
  noneLabel?: string;
}) {
  return (
    <Select
      allowClear
      showSearch
      style={{ width: "100%" }}
      value={value ?? undefined}
      onChange={(v) => onChange(v ?? null)}
      placeholder={placeholder || noneLabel}
      notFoundContent={emptyText}
      optionFilterProp="label"
      options={options.map((o) => ({
        value: o.id,
        label: o.sublabel ? `${o.label} · ${o.sublabel}` : o.label,
      }))}
    />
  );
}
export function SelectDropdown<T extends string>({
  options,
  value,
  onChange,
}: {
  options: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
}) {
  return (
    <Select<T>
      style={{ width: "100%" }}
      options={options}
      value={value}
      onChange={onChange}
    />
  );
}
