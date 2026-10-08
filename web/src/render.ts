/* 页面装配入口。
 *
 * 这里只做 re-export：每个 Tab 的 HTML 在 render/ 下自己拼，App.vue 负责
 * 壳装配与事件委托。旧的整页构造器 renderPage 已经删除 —— 它没有任何生产
 * 调用方（App.vue 从不调它），只被它自己的测试撑着，输出的是一套早就和真实
 * 界面脱节的区块标题，那批 markers 还因此空转过（见 git 历史）。想加区块改
 * 对应的 render/*.ts，不要在这里重新堆一个整页渲染器。
 */
export type { UiState } from './render/shared'
export { defaultUiState, esc } from './render/shared'
export { renderOverview } from './render/overview'
export { renderModels } from './render/models'
export { renderSessions } from './render/sessions'
export { budgetAlertHtml, BUDGET_TIERS, renderProjects } from './render/projects'
export { renderSettings } from './render/settings'