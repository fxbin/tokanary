/* dayChart 的 X 轴标签抽稀回归。
 * 旧稿 days.forEach 里每天都画一个 <text>，range=all 下 201 天挤成
 * 「1/2/2/2/2/2…」一团糊字。这里锁三件事：稀疏时不减标签、稠密时按宽度抽稀、
 * 任何两个保留标签的间距都不小于一个标签的占宽（这是抽稀真正要保证的东西）。 */
import { describe, it, expect } from 'vitest'
import { dayChart, dateShort, stackBar, textUnits, type DayRow } from './charts'

/* dayChart 的 X 轴文字都落在 y = h-12 = 248；Y 轴刻度虽然也带 .axis，
 * 但 y 落在绘图区内，用 y="248" 就能只摘出 X 轴标签。 */
const AXIS_Y = 248

function xLabels(days: DayRow[]): Array<{ x: number; t: string }> {
  const html = dayChart(days, {})
  const out: Array<{ x: number; t: string }> = []
  const re = /<text x="(-?[\d.]+)" y="248"[^>]*class="axis">([^<]*)<\/text>/g
  let m: RegExpExecArray | null
  while ((m = re.exec(html)) !== null) out.push({ x: Number(m[1]), t: m[2] })
  return out
}

function mkDays(n: number, startDay = 27): DayRow[] {
  const out: DayRow[] = []
  const base = Date.UTC(2025, 11, startDay)
  for (let i = 0; i < n; i++) {
    const d = new Date(base + i * 86400000).toISOString().slice(0, 10)
    out.push({ d, total: 1000 + i, cacheRead: 800, cacheWrite: 50, input: 100, output: 50 })
  }
  return out
}

describe('dayChart X 轴标签抽稀', () => {
  it('7 天：一天一个标签，一个都不减', () => {
    const days = mkDays(7)
    const ls = xLabels(days)
    expect(ls.length).toBe(7)
    expect(ls.map((l) => l.t)).toEqual(days.map((d) => dateShort(new Date(d.d + 'T00:00:00').getTime())))
  })

  it('1 天 / 2 天：退化输入不崩，仍有首尾标签', () => {
    expect(xLabels(mkDays(1)).length).toBe(1)
    const two = xLabels(mkDays(2))
    expect(two.length).toBe(2)
    expect(two[0].t).not.toBe(two[1].t)
  })

  it('201 天：标签数从 201 降到两位数，且首尾都在', () => {
    const days = mkDays(201)
    const ls = xLabels(days)
    expect(ls.length).toBeLessThan(30)
    expect(ls.length).toBeGreaterThan(4)
    expect(ls[0].t).toBe(dateShort(new Date(days[0].d + 'T00:00:00').getTime()))
    expect(ls[ls.length - 1].t).toBe(dateShort(new Date(days[200].d + 'T00:00:00').getTime()))
  })

  it('任意天数：相邻保留标签的间距都不小于一个标签的占宽', () => {
    // 抽稀的全部意义就是「不重叠」。5 字符日期在 .axis 10.5px 下约 5.5/字 + 12 间隙
    for (const n of [7, 30, 60, 201, 365, 800]) {
      const ls = xLabels(mkDays(n))
      expect(ls.length).toBeGreaterThan(0)
      for (let i = 1; i < ls.length; i++) {
        expect(ls[i].x - ls[i - 1].x, 'n=' + n + ' 第 ' + i + ' 个标签重叠').toBeGreaterThanOrEqual(38)
      }
    }
  })

  it('标签数受绘图区宽度封顶，远低于天数（抽稀是真省，不是换个地方重画）', () => {
    // 824 单位宽 / 39.5 单位标签 ≈ 20 个，再加首尾两个余量。
    const capacity = Math.floor(824 / 39.5) + 2
    for (const n of [7, 30, 60, 120, 201, 365, 800]) {
      const ls = xLabels(mkDays(n))
      expect(ls.length, 'n=' + n).toBeLessThanOrEqual(capacity)
      // 一天一个标签在宽窗里是对的（slot 装得下），不该被无脑砍掉
      expect(ls.length, 'n=' + n).toBeGreaterThan(1)
    }
  })

  it('末位标签不与前一个挤在一起（n-1 撞前一位时让掉前一位）', () => {
    // n=30 时步长 2、末位落在奇数索引，天然紧挨着网格点 28；不处理就画出 "1/24 1/25"
    for (const n of [30, 60, 121, 202]) {
      const ls = xLabels(mkDays(n))
      expect(ls[ls.length - 1].t, 'n=' + n + ' 末位应是真最后一天')
        .toBe(dateShort(new Date(mkDays(n)[n - 1].d + 'T00:00:00').getTime()))
      if (ls.length > 1) {
        expect(ls[ls.length - 1].x - ls[ls.length - 2].x, 'n=' + n + ' 末两位重叠').toBeGreaterThanOrEqual(38)
      }
    }
  })
})

