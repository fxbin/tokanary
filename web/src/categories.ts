/**
 * 任务类别 · 确定性启发式（不调用 LLM）。
 * 依据会话标题 / 工具名 / 常见意图词，映射到有限类别；同屏色仍遵守六味上限。
 */
export type TaskCat =
  | 'coding' | 'debug' | 'research' | 'write' | 'plan' | 'review' | 'other'

export const TASK_CATS: { k: TaskCat; label: string }[] = [
  { k: 'coding', label: '编码' },
  { k: 'debug', label: '调试' },
  { k: 'research', label: '研究' },
  { k: 'write', label: '写作' },
  { k: 'plan', label: '方案' },
  { k: 'review', label: '评审' },
  { k: 'other', label: '其他' }
]

const RULES: { k: TaskCat; re: RegExp }[] = [
  { k: 'debug', re: /debug|bug|crash|error|fail|troubleshoot|报错|崩溃|排查|修复|fix\s/i },
  { k: 'review', re: /review|audit|check|评审|审查|检查|验收/i },
  { k: 'research', re: /research|study|analyze|compare|调研|研究|分析|对标|拆解/i },
  { k: 'write', re: /write|doc|article|draft|novel|小说|文章|文案|写作|剧本/i },
  { k: 'plan', re: /plan|design|spec|roadmap|architecture|方案|设计|规划|架构|sop/i },
  { k: 'coding', re: /code|impl|refactor|feature|build|coding|实现|重构|开发|写码|组件/i }
]

export function classifyTask(text: string, tool?: string): TaskCat {
  const t = (text || '') + ' ' + (tool || '')
  for (const r of RULES) {
    if (r.re.test(t)) return r.k
  }
  // 工具默认：CLI 编码工具更偏 coding
  const tl = (tool || '').toLowerCase()
  if (/codex|claude|cursor|zcode|opencode|cline|gemini|copilot|droid/.test(tl)) return 'coding'
  return 'other'
}

export interface CatAgg {
  cat: TaskCat
  label: string
  count: number
  tokens: number
}

/** 按会话标题/模型行聚类；tokens 可选。 */
export function aggregateCats(rows: { title?: string; tool?: string; tokens?: number }[]): CatAgg[] {
  const map: Record<string, CatAgg> = {}
  for (const c of TASK_CATS) {
    map[c.k] = { cat: c.k, label: c.label, count: 0, tokens: 0 }
  }
  for (const r of rows || []) {
    const cat = classifyTask(r.title || '', r.tool)
    map[cat].count++
    map[cat].tokens += Number(r.tokens || 0)
  }
  return TASK_CATS.map((c) => map[c.k]).filter((x) => x.count > 0 || x.tokens > 0)
}
