import { useState } from 'react'
import type { AgentDocument } from '@/lib/agent/types'

/** 单文件 ≤8MiB、最多 20 个（与后端限制对齐）。 */
const MAX_FILE_BYTES = 8 << 20
const MAX_FILES = 20
/** 请求体总预算：后端整个 JSON 上限 40MiB，dataUrl 约 4/3 膨胀 + 文本与
 * JSON 开销，取 28MiB 保守值——单文件/个数限制拦不住「3 个 8MiB 文件」。 */
const MAX_TOTAL_PAYLOAD = 28 << 20

async function fileToDocument(file: File): Promise<AgentDocument> {
  const isTextLike = file.type.startsWith('text/') || /\.(txt|md|json|yaml|yml|xml|csv|html?)$/i.test(file.name)
  if (isTextLike) {
    return { name: file.name, mime: file.type || 'text/plain', text: await file.text() }
  }
  const dataUrl = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
  return { name: file.name, mime: file.type || 'application/octet-stream', dataUrl }
}

/** 文档的传输体积估算：dataUrl 按其字符串长度（≈原始 4/3），文本按长度。 */
function docPayloadBytes(doc: AgentDocument) {
  return (doc.dataUrl ?? doc.text ?? '').length
}

/**
 * Composer 的附件集：文本类读为纯文本、其余转 dataUrl；逐文件限 8MiB、
 * 总量限约 28MiB、数量限 20，超限项丢弃并经 notify 提示（按新到先裁：
 * 保住已有附件，超预算的后来者丢弃）。
 */
export function useComposerAttachments(notify: (description: string) => void, initial: AgentDocument[] = []) {
  const [documents, setDocuments] = useState<AgentDocument[]>(initial)

  async function addFiles(files: FileList | File[]) {
    const next: AgentDocument[] = []
    for (const file of Array.from(files)) {
      if (file.size > MAX_FILE_BYTES) {
        notify(`${file.name} 超过 8MiB 上限，已跳过`)
        continue
      }
      next.push(await fileToDocument(file))
    }
    setDocuments((current) => {
      let merged = [...current, ...next]
      if (merged.length > MAX_FILES) {
        notify(`附件最多 ${MAX_FILES} 个`)
        merged = merged.slice(0, MAX_FILES)
      }
      const total = merged.reduce((sum, doc) => sum + docPayloadBytes(doc), 0)
      if (total > MAX_TOTAL_PAYLOAD) {
        const kept: AgentDocument[] = []
        let budget = MAX_TOTAL_PAYLOAD
        for (const doc of merged) {
          const size = docPayloadBytes(doc)
          if (size > budget) {
            notify(`${doc.name ?? '附件'} 超出总预算（约 28MiB），未添加`)
            continue
          }
          kept.push(doc)
          budget -= size
        }
        merged = kept
      }
      return merged
    })
  }

  return { documents, setDocuments, addFiles }
}
