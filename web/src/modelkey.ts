/* 模型 id 归一化 —— 单独成模块，因为 range.ts 与 pricing.ts 都要用。
 *
 * 放在 pricing.ts 里会让 range.ts 反向依赖计价核心，而 range.ts 刻意不依赖它
 * （文件头就写着「纯函数、无依赖 pricing 内部」）；两份拷贝则必然漂移。
 */

const CANON_PREFIXES = [
  'azure-', 'openai/', 'kimi/', 'moonshotai/', 'moonshot-', 'x-ai/', 'z-ai/',
  'google/', 'anthropic/', 'deepseek/', 'qwen/', 'zai-org/', 'minimax/',
  'xiaomi/', 'stepfun/', 'subconscious/', 'bothub/', 'modelis/', 'greenpt/'
]
/* 顺序与 Go 的 ReplaceAllString 调用顺序一致：先 int，再 flag，再日期，再 preview/exp */
const RE_TRAILING_INT = /--?int$/g
const RE_TRAILING_FLAG = /--?(ga|preview|exp|latest|beta)[-_]?\d*$/g
const RE_TRAILING_DATE = /-\d{6}$/g
const RE_TRAILING_EXP = /-(preview|exp)$/g

/**
 * 模型 id 归一化，镜像 Go 侧 clisession.CanonicalModel / pi_common.canonical_model。
 * pi 的 model_canon 已在入库时归一，这里用它对齐外部工具（Codex/Claude Code 等）
 * 的原始 id，免得同一模型在模型视图里出现多行。
 * 空值按 Go 侧约定返回 '(unknown)'；null/undefined 一并视作空。
 *
 * 前缀表与内部/clisession/session.go 的 prefixes 逐条一致。两边必须同改，否则
 * 同一模型会裂成两行。
 */
export function canonicalModelKey(modelID: string | null | undefined): string {
  if (modelID === null || modelID === undefined || modelID === '') return '(unknown)'
  let s = String(modelID).trim().toLowerCase()
  let changed = true
  while (changed) {
    changed = false
    for (const p of CANON_PREFIXES) {
      if (s.indexOf(p) === 0) { s = s.slice(p.length); changed = true }
    }
  }
  s = s.replace(RE_TRAILING_INT, '')
  s = s.replace(RE_TRAILING_FLAG, '')
  s = s.replace(RE_TRAILING_DATE, '')
  s = s.replace(RE_TRAILING_EXP, '')
  if (s === '') return String(modelID).trim().toLowerCase()
  return s
}