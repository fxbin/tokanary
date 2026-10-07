/* Slice3c 后自包含定价测试:手算固件 + 真实数据不变式,不再依赖仓根 app.js。
 * 用法: npm test (web/ 目录下) */
import { describe, it, expect } from 'vitest'
import {
  computeAll, compareSources, dailyCost, modelCost, externalSummary,
  normalizeCost, normalizeCustomMap, defaultOpts, canonicalModelKey,
  money, fmt, type Cost4
} from './pricing'
import { hasCurated, hasGateway, loadDashboardFixture } from './testsupport'

const DATA = loadDashboardFixture()
const live = DATA ? describe : describe.skip

const CLOSE = 1e-6
const near = (a: number, b: number) => expect(Math.abs(a - b)).toBeLessThan(CLOSE)

/* 手算固件:1M 缓存读 + 2M 缓存写 + 3M 新增输入 + 4M 输出,单价 0.4/5/4/20
 * 期望 = 1*0.4 + 2*5 + 3*4 + 4*20 = 102.4 */
function fixtureData(cost: Cost4) {
  return {
    totals: { cacheRead: 1000000, cacheWrite: 2000000, input: 3000000, output: 4000000 },
    models: [{
      key: 'demo', rawIds: ['demo'], turns: 1, missingUsage: 0, statuses: {},
      cacheRead: 1000000, cacheWrite: 2000000, input: 3000000, output: 4000000,
      reasoning: 0, total: 10000000
    }],
    days: [],
    dayModel: [],
    pricing: { demo: { target: 'demo', cost } }
  }
}

describe('pricing core', () => {
  it('手算对照:四项求和 == 102.4', () => {
    const d = fixtureData({ input: 4, output: 20, cache_read: 0.4, cache_write: 5 })
    const r = modelCost(d.models[0], d, defaultOpts())
    expect(r.priced).toBe(true)
    near(r.total, 102.4)
    near(r.parts.cacheRead, 0.4)
    near(r.parts.cacheWrite, 10)
    near(r.parts.input, 12)
    near(r.parts.output, 80)
  })

  it('缺价策略:ratio10 按 0.1/1.25,zero 按 0', () => {
    const d = fixtureData({ input: 4, output: 20, cache_read: null, cache_write: null })
    const ratio = modelCost(d.models[0], d, defaultOpts()).total
    near(ratio, 1 * 0.4 + 2 * 5 + 3 * 4 + 4 * 20)
    const zero = modelCost(d.models[0], d, { ...defaultOpts(), policy: 'zero' }).total
    near(zero, 0 + 0 + 12 + 80)
  })

  it('未定价模型不计金额且拉低覆盖率', () => {
    const d = fixtureData({ input: 4, output: 20, cache_read: 0.4, cache_write: 5 })
    d.models.push({
      key: 'ghost', rawIds: ['ghost'], turns: 1, missingUsage: 0, statuses: {},
      cacheRead: 0, cacheWrite: 0, input: 1000000, output: 0, reasoning: 0, total: 1000000
    })
    const s = computeAll(d, defaultOpts())
    near(s.totalCost, 102.4)
    expect(s.unpriced.length).toBe(1)
    near(s.coverage, 10000000 / 11000000 * 100)
  })

  it('真实数据:双口径都有金额且不同', () => {
    if (!DATA) return
    // This compares two price SOURCES, so it only means anything when both can
    // price the same models. .cache/prices-raw.json is local-only (model aliases), and
    // `tokanary prices` only regenerates the former. A fresh clone therefore
    // has neither, and one holding only a gateway table prices every model
    // through that gateway - both totals are then identical by construction.
    if (!hasCurated(DATA) || !hasGateway(DATA)) {
      expect(compareSources(DATA, defaultOpts()).gateway).toBe(
        compareSources(DATA, defaultOpts()).modelsdev)
      return
    }
    const c = compareSources(DATA, defaultOpts())
    expect(c.gateway).toBeGreaterThan(0)
    expect(c.modelsdev).toBeGreaterThan(0)
    expect(c.gateway).not.toBe(c.modelsdev)
  })

  it('真实数据:总额 == 各模型之和,按天合计 == 总额', () => {
    if (!DATA) return
    const s = computeAll(DATA, defaultOpts())
    near(s.rows.reduce((t: number, r: any) => t + r.cost, 0), s.totalCost)
    const byDay = dailyCost(DATA, s)
    near(Object.keys(byDay).reduce((t, k) => t + byDay[k], 0), s.totalCost)
    expect(s.totalTokens).toBe(DATA.totals.total)
  })

  it('自定义优先级:手动 > 自定义 > 口径', () => {
    const d = fixtureData({ input: 4, output: 20, cache_read: 0.4, cache_write: 5 })
    const custom = { demo: { input: 1, output: 2, cache_read: 0.1, cache_write: 0.2 } } as any
    const hit = computeAll(d, { ...defaultOpts(), customPrices: custom })
      .rows.find((r: any) => r.m.key === 'demo')
    expect(hit.price.source).toBe('custom')
    const manual = computeAll(d, {
      ...defaultOpts(), customPrices: custom,
      overrides: { demo: { input: 9, output: 9, cache_read: 0.9, cache_write: 0.9 } }
    }).rows.find((r: any) => r.m.key === 'demo')
    expect(manual.price.source).toBe('manual')
    expect(manual.cost).not.toBe(hit.cost)
  })

  it('normalizeCustomMap 两种形态 + 非法输入', () => {
    expect(normalizeCustomMap(null)).toBeNull()
    expect(normalizeCustomMap({})).toBeNull()
    const raw = normalizeCustomMap({ models: [{ target: 'gpt-5.6-sol', cost: { input: 1, output: 2 } }] })
    expect(raw && raw['gpt-5.6-sol'] && raw['gpt-5.6-sol'].input).toBe(1)
    const flat = normalizeCustomMap({ 'kimi-k3': { input: 3, output: 15 } })
    expect(flat && flat['kimi-k3'] && flat['kimi-k3'].output).toBe(15)
  })

  it('money/fmt 烟雾', () => {
    expect(money(0)).toBe('$0')
    expect(money(1234.5)).toBe('$1,234.50')
    expect(fmt(1234567)).toBe('1.23M')
    expect(normalizeCost(null, 'ratio10').ok).toBe(false)
  })
})

