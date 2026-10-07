// Prism 语法子集加载 + 高亮入口：只为会话代码块引入常用语言，未加载的
// 语言回退为纯转义文本（仍保留代码块头栏与复制按钮）。
import Prism from 'prismjs'
import 'prismjs/components/prism-javascript'
import 'prismjs/components/prism-typescript'
import 'prismjs/components/prism-python'
import 'prismjs/components/prism-bash'
import 'prismjs/components/prism-go'
import 'prismjs/components/prism-sql'
import 'prismjs/components/prism-yaml'
import 'prismjs/components/prism-json'
import 'prismjs/components/prism-diff'
import 'prismjs/components/prism-css'
import 'prismjs/components/prism-markup'
import 'prismjs/components/prism-markdown'

/** 围栏语言标记 → Prism 语法名。 */
const LANGUAGE_ALIASES: Record<string, string> = {
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  ts: 'typescript', tsx: 'typescript',
  sh: 'bash', shell: 'bash', zsh: 'bash', console: 'bash',
  py: 'python', python3: 'python',
  yml: 'yaml',
  html: 'markup', xml: 'markup', svg: 'markup', vue: 'markup',
  golang: 'go',
  patch: 'diff',
  md: 'markdown',
}

function escapeHTML(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** 返回 HTML 片段；未知语言返回转义后的纯文本。 */
export function highlightCode(code: string, language: string): string {
  const name = LANGUAGE_ALIASES[language] ?? language
  const grammar = Prism.languages[name]
  if (!grammar) return escapeHTML(code)
  return Prism.highlight(code, grammar, name)
}
