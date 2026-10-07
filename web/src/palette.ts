/* 青簡 QINGJIAN · 六味一义。
 *
 * 分工（分类色唯一源 = 本文件）：
 *   本文件 —— 定义「第 i 个槽是什么语义」；
 *   tokens.css —— 给每个主题填 `--chart-c0..c5` 的值（CSS 读不到 TS，值只能落那里）。
 * 运行时图元拿的是 `var(--chart-cN)` 而非 hex，所以同一段 SVG 在纸/夜下自动换色；
 * 调用方勿在业务代码里写色值，要改色相就改语义映射与 token。
 *
 * CATS 四段 = 前 4 槽(缓存读/写、输入、输出)，后 2 槽给推理/实时；
 * 同屏不过六，第七类并入墨色。 */

/** 槽位顺序即 DESIGN.md §3.3 的图表六色，勿调换。 */
export const CHART_SLOTS = [
  'cacheRead',  // c0 缓存读 · 石绿 400 大面积档
  'cacheWrite', // c1 缓存写 · 石绿本色
  'input',      // c2 新增输入 · 石青
  'output',     // c3 输出 · 朱砂
  'reason',     // c4 推理 · 藤黄
  'realtime'    // c5 实时 · 藕荷
] as const

/* 六色（图元色槽的运行时引用；槽位色值见 tokens.css 纸/夜两块） */
export const MODEL_COLORS: readonly string[] =
  CHART_SLOTS.map(function (_sem, i) { return 'var(--chart-c' + i + ')' })

/* CATS 四段色 = MODEL_COLORS[0..3]（定价层 CATS 走同源，见 pricing.ts） */
export const CAT_COLORS: readonly [string, string, string, string] = [
  MODEL_COLORS[0], MODEL_COLORS[1], MODEL_COLORS[2], MODEL_COLORS[3]
]