/* 外部工具夹具:一个缓存占 98.9% 的模型。
 * 单价 input 4 / output 20 / cache_read 0.2 / cache_write 5(USD/1M)。
 * 真值 = 1M×4 + 100M×0.2 + 0.1M×20 = 26;四档算术平均 (4+20+0.2+5)/4 = 7.3
 * 会把同一个模型算成 738 —— 高出 28 倍。 */
function extFixture(cost: any = { input: 4, output: 20, cache_read: 0.2, cache_write: 5 }) {
  const m = {
    id: 'Big-Cache', input: 1000000, cacheRead: 100000000, cacheWrite: 0,
    output: 100000, reasoning: 50000, total: 101100000,
    price: { source: 'models.dev', cost }
  }
  return {
    external: {
      generatedAt: '2026-10-01', totals: { sessions: 1, calls: 1 },
      tools: [{
        tool: 'codex', label: 'Codex CLI', sessions: 1, calls: 1,
        input: m.input, cacheRead: m.cacheRead, cacheWrite: 0, output: m.output,
        reasoning: 0, total: m.total, models: [m],
        days: [{
          d: '2026-10-01', total: m.total, input: m.input,
          cacheRead: m.cacheRead, cacheWrite: 0, output: m.output, reasoning: 0
        }]
      }]
    }
  }
}

describe('external pricing by composition', () => {
  it('外部模型按真实四段构成计价,远低于四价算术平均', () => {
    const s = externalSummary(extFixture() as any, 'ratio10')!
    const row = s.rows[0].models[0]
    expect(row.priced).toBe(true)
    near(row.cost, 26)                                    // 4 + 20 + 2
    near(row.unit, 26 / row.tok.total * 1e6)              // $/1M 综合均价,按构成加权
    const mean = (4 + 20 + 0.2 + 5) / 4                    // 错的算法:四档单价算术平均
    expect(mean * row.tok.total / 1e6).toBeGreaterThan(row.cost * 10)
  })

  it('外部模型缺缓存单价走既有 POLICIES,不另立口径', () => {
    // 只有 input:ratio10 → cache_read = 0.4、cache_write = 5;zero → 两者 0
    const ratio = externalSummary(extFixture({ input: 4, output: 20 }) as any, 'ratio10')!
    near(ratio.rows[0].models[0].cost, 4 + 40 + 2)
    const zero = externalSummary(extFixture({ input: 4, output: 20 }) as any, 'zero')!
    near(zero.rows[0].models[0].cost, 4 + 0 + 2)
    // 缺 input 单价 = 无从推算,整行不计钱
    const noInput = externalSummary(extFixture({ output: 20, cache_read: 0.2 }) as any, 'ratio10')!
    expect(noInput.rows[0].models[0].priced).toBe(false)
    near(noInput.rows[0].models[0].cost, 0)
  })

  it('pi 侧 unit 同为构成加权(参照实现),不是四价均值', () => {
    const cost = { input: 4, output: 20, cache_read: 0.2, cache_write: 5 }
    const d: any = {
      totals: { cacheRead: 100000000, cacheWrite: 0, input: 1000000, output: 100000 },
      models: [{
        key: 'big-cache', rawIds: ['Big-Cache'], turns: 1, missingUsage: 0, statuses: {},
        cacheRead: 100000000, cacheWrite: 0, input: 1000000, output: 100000,
        reasoning: 0, total: 101100000
      }],
      days: [], dayModel: [], pricing: { 'big-cache': { target: 'big-cache', cost } }
    }
    const r = computeAll(d, defaultOpts()).rows[0]
    near(r.cost, 26)
    near(r.unit, 26 / 101100000 * 1e6)
    expect(7.3 * 101.1).toBeGreaterThan(r.cost * 10)
  })
})

describe('canonicalModelKey', () => {
  it('与 Go 侧 clisession.CanonicalModel 逐条对齐', () => {
    const cases: Array<[string, string]> = [
      ['azure-gpt-5.6-sol', 'gpt-5.6-sol'],
      ['kimi/kimi-k3', 'kimi-k3'],
      ['gpt-5.6-sol--int', 'gpt-5.6-sol'],
      ['deepseek-v4-flash-ga-260731', 'deepseek-v4-flash'],
      ['gpt-5.6-sol', 'gpt-5.6-sol'],
      ['Deepseek-v4-flash', 'deepseek-v4-flash'],
      ['deepseek/deepseek-v4.1-flash', 'deepseek-v4.1-flash'],
      ['openai/gpt-5.6-sol-preview', 'gpt-5.6-sol'],
      ['moonshotai/kimi-k3-260101', 'kimi-k3'],
      ['  GLM-5.3  ', 'glm-5.3'],
      ['', '(unknown)']
    ]
    cases.forEach(function (c) { expect(canonicalModelKey(c[0])).toBe(c[1]) })
    expect(canonicalModelKey(null as any)).toBe('(unknown)')
  })
})