/* stackBar 的标签栏与右侧数值栏曾经写死宽度（132/150/160/170 各不相同），
   而 .chart 是 overflow: visible —— 短的名字把长的挤出去，溢出的文字不会
   被裁掉，只会盖在柱子上（真实数据：DeepSeek Harness（DSH Desktop）需 211 单位，
   旧写法只给 132，柱子正好压住标签中间）。 */
describe('stackBar：按内容实测栏宽', () => {
  const seg = (v: number) => [{ v, color: 'var(--chart-c0)', name: 's' }]

  it('标签栏容得下最长标签，不截断也不压柱', () => {
    const label = 'DeepSeek Harness（DSH Desktop）'
    const html = stackBar([{ label, total: 100, totalText: '100', segments: seg(100) }], { labelW: 132 })
    const x = Number((html.match(/<rect x="([\d.]+)"/) || [])[1])
    expect(textUnits(label, 12.5) + 12).toBeLessThanOrEqual(x)
    expect(html).not.toContain('…')            // 未触发截断
    expect(x).toBeGreaterThan(132)             // 不是写死的 132
  })

  it('副标题（更长的那行）也算进标签栏宽度', () => {
    const html = stackBar([
      { label: 'pi', sub: '9.78B · $5,398.07', total: 100, totalText: '100', segments: seg(100) }
    ], { labelW: 132 })
    const x = Number((html.match(/<rect x="([\d.]+)"/) || [])[1])
    expect(textUnits('9.78B · $5,398.07', 10.5) + 12).toBeLessThanOrEqual(x)
  })

  it('最长条的右端不越过右侧数值栏起点', () => {
    const items = [
      { label: 'pi', total: 978, totalText: '9.78B · $5,398.07', segments: seg(978) },
      { label: 'b', total: 1, totalText: '1', segments: seg(1) }
    ]
    const html = stackBar(items)
    // 最大那行的条从 labelW 起、长度 = plotW，终点必须 ≤ 900 - 数值占宽 - 留白
    const x = Number((html.match(/<rect x="([\d.]+)"/) || [])[1])
    const firstWidth = Number((html.match(/<rect x="[\d.]+" y="[\d.]+" width="([\d.]+)"/) || [])[1])
    expect(x + firstWidth).toBeLessThanOrEqual(900 - textUnits('9.78B · $5,398.07', 12) - 12)
  })

  it('整行量级为 0 时不画那根灰竖标（有量级但无分段才画）', () => {
    const zero = stackBar([{ label: 'x', total: 0, totalText: '0', segments: [] }])
    expect(zero).not.toMatch(/fill="var\(--dim\)" opacity="\.4"/)
    const noSegButScaled = stackBar([
      { label: 'a', total: 10, totalText: '10', segments: seg(10) },
      { label: 'b', total: 5, totalText: '5', segments: [] }
    ])
    expect(noSegButScaled).toMatch(/fill="var\(--dim\)" opacity="\.4"/)
  })
})
