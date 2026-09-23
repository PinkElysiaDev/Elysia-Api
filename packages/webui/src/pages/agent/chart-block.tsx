import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { BarChart3 } from "lucide-react";
import { modelColor } from "@/components/usage-chart-model";
import type { AgentChartSpec } from "@/lib/agent/chart";

/**
 * 聊天内嵌图表：渲染助手输出的 ```chart 围栏 JSON。
 * 规格：{type:"bar"|"line"|"pie", title?, x:[类目], series:[{name, data:[数值]}]}。
 * 规格非法时由调用方回退为普通代码块展示。
 */

import { CHART_TICK } from "@/lib/utils";

function toRows(spec: AgentChartSpec) {
  const rows: Record<string, unknown>[] = [];
  const length = spec.x?.length ?? 0;
  for (let i = 0; i < length; i += 1) {
    const row: Record<string, unknown> = { name: String(spec.x?.[i] ?? i) };
    (spec.series ?? []).forEach((series, seriesIndex) => {
      row[series.name || `系列${seriesIndex + 1}`] = Number(
        series.data?.[i] ?? 0,
      );
    });
    rows.push(row);
  }
  return rows;
}

export function ChartBlock({ spec }: { spec: AgentChartSpec }) {
  const rows = toRows(spec);
  const seriesNames = (spec.series ?? []).map(
    (series, index) => series.name || `系列${index + 1}`,
  );
  const height = 240;

  return (
    <div className="my-1 rounded-lg border border-border bg-card p-2.5">
      {spec.title ? (
        <p className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
          <BarChart3 className="h-3.5 w-3.5" />
          {spec.title}
        </p>
      ) : null}
      <div style={{ height }} className="w-full">
        <ResponsiveContainer width="100%" height={height}>
          {spec.type === "pie" ? (
            <PieChart>
              <Tooltip
                contentStyle={{
                  fontSize: 11,
                  background: "hsl(var(--popover))",
                  border: "1px solid hsl(var(--border))",
                  borderRadius: 8,
                }}
              />
              <Pie
                data={rows.map((row) => ({
                  name: String(row.name),
                  value: Number(row[seriesNames[0]] ?? 0),
                }))}
                dataKey="value"
                nameKey="name"
                innerRadius="45%"
                outerRadius="80%"
                paddingAngle={2}
              >
                {rows.map((_row, index) => (
                  <Cell key={index} fill={modelColor(index)} />
                ))}
              </Pie>
              <Legend wrapperStyle={{ fontSize: 11 }} />
            </PieChart>
          ) : spec.type === "line" ? (
            <LineChart
              data={rows}
              margin={{ top: 8, right: 16, bottom: 0, left: 0 }}
            >
              <CartesianGrid
                stroke="hsl(var(--border) / 0.45)"
                strokeDasharray="3 3"
                vertical={false}
              />
              <XAxis
                dataKey="name"
                tick={CHART_TICK}
                interval="preserveStartEnd"
              />
              <YAxis tick={CHART_TICK} width={48} />
              <Tooltip
                contentStyle={{
                  fontSize: 11,
                  background: "hsl(var(--popover))",
                  border: "1px solid hsl(var(--border))",
                  borderRadius: 8,
                }}
              />
              <Legend wrapperStyle={{ fontSize: 11 }} />
              {seriesNames.map((name, index) => (
                <Line
                  key={name}
                  dataKey={name}
                  type="monotone"
                  stroke={modelColor(index)}
                  strokeWidth={2}
                  dot={false}
                />
              ))}
            </LineChart>
          ) : (
            <BarChart
              data={rows}
              margin={{ top: 8, right: 16, bottom: 0, left: 0 }}
            >
              <CartesianGrid
                stroke="hsl(var(--border) / 0.45)"
                strokeDasharray="3 3"
                vertical={false}
              />
              <XAxis
                dataKey="name"
                tick={CHART_TICK}
                interval="preserveStartEnd"
              />
              <YAxis tick={CHART_TICK} width={48} />
              <Tooltip
                cursor={{ fill: "var(--wash)" }}
                contentStyle={{
                  fontSize: 11,
                  background: "hsl(var(--popover))",
                  border: "1px solid hsl(var(--border))",
                  borderRadius: 8,
                }}
              />
              <Legend wrapperStyle={{ fontSize: 11 }} />
              {seriesNames.map((name, index) => (
                <Bar
                  key={name}
                  dataKey={name}
                  fill={modelColor(index)}
                  fillOpacity={0.75}
                  radius={[3, 3, 0, 0]}
                  maxBarSize={28}
                />
              ))}
            </BarChart>
          )}
        </ResponsiveContainer>
      </div>
    </div>
  );
}
