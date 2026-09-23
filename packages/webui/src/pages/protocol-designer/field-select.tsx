import { Link2 } from "lucide-react";
import { Input } from "@/components/ui/input";

/**
 * 映射位字段选择：datalist 补全 + 通俗解释 + 未知字段告警。
 * 树编辑器与映射位编辑器共用；单一 Input 形态（datalist 补全）避免
 * 已知/未知字段间切换 Select↔Input 导致组件卸载重建、输入中失焦。
 */
export function FieldSelect({
  value,
  onChange,
  fields,
  datalistId,
  placeholder = "映射到的 Maheshvara 字段",
}: {
  value: string;
  onChange: (next: string) => void;
  fields: { name: string; label: string }[];
  datalistId: string;
  placeholder?: string;
}) {
  const spec = fields.find((field) => field.name === value);
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        <Link2 className="h-3 w-3 shrink-0 text-primary" />
        <Input
          list={datalistId}
          className="h-7 w-full min-w-40 font-mono text-xs"
          placeholder={placeholder}
          value={value}
          onChange={(event) => onChange(event.target.value.trim())}
        />
      </div>
      {spec ? (
        <span className="pl-[18px] text-2xs text-muted-foreground">
          {spec.label}
        </span>
      ) : (
        value !== "" && (
          <span className="pl-[18px] text-2xs text-amber-600 dark:text-amber-400">
            未知字段，保存时将被拒绝
          </span>
        )
      )}
    </div>
  );
}
