/* 青簡 QINGJIAN · 六味一义（纸/夜双主题见 App.vue Token）。
 * CATS 四段 = 前 4 色(缓存读/缓存写/新增输入/输出)，后 2 色给推理/实时；
 * 同屏不过六，第七类并入墨色。禁止在别处内联色值。 */
export const MODEL_COLORS = [
  '#B0D2B0', // 缓存读 · 石绿浅 (chart-cache)
  '#779E77', // 缓存写 · 石绿 (c-cache)
  '#489499', // 新增输入 · 石青 (c-input)
  '#B75E66', // 输出 · 朱砂 (c-output)
  '#C69C68', // 推理 · 藤黄
  '#9C91B6'  // 实时 · 藕荷
]

/* CATS 四段色 = MODEL_COLORS[0..3] */
export const CAT_COLORS: readonly [string, string, string, string] = [
  MODEL_COLORS[0], MODEL_COLORS[1], MODEL_COLORS[2], MODEL_COLORS[3]
]
