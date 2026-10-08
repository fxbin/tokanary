# Tokanary

> Every token, accounted for.

本地 AI 用量与费用看板：读取常见 Harness 工具的 token 账单，按模型统计，并用 [models.dev](https://models.dev) 公开单价估算花费。

- **客户端形态**：原生窗口（Wails），不是静态站
- 数据来自本地 SQLite 仓库，窗口内直读
- 支持 pi / Claude Code / Codex / OpenCode / DeepSeek Harness 等

---

## 快速开始

```bash
# 1. 采集 + 建仓库 + 构建界面
go build -buildvcs=false -o tokanary ./cmd/tokanary
./tokanary refresh

# 2. 生成价格表（否则金额为 0，属预期）
./tokanary prices

# 3. 打开桌面窗口
go build -buildvcs=false -o tokanary-desktop .
./tokanary-desktop
```

日常使用：跑 `tokanary refresh` 后在窗口里点「重新读取」，或重启 `tokanary-desktop`。

---

## 常用命令

| 命令 | 作用 |
|---|---|
| `tokanary refresh` | 采集、重建仓库、写 `.cache/dashboard.json`、构建前端 |
| `tokanary prices` | 从 models.dev 生成价格表 |
| `tokanary schedule` | 注册定时刷新（默认 60 分钟；`30` 改间隔；`--remove` 移除） |
| `tokanary collect` | 只采集其他 CLI |
| `tokanary build` | 只重建仓库并写 dashboard JSON |
| `tokanary warehouse` | 仓库导出（口径校验） |

---

## 数据流

```
pi / 其他 CLI 本地日志
        │
        ▼
.cache/tokanary.sqlite     ← 分析仓库（唯一数据源）
        │
        ├─► 窗口 GET /api/dashboard  → Vue 界面
        └─► tokanary build → .cache/dashboard.json（测试/离线）
```

窗口每次读取的都是仓库当前状态。

---

## 其他 CLI

Claude Code / Codex / OpenCode / DeepSeek Harness 由声明式适配器采集，`refresh` 时自动合并。

**加一个新工具 = 在 `data/adapters/` 放一个 JSON，不改代码。**

字段映射与口径规则见 [`data/adapters/README.md`](data/adapters/README.md)。

---

## 计费口径

```
费用 = 缓存读 × 读单价
     + 缓存写 × 写单价
     + 新增输入 × 输入单价
     + 输出 × 输出单价
```

三条铁律（适配器层已内置）：

1. **`input` 只算非缓存输入**
2. **`output` 已含 reasoning**，不重复相加
3. **累计值要差分**，不能直接累加

金额是 **models.dev 公开价** 下的估算，不是合同结算价。缓存单价缺失时按页面「缺价策略」推算。

支持手动改价、自定义价格 JSON，优先级：手动 > 自定义 > models.dev。

---

## 项目结构

```
cmd/tokanary/          CLI：refresh / prices / collect / build / warehouse / schedule
desktop.go             桌面窗口入口（Wails + /api/dashboard）
internal/
  dashboard/           从仓库装配看板数据
  pidata/              读 pi.sqlite + 一致性快照
  clisession/          官方 pi CLI JSONL 解析
  sources/             声明式适配器引擎
  warehouse/           SQLite 聚合仓库
  pricing/             价格解析与合并
  webui/               静态资源服务（桌面 WebView）
web/                   Vite + Vue3 界面（桌面 UI）
data/adapters/         适配器声明（JSON）
.cache/                运行时产物：仓库、价格表、dashboard.json 等（不入库）
```

约定：`data/` 只放声明与配置；中间产物一律在 `.cache/`。

---

## 开发与自检

```bash
# 后端
gofmt -l .
go vet -buildvcs=false ./...
go test -buildvcs=false ./...

# 前端（真实数据用例需要先 refresh / build 出 .cache/dashboard.json）
cd web
npm install
npm test
npm run build
```

### 门禁说明

| 测试 | 作用 | 状态 |
|---|---|---|
| `go test ./...` | 采集 / 聚合 / 路径安全 / 装配 | 通过 |
| `TestFrozenParity` | 与冻结 fixture 比对口径 | 需 `.cache/frozen/` |
| `TestAdapterParity` | 外部适配器一致性 | 暂跳过 |
| `web/npm test` | 定价 / 渲染 / 范围聚合 | 通过（无 fixture 时跳过真实数据用例） |

---

## 已知限制

- 工具调用**内容**未落库，只能统计次数
- reasoning 已含在 output 中，仅作展示
- 无用量的中断回合不计费
- 同一模型的不同路由 id 会归一化合并
- Windows 之外 `schedule` 只打印 crontab 示例，不自动注册

---

## 更多

| 文档 | 内容 |
|---|---|
| [`data/adapters/README.md`](data/adapters/README.md) | 适配器规范、字段映射、口径铁律 |
| [`DESIGN.md`](DESIGN.md) | 界面设计语言与诚实性约束 |
| [`AGENTS.md`](AGENTS.md) | 代码库约定与提交门禁 |
| `tokanary -h` | 全部命令与参数 |

---

## 许可证

[MIT](LICENSE)
