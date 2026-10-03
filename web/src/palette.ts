/* Ember Instrument 分类色板 —— 唯一字面量源(圆桌终裁 s4e1/s3e1)。
 * 6 色封顶、禁第七色;CATS 四段 = 前 4 色,后 2 色给模型边界/中性。
 * pricing.ts(CATS) 与 charts.ts(MODEL_COLORS) 均从本模块取色,禁双源内联。
 * charts.ts 已 import pricing.ts,若 pricing 反向 import charts 会成环,故独立本模块。 */
export const MODEL_COLORS = ['#E8B45A', '#C98A2E', '#B07D62', '#8A9B6E', '#6E8B9B', '#C96F4A']

/* CATS 四段色 = MODEL_COLORS[0..3](缓存读/缓存写/新增输入/输出) */
export const CAT_COLORS: readonly [string, string, string, string] = [
  MODEL_COLORS[0], MODEL_COLORS[1], MODEL_COLORS[2], MODEL_COLORS[3]
]
