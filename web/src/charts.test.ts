/* dayChart 的 X 轴标签抽稀回归。
 * 旧稿 days.forEach 里每天都画一个 <text>，range=all 下 201 天挤成
 * 「1/2/2/2/2/2…」一团糊字。这里锁三件事：稀疏时不减标签、稠密时按宽度抽稀、
 * 任何两个保留标签的间距都不小于一个标签的占宽（这是抽稀真正要保证的东西）。 */
import { describe, it, expect } from 'vitest'
import { dayChart, dateShort, type DayRow } from './charts'

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
