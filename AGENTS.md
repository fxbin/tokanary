# AGENTS.md

Tokanary：本地 AI 用量与费用看板（Go 管线 + Wails 桌面 + Vue UI）。

## 构建与门禁

```bash
gofmt -l .
go vet -buildvcs=false ./...
go test -buildvcs=false ./...

cd web && npm test && npm run build
```

提交前必须上述全绿。`-buildvcs=false` 仅用于无 git 历史时的本地构建；有正常仓库后可省略。

## 架构约定

- `data/` 只放**入库声明**（目前仅 `data/adapters/*.json`）
- `.cache/` 一律是**运行时产物**（仓库 sqlite、价格表、dashboard.json、collect-state 等），禁止入库
- 单价只用 **models.dev**（`.cache/prices-raw.json`，由 `tokanary prices` 生成）；无网关价源
- 桌面窗口经 `GET /api/dashboard` **直读仓库**，不生成 `data.js`
- 采集计费三条铁律（适配器层已内置，勿破坏）：
  1. `input` 只算非缓存输入
  2. `output` 已含 reasoning，不重复相加
  3. 累计值要差分，不能直接累加

## 常用命令

| 命令 | 作用 |
|---|---|
| `tokanary refresh` | 全量采集 + 重建仓库 + 构建前端 |
| `tokanary collect` | 只采集外部 CLI（源文件未变则复用；`--full` 全量） |
| 桌面 `/api/dashboard` | **读取时增量刷新**（`internal/refresh.Touch`）：适配器 mtime + pi.sqlite 指纹 |
| `tokanary prices` | 生成 `.cache/prices-raw.json`。**每 7 天自动重抓**，随 `collect`/`refresh`/桌面轮询顺带完成 |
| `tokanary prices --status` | 只看价表新鲜度，不联网 |
| `tokanary prices --force` | 手动立刻重抓（旧表先写 `.bak`） |
| `tokanary build` | 重建仓库并写 `.cache/dashboard.json` |

## 布局

```
cmd/tokanary/     CLI
desktop.go        Wails 窗口 + /api/dashboard
internal/
  sources/        声明式适配器
  pidata/         pi.sqlite 读取
  clisession/     pi CLI JSONL
  warehouse/      SQLite 聚合
  pricing/        models.dev 匹配与装配
  dashboard/      看板 JSON 装配
  webui/          静态资源服务
web/              Vue3 桌面 UI（render/ 分区块）
data/adapters/    适配器声明
testdata/         合成夹具（勿当真实数据）
```

## 设计语言

- **现行标准见 [DESIGN.md](DESIGN.md)（青簡）**，改 UI 前必读
- Token：`web/src/styles/tokens.css`；壳：`shell.css`；内容组件：`ui.css`
- 分类色唯一源 `web/src/palette.ts`；禁止内联 hex

## 前端布局

```
web/src/
  App.vue           壳装配 + 事件委托
  composables/      useTheme / useHashRoute / useDashboard / usePriceIo
  styles/           tokens.css · shell.css · ui.css
  render/           分区块 HTML
  pricing.ts        计价核心
  range.ts          范围聚合
  palette.ts · charts.ts
```

## 提交信息

- **不用中文破折号**（U+2014，成对或落单都不用）。连接分句用「，」，引出结论或清单用「：」，需要停顿就另起一句。本条不举字符实例：把被禁的符号写进规范，等于让规范先破一次自己的规矩，检测用码位即可。范围覆盖 commit subject 与 body，以及本文档正文。
- 数字区间用 en dash（U+2013，例：16–1024）；命令行参数里的 ASCII 双连字符（`--force`、`--status`）原样保留；不得用三个及以上的连字符画分隔线。
- subject 沿用 `type(scope): 说明`，说明写成**一句自然语言**：像说话那样讲清改了什么、为什么，不靠标点符号表达两半的关系，也不写压缩到读不通的行话。
- 正文一行一事。

## 禁止

- 提交 `.cache/`、`web/dist/`、`node_modules/`、二进制、本机价目/网关文件
- 用 `git add -f` 绕过 `.gitignore`
- 在适配器里改计费口径语义（须走铁律与测试）
