/* Slice3b 走查:整页构造器 8 区块存在性 + 总额一致 + 无泄漏 + 自定义 tag。
 * 用法: npx vitest run (web/ 目录下)。需要 .cache/dashboard.json（tokanary refresh 生成） */
import { describe, it, expect } from 'vitest'
import { computeAll, compareSources, money, defaultOpts } from './pricing'
import { renderPage, defaultUiState, missingSections } from './render'
import { hasAnyPricing, loadDashboardFixture } from './testsupport'

const DATA = loadDashboardFixture()
const suite = DATA ? describe : describe.skip

const st = defaultUiState()
const cmp = DATA ? compareSources(DATA, st) : {}

suite('renderPage walkthrough', () => {
  it('8 区块 + 输入框 + 渠道下拉 + SVG 齐全', () => {
    expect(missingSections(renderPage(DATA, st, cmp, ''), DATA)).toEqual([])
  })

  it('KPI 总额 == 逻辑层总额', () => {
    const total = money(computeAll(DATA, st).totalCost)
    expect(renderPage(DATA, st, cmp, '')).toContain(total)
  })

  it('无 undefined/NaN 泄漏', () => {
    const html = renderPage(DATA, st, cmp, '')
    expect(html).not.toMatch(/>undefined</)
    expect(html).not.toMatch(/NaN/)
  })

  it('自定义源命中时标「自定义源」', () => {
    const st2 = defaultUiState()
    st2.customPrices = { 'gpt-5.6-sol': { input: 1, output: 2, cache_read: 0.1, cache_write: 0.2 } }
    expect(renderPage(DATA, st2, cmp, '')).toContain('自定义源')
  })

  it('网关价目表列出全部模型', () => {
    const html = renderPage(DATA, st, cmp, '')
    const ids: string[] = ((DATA.gateway || {}).models || []).map((m: any) => m.id)
    const shown = ids.filter((id) => html.indexOf('>' + id + '<') >= 0).length
    expect(shown).toBe(ids.length)
  })

  it('缺 data 时给出生效提示而非抛错', () => {
    const html = renderPage(null, st, cmp, '')
    expect(html).toContain('tokanary refresh')
    expect(html).not.toContain('build_data.py')
  })

  it('缺 gateway 段时降级到 models.dev 而非抛错', () => {
    const noGw: any = JSON.parse(JSON.stringify(DATA))
    noGw.gateway = null
    const st2 = defaultUiState()
    const html = renderPage(noGw, st2, compareSources(noGw, st2), '')
    expect(html).not.toContain('>undefined<')
    expect(html).not.toMatch(/NaN/)
    expect(html).toContain('计费口径')
  })

  it('全新克隆(无任何价格表)仍渲染完整且 8 区块齐全', () => {
    // This is what a fresh clone looks like: .cache/prices-raw.json and
    // data/gateway-models.json are gitignored, so `tokanary prices` has not
    // been run yet. Token totals must still be right and every section must
    // still render - only the money collapses to zero.
    const bare: any = JSON.parse(JSON.stringify(DATA))
    bare.pricing = {}
    bare.gateway = null
    for (const m of bare.models || []) { m.cost = null }
    for (const t of ((bare.external || {}).tools || [])) {
      for (const k of Object.keys(t.models || {})) t.models[k].price = { source: 'unpriced' }
    }
    const st2 = defaultUiState()
    expect(hasAnyPricing(bare)).toBe(false)
    const html = renderPage(bare, st2, compareSources(bare, st2), '')
    expect(missingSections(html, bare)).toEqual([])
    expect(html).not.toContain('>undefined<')
    expect(html).not.toMatch(/NaN/)
    expect(html).toContain('0')
  })
})
