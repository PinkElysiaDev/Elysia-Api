import type {
  CustomProtocolBodyFieldRef,
  CustomProtocolResponseFieldMapping,
  CustomProtocolResponseBodyLeaf,
  CustomProtocolResponseBodyTree,
} from '@/lib/types'

/**
 * 请求体 / 返回体构造树的共享工具：叶子判别、从 fields + 示例合成
 * 返回体构造树（供行表一键转换 / AI 草稿转换）。
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

/** 构造树中一个映射位叶子的定位信息:显示路径 + 写回用的键段序列。 */
export interface MappedLeafLocator {
  /** 展示用点路径(数组下标 [n]) */
  displayPath: string
  /** 写回用的键段序列(对象键或数组下标) */
  segments: (string | number)[]
  node: Record<string, unknown>
}

function collectMappedLeavesInto(
  value: unknown,
  segments: (string | number)[],
  displayPath: string,
  isMapped: (node: unknown) => boolean,
  out: MappedLeafLocator[],
) {
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      collectMappedLeavesInto(item, [...segments, index], `${displayPath}[${index}]`, isMapped, out)
    })
    return
  }
  if (isPlainObject(value)) {
    if (isMapped(value)) {
      out.push({ displayPath: displayPath || '(根)', segments: [...segments], node: value })
      return
    }
    for (const key of Object.keys(value)) {
      collectMappedLeavesInto(value[key], [...segments, key], displayPath ? `${displayPath}.${key}` : key, isMapped, out)
    }
  }
}

/** 收集请求体构造树里的全部映射位叶子(常量/裸标量跳过)。 */
export function collectRequestMappedLeaves(tree: unknown): MappedLeafLocator[] {
  const out: MappedLeafLocator[] = []
  collectMappedLeavesInto(tree, [], '', isRequestFieldRef, out)
  return out
}

/** 收集返回体构造树里的全部映射位叶子(纯示例占位跳过)。 */
export function collectResponseMappedLeaves(tree: unknown): MappedLeafLocator[] {
  const out: MappedLeafLocator[] = []
  collectMappedLeavesInto(tree, [], '', isResponseMappingLeaf, out)
  return out
}

/**
 * 按键段序列把更新后的叶子写回构造树(不可变更新,返回新树)。
 * 段序列由 collect*MappedLeaves 产出,与树的真实形态一致。
 */
export function replaceLeafAtPath(
  tree: unknown,
  segments: (string | number)[],
  nextLeaf: unknown,
): unknown {
  if (segments.length === 0) return nextLeaf
  const [head, ...rest] = segments
  if (typeof head === 'number') {
    const array = Array.isArray(tree) ? tree : []
    const copy = [...array]
    copy[head] = replaceLeafAtPath(copy[head], rest, nextLeaf)
    return copy
  }
  const object = isPlainObject(tree) ? { ...tree } : {}
  object[head] = replaceLeafAtPath(object[head], rest, nextLeaf)
  return object
}
