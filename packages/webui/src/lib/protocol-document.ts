interface JsonNode { start: number; end: number; kind: 'object' | 'array' | 'scalar'; children: Map<string, JsonNode> }
const MAX_DEPTH = 64
const MAX_NODES = 100000
const MAX_BYTES = 8 << 20

/** Index JSON source spans without coercing numeric literals into JS numbers. */
export class ProtocolDocument {
  readonly root: JsonNode
  constructor(readonly source: string) {
    if (new TextEncoder().encode(source).length > MAX_BYTES) throw new Error('JSON 超过编辑器大小限制')
    let offset = 0
    let nodes = 0
    const space = () => { while (/[ \t\r\n]/.test(source[offset] ?? '') && offset < source.length) offset++ }
    const readString = () => {
      const start = offset++
      while (offset < source.length) {
        const character = source[offset++]
        if (character === '\\') offset++
        else if (character === '"') return JSON.parse(source.slice(start, offset)) as string
      }
      throw new Error('JSON 字符串未结束')
    }
    const read = (depth: number): JsonNode => {
      if (depth > MAX_DEPTH || ++nodes > MAX_NODES) throw new Error('JSON 超过深度或节点限制')
      space()
      const node: JsonNode = { start: offset, end: offset, kind: 'scalar', children: new Map() }
      const token = source[offset]
      if (token === '{' || token === '[') {
        node.kind = token === '{' ? 'object' : 'array'
        const end = token === '{' ? '}' : ']'
        offset++; space()
        let index = 0
        while (source[offset] !== end) {
          let key = String(index++)
          if (node.kind === 'object') {
            if (source[offset] !== '"') throw new Error('JSON 对象需要字段名')
            key = readString(); space()
            if (source[offset++] !== ':') throw new Error('JSON 字段缺少冒号')
            if (node.children.has(key)) throw new Error(`JSON 字段重复：${key}`)
          }
          node.children.set(key, read(depth + 1)); space()
          if (source[offset] === end) break
          if (source[offset++] !== ',') throw new Error('JSON 字段或元素缺少逗号')
          space()
          if (source[offset] === end) throw new Error('JSON 不允许尾随逗号')
        }
        offset++
      } else if (token === '"') readString()
      else {
        const match = /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(source.slice(offset))
        if (!match) throw new Error('JSON 值无效')
        offset += match[0].length
      }
      node.end = offset
      return node
    }
    this.root = read(0); space()
    if (offset !== source.length) throw new Error('只能输入一个 JSON 文档')
  }

  /** Locate a diagnostic or editor field using its RFC 6901 pointer. */
  locate(pointer: string): JsonNode | undefined {
    let node: JsonNode | undefined = this.root
    if (!pointer) return node
    if (!pointer.startsWith('/')) return undefined
    for (const part of pointer.slice(1).split('/')) node = node?.children.get(part.replace(/~1/g, '/').replace(/~0/g, '~'))
    return node
  }

  /** Read exact JSON, including long integers, nulls and original array order. */
  read(pointer: string): string | undefined {
    const node = this.locate(pointer)
    return node && this.source.slice(node.start, node.end)
  }

  /** Replace one field while keeping all unrelated source spans untouched. */
  set(pointer: string, value: string): string {
    new ProtocolDocument(value)
    const node = this.locate(pointer)
    if (node) return this.source.slice(0, node.start) + value + this.source.slice(node.end)
    const separator = pointer.lastIndexOf('/')
    const parent = this.locate(pointer.slice(0, separator))
    if (!parent || parent.kind !== 'object') throw new Error('字段父节点必须是对象')
    const key = pointer.slice(separator + 1).replace(/~1/g, '/').replace(/~0/g, '~')
    const addition = `${parent.children.size ? ',' : ''}\n${JSON.stringify(key)}: ${value}\n`
    return this.source.slice(0, parent.end - 1) + addition + this.source.slice(parent.end - 1)
  }
}
