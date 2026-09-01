import { useEffect, useState } from 'react'
import { Input } from '@/components/ui/input'

/** 数字输入：清空时保持空串展示、失焦还原旧值，只在输入有效数字时提交。
 * 直接 Number(v)||0 会让「清空准备输入新值」的瞬间坍缩成显式 0——对
 * bodyMaxKB 这类 0 具有「关闭/不保存」语义的字段，等于一次误触就静默关功能。
 * 想真正设为 0 必须显式键入 0。 */
export function NumberField({
  value,
  onCommit,
  min,
  className,
}: {
  value: number
  onCommit: (v: number) => void
  min?: number
  className?: string
}) {
  const [text, setText] = useState<string>(String(value))
  useEffect(() => {
    setText(String(value))
  }, [value])
  return (
    <Input
      type="number"
      min={min}
      className={className}
      value={text}
      onChange={(e) => {
        const raw = e.target.value.trim()
        if (raw === '') {
          setText('') // 挂起空态，不提交
          return
        }
        const n = Number(raw)
        if (Number.isFinite(n)) {
          setText(e.target.value)
          onCommit(Math.max(min ?? 0, Math.floor(n)))
        }
      }}
      onBlur={() => {
        if (text.trim() === '') setText(String(value)) // 空态失焦：还原，不误置 0
      }}
    />
  )
}
