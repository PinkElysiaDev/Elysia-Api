import type {
  CustomProtocolBodyFieldRef,
  CustomProtocolBodyTree,
  CustomProtocolResponse,
  CustomProtocolResponseFieldMapping,
  CustomProtocolResponseBodyLeaf,
  CustomProtocolResponseBodyTree,
} from '@/lib/types'

/**
 * 请求体 / 返回体构造树的共享工具：叶子判别、映射位收集（供映射配置卡）、
 * 从 fields + 示例合成返回体构造树（供 AI 草稿一键转换）。
 */

export function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function isRequestFieldRef(value: unknown): value is CustomProtocolBodyFieldRef {
  return isPlainObject(value) && typeof value.field === 'string'
}

export function isRequestConstant(value: unknown): value is { value: unknown } {
  return isPlainObject(value) && 'value' in value && Object.keys(value).length === 1
}

export function isResponseMappingLeaf(value: unknown): value is CustomProtocolResponseBodyLeaf {
  return isPlainObject(value) && 'field' in value && typeof (value as { field?: unknown }).field === 'string'
}

export function isResponsePlaceholder(value: unknown): value is { value: unknown } {
  return isPlainObject(value) && 'value' in value && Object.keys(value).length === 1
}

export interface RequestMappingSlot {
  /** 树内展示路径（如 params.temperature / items[0]） */
  path: string
  leaf: CustomProtocolBodyFieldRef
  setLeaf: (next: CustomProtocolBodyFieldRef) => void
}

/** 收集请求体构造树中的全部映射位（含就地更新函数）。 */
export function collectRequestMappings(
  tree: CustomProtocolBodyTree | undefined,
  onChange: (next: CustomProtocolBodyTree) => void,
): RequestMappingSlot[] {
  const slots: RequestMappingSlot[] = []
  const root = tree === undefined || tree === null ? {} : tree
  const walk = (node: unknown, path: string, replace: (next: unknown) => void) => {
    if (isRequestFieldRef(node)) {
      slots.push({
        path: path || '（根）',
        leaf: node,
        setLeaf: (next) => replace(next),
      })
      return
    }
    if (Array.isArray(node)) {
      node.forEach((item, index) => walk(item, `${path}[${index}]`, (next) => {
        const items = node.slice()
        items[index] = next as CustomProtocolBodyTree
        replace(items)
      }))
      return
    }
    if (isPlainObject(node)) {
      Object.entries(node).forEach(([key, child]) =>
        walk(child, path ? `${path}.${key}` : key, (next) => {
          replace({ ...node, [key]: next as CustomProtocolBodyTree })
        }),
      )
    }
  }
  walk(root, '', (next) => onChange(next as CustomProtocolBodyTree))
  return slots
}

export interface ResponseMappingSlot {
  path: string
  leaf: CustomProtocolResponseBodyLeaf
  setLeaf: (next: CustomProtocolResponseBodyLeaf) => void
}

/** 收集返回体构造树中的全部映射位（含就地更新函数）。 */
export function collectResponseMappings(
  tree: CustomProtocolResponseBodyTree | undefined,
  onChange: (next: CustomProtocolResponseBodyTree) => void,
): ResponseMappingSlot[] {
  const slots: ResponseMappingSlot[] = []
  const walk = (node: unknown, path: string, replace: (next: unknown) => void) => {
    if (isResponseMappingLeaf(node)) {
      if ((node.field ?? '').trim() !== '') {
        slots.push({
          path: path || '$',
          leaf: node,
          setLeaf: (next) => replace(next),
        })
      }
      return
    }
    if (Array.isArray(node)) {
      node.forEach((item, index) => walk(item, `${path}[${index}]`, (next) => {
        const items = node.slice()
        items[index] = next as CustomProtocolResponseBodyTree
        replace(items)
      }))
      return
    }
    if (isPlainObject(node)) {
      Object.entries(node).forEach(([key, child]) =>
        walk(child, path ? `${path}.${key}` : key, (next) => {
          replace({ ...node, [key]: next as CustomProtocolResponseBodyTree })
        }),
      )
    }
  }
  walk(tree, '', (next) => onChange(next as CustomProtocolResponseBodyTree))
  return slots
}

function pathSegments(path: string): string[] {
  // 仅支持点路径 + [n] 下标的常规形态（与生成器产出一致）。
  return path
    .replace(/^\$\.?/, '')
    .split(/\.|\[(\d+)\]/)
    .filter((segment) => segment !== undefined && segment !== '')
}

/**
 * 从 fields 映射 + 示例响应合成返回体构造树：按 path 建嵌套结构，映射叶子
 * 标注 field 并尽量从示例取值；示例缺失时用占位文本。
 */
export function synthesizeResponseBodyFrom(
  fields: CustomProtocolResponseFieldMapping[],
  sample: unknown,
): CustomProtocolResponseBodyTree {
  const placeholder = (mapping: CustomProtocolResponseFieldMapping, sampleValue: unknown): unknown => {
    if (sampleValue !== undefined) return sampleValue
    const shape = mapping.field === 'text' || mapping.field === 'reasoning' || mapping.field === 'stop_reason' ? '示例文本' : '示例值'
    return shape
  }

  let root: Record<string, unknown> = {}
  for (const mapping of fields) {
    const segments = pathSegments(mapping.path)
    if (segments.length === 0) continue
    const sampleAt = <T,>(probe: (value: unknown) => T | undefined): T | undefined => {
      let current: unknown = sample
      for (const segment of segments.slice(0, -1)) {
        if (Array.isArray(current)) current = current[Number(segment)]
        else if (isPlainObject(current)) current = current[segment]
        else return undefined
      }
      return probe(current)
    }
    const last = segments[segments.length - 1]
    const leaf: Record<string, unknown> = { field: mapping.field }
    if (mapping.transform) leaf.transform = mapping.transform
    leaf.value = placeholder(
      mapping,
      sampleAt((value) =>
        isPlainObject(value) ? value[last] : Array.isArray(value) ? value[Number(last)] : undefined,
      ),
    )
    // 逐层写入（浅实现：直接按 segments 重建，重复前缀合并交给对象合并）
    root = mergeAtPath(root, segments, leaf)
  }
  return root as CustomProtocolResponseBodyTree
}

function mergeAtPath(target: Record<string, unknown>, segments: string[], leaf: unknown): Record<string, unknown> {
  const [head, ...rest] = segments
  const index = Number(head)
  const isIndex = !Number.isNaN(index) && /^\d+$/.test(head)
  if (rest.length === 0) {
    if (isIndex) {
      // 数组叶子场景极少（fields 路径以数组下标结尾）：退化为对象键 "[n]"
      return { ...target, [`[${head}]`]: leaf }
    }
    return { ...target, [head]: leaf }
  }
  if (isIndex) {
    return { ...target, [`[${head}]`]: mergeAtPath(
      isPlainObject(target[`[${head}]`]) ? (target[`[${head}]`] as Record<string, unknown>) : {},
      rest,
      leaf,
    ) }
  }
  const child = isPlainObject(target[head]) ? (target[head] as Record<string, unknown>) : {}
  return { ...target, [head]: mergeAtPath(child, rest, leaf) }
}

/** 返回体树是否存在（区别于 undefined：空对象也视为已开始构造）。 */
export function hasResponseBodyTree(response: CustomProtocolResponse): boolean {
  return response.body !== undefined && response.body !== null
}
