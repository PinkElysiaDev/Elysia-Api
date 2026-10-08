/** 流式渲染前补齐未配对围栏：流到一半的 ``` 块按 micromark 语义会吞掉后续
 * 一切内容直到 EOF，补一个闭合围栏让未完代码块先以代码块形态呈现。 */
export function closeUnbalancedFences(text: string): string {
  const fences = text.match(/^[ \t]*(?:```|~~~)/gm) ?? [];
  return fences.length % 2 === 1 ? text + "\n```" : text;
}
