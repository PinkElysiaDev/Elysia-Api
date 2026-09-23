import type { AgentDocument } from './types'

/**
 * 未发送内容的本机缓存：文本与附件（data URL，单文件可达数 MB）按会话存进
 * IndexedDB。localStorage 配额装不下附件，所以不用它。只留在这台浏览器，
 * 发送成功或删除会话时清掉。
 */

const DB_NAME = 'elysia-agent'
const STORE = 'drafts'

export interface AgentComposerDraft {
  text: string
  documents: AgentDocument[]
}

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1)
    request.onupgradeneeded = () => {
      request.result.createObjectStore(STORE)
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

export async function loadComposerDraft(sessionId: string): Promise<AgentComposerDraft | null> {
  try {
    const db = await openDb()
    return await new Promise((resolve) => {
      const request = db.transaction(STORE).objectStore(STORE).get(sessionId)
      request.onsuccess = () => resolve((request.result as AgentComposerDraft | undefined) ?? null)
      request.onerror = () => resolve(null)
    })
  } catch {
    return null
  }
}

export async function saveComposerDraft(sessionId: string, draft: AgentComposerDraft): Promise<void> {
  try {
    const db = await openDb()
    const store = db.transaction(STORE, 'readwrite').objectStore(STORE)
    if (!draft.text.trim() && draft.documents.length === 0) {
      store.delete(sessionId)
      return
    }
    store.put(draft, sessionId)
  } catch {
    /* 隐私模式或配额耗尽：草稿退化为仅内存，不打扰用户 */
  }
}

export async function deleteComposerDraft(sessionId: string): Promise<void> {
  try {
    const db = await openDb()
    db.transaction(STORE, 'readwrite').objectStore(STORE).delete(sessionId)
  } catch {
    /* 同上 */
  }
}

/** 总览卡片用：哪些会话留有未发送内容。读失败按没有处理。 */
export async function sessionsWithDrafts(sessionIds: string[]): Promise<Set<string>> {
  const found = new Set<string>()
  try {
    const db = await openDb()
    await new Promise<void>((resolve) => {
      const request = db.transaction(STORE).objectStore(STORE).openCursor()
      request.onsuccess = () => {
        const cursor = request.result
        if (!cursor) {
          resolve()
          return
        }
        const value = cursor.value as AgentComposerDraft
        if (sessionIds.includes(String(cursor.key)) && (value.text.trim() || value.documents.length > 0)) {
          found.add(String(cursor.key))
        }
        cursor.continue()
      }
      request.onerror = () => resolve()
    })
  } catch {
    /* 同上 */
  }
  return found
}
