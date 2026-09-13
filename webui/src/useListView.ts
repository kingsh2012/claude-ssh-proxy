import type { ProColumns, ProTableProps } from "@ant-design/pro-components";
import { useSearchParams } from "@umijs/max";
import type { SorterResult } from "antd/es/table/interface";
import { useMemo } from "react";

const pageSizes = [50, 100, 300, 500];
type ColumnState = NonNullable<
  NonNullable<
    ProTableProps<object, Record<string, unknown>>["columnsState"]
  >["value"]
>;
export function useListView<T extends object>(
  rows: T[],
  sourceColumns: ProColumns<T>[],
  name: string,
  config: {
    searchText?: (row: T) => string;
    match?: (row: T, key: string, value: string) => boolean;
  } = {},
) {
  const [params, setParams] = useSearchParams();
  const query = params.get("query") || "";
  const update = (changes: Record<string, string>) => {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(changes)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next);
  };
  const persistenceKey = `ops-${name}-columns-v2`;
  const identity = String(sourceColumns[0].key ?? sourceColumns[0].dataIndex);
  const defaultValue = useMemo<ColumnState>(
    () => ({
      [identity]: { disable: true, show: true },
      option: { disable: true, show: true },
    }),
    [identity],
  );
  const columns = sourceColumns.map((column) => {
    const key = String(column.key ?? column.dataIndex);
    if (typeof column.width !== "number" || column.width <= 0)
      throw new Error(`表格列 ${key} 缺少有效列宽`);
    return {
      ...column,
      key,
      ...(column.filters || column.filterDropdown
        ? {
            filterMultiple: false,
            filteredValue: params.get(key) ? [params.get(key)!] : null,
          }
        : {}),
      ...(column.sorter
        ? {
            sortOrder:
              params.get("sort") === key
                ? params.get("order") === "asc"
                  ? ("ascend" as const)
                  : ("descend" as const)
                : null,
          }
        : {}),
    };
  });
  const filtered = rows.filter(
    (row) =>
      (!query ||
        !config.searchText ||
        config.searchText(row).toLowerCase().includes(query.toLowerCase())) &&
      columns.every((c) => {
        const value = params.get(String(c.key));
        if (!(c.filters || c.filterDropdown) || !value) return true;
        return config.match
          ? config.match(row, String(c.key), value)
          : String(
              (row as Record<string, unknown>)[String(c.dataIndex ?? c.key)],
            ) === value;
      }),
  );
  const rawSize = Number(params.get("pageSize"));
  const pageSize = pageSizes.includes(rawSize) ? rawSize : 50;
  const rawPage = Number(params.get("page"));
  const current = Math.min(
    Math.max(1, Number.isSafeInteger(rawPage) ? rawPage : 1),
    Math.max(1, Math.ceil(filtered.length / pageSize)),
  );
  const onChange: ProTableProps<T, Record<string, unknown>>["onChange"] = (
    pagination,
    filters,
    sorter,
    extra,
  ) => {
    if (extra.action === "paginate") {
      const size = pagination.pageSize || 50;
      update({
        page:
          size !== pageSize
            ? ""
            : pagination.current === 1
              ? ""
              : String(pagination.current),
        pageSize: size === 50 ? "" : String(size),
      });
    }
    if (extra.action === "filter")
      update({
        ...Object.fromEntries(
          Object.entries(filters).map(([key, value]) => [
            key,
            value?.[0] == null ? "" : String(value[0]),
          ]),
        ),
        page: "",
      });
    if (extra.action === "sort") {
      const s = (Array.isArray(sorter) ? sorter[0] : sorter) as SorterResult<T>;
      update({
        sort: s.order ? String(s.columnKey) : "",
        order: s.order === "ascend" ? "asc" : s.order ? "desc" : "",
        page: "",
      });
    }
  };
  return {
    query,
    search: (value: string) => update({ query: value, page: "" }),
    hasFilters:
      !!query ||
      !!params.get("sort") ||
      columns.some(
        (c) => (c.filters || c.filterDropdown) && params.has(String(c.key)),
      ),
    reset: () =>
      update({
        query: "",
        sort: "",
        order: "",
        page: "",
        ...Object.fromEntries(
          columns
            .filter((c) => c.filters || c.filterDropdown)
            .map((c) => [String(c.key), ""]),
        ),
      }),
    tableProps: {
      columns,
      dataSource: filtered,
      onChange,
      tableLayout: "fixed" as const,
      search: false as const,
      size: "small" as const,
      cardBordered: true,
      scroll: {
        x: columns.reduce((sum, c) => sum + Number(c.width), 0),
      },
      options: {
        reload: false,
        density: false,
        fullScreen: false,
        setting: true,
      },
      columnsState: {
        persistenceKey,
        persistenceType: "localStorage" as const,
        defaultValue,
      },
      pagination: {
        current,
        pageSize,
        showSizeChanger: true,
        pageSizeOptions: pageSizes,
        showTotal: (total: number) => `共 ${total} 条`,
      },
    },
  };
}
