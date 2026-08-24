/*
 * JSON / SSE 语法着色：
 * 单趟正则替换、字符串优先消费；j-k 键 / j-s 字符串 / j-n 数字 / j-b 布尔。
 */
const JRE = /"(?:\\.|[^"\\])*"(\s*:)?|-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b|\btrue\b|\bfalse\b|\bnull\b/g

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** 返回带 <span class="j-*"> 高亮标记的 HTML（输入先转义，输出可直接进 pre）。 */
export function colorize(src: string): string {
  return esc(src).replace(JRE, (m, isKey) => {
    if (m[0] === '"') return `<span class="${isKey ? 'j-k' : 'j-s'}">${m}</span>`
    if (m === 'true' || m === 'false' || m === 'null') return `<span class="j-b">${m}</span>`
    return `<span class="j-n">${m}</span>`
  })
}
