# 数据源适配器框架

**目标：加一个新 AI CLI = 在 `data/adapters/` 放一个 JSON 文件，不改代码。**

只有「累计值差分」「跨行状态」这类怪癖才需要写 driver（`internal/sources/drivers.go`）。
实现是「一个工具一份声明」，不是「一个工具一个解析器」。

```
data/adapters/claude-code.json ─┐
data/adapters/codex.json ───────┤
data/adapters/opencode.json ────┤
data/adapters/deepseek-harness.json ─┤
data/adapters/deepseek-harness-wrapper.json ─┼─► internal/sources ─► 规范记录 ─► data/external-usage.json ─► data.js ─► 页面
data/adapters/<你的工具>.json ──┘          ▲
                                          └── drivers.go（只有怪癖才需要）
```

Go 侧各文件职责：

| 文件 | 职责 |
|---|---|
| `manifest.go` | 声明结构体（`Manifest`）与默认值（kind 缺省 `jsonl`、`enabled` 缺省 true 等） |
| `reader.go` / `readers.go` | 遍历路径、`prefilter` 过滤、JSONL / zstd-jsonl / sqlite 三种读法 |
| `normalize.go` | 字段取值、token 口径修正、时间归一 |
| `drivers.go` | 可选的怪癖 driver（目前只有 `codex`） |
| `record.go` | 规范记录与去重键 |

---

## 1. 规范记录（唯一契约）

任何工具，无论日志长什么样，最后都要变成这一组字段：

```jsonc
{
  "tool":       "codex",              // 适配器 id
  "model":      "azure-gpt-5.6-sol",  // 模型名
  "session":    "01a0a897-...",       // 会话标识（仅用于数会话个数）
  "ts":         "2026-09-16T05:02:27.816Z",   // 可空
  "input":      68,                   // 非缓存输入
  "cacheRead":  0,                    // 缓存读
  "cacheWrite": 25546,                // 缓存写
  "output":     793,                  // 计费输出（**已包含 reasoning**）
  "reasoning":  542                   // reasoning 是 output 的子集，只展示
}
```

三条铁律（全项目一致）：

1. **`input` 只算非缓存输入。** 很多工具的 `input_tokens` 是「含缓存的总 prompt」——
   Codex 就是这样（本机实测：原始 402.6M，真正新增输入只有 0.58M，差 **690 倍**）。
   用 `inputSubtract` 声明要扣哪些字段。
2. **`output` 必须已包含 `reasoning`，绝不重复相加。** 大多数工具（Claude、Codex）的
   reasoning 本来就是 output 的子集；OpenCode 例外，它的 reasoning 是可加的，用 `outputAdd` 并进去。
3. **成本只由 `input + cacheRead + cacheWrite + output` 四项算出，永远不用 `total_tokens`。**
   （各家的 `total_tokens` 口径不一致，Codex 的 total 只是 input+output。）

---

## 2. 写一个适配器

### 2.1 JSONL 型（最常见）

```jsonc
{
  "id": "my-tool",                        // 必填，唯一
  "label": "My Tool",                     // 显示名
  "kind": "jsonl",                        // 可省略，缺省即 jsonl
  "paths": ["{home}/.my-tool/sessions/**/*.jsonl"],   // 支持 {home} / %VAR% / ** / *
  "prefilter": "\"usage\"",               // 可选，先做子串过滤，跳过无关行（大文件提速关键）

  "require": ["message.usage"],           // 这些路径都必须有值，否则丢弃该行
  "where":   { "type": "assistant" },     // 可选，精确匹配
  "dedup":   ["message.id", "requestId"], // 去重键；全部取不到时退回「文件#行号」

  "fields": {                             // 取值路径，给数组表示「按顺序取第一个非空」
    "model":      ["message.model"],
    "session":    ["sessionId", "$dir"],  // $file / $dir / $line 是内置变量
    "time":       ["timestamp"],
    "input":      ["message.usage.input_tokens"],
    "cacheRead":  ["message.usage.cache_read_input_tokens"],
    "cacheWrite": ["message.usage.cache_creation_input_tokens"],
    "output":     ["message.usage.output_tokens"],
    "reasoning":  ["message.usage.output_tokens.details.thinking_tokens"]
  },
  "note": "给用户看的一句话口径说明（会显示在页面卡片上）"
}
```

### 2.2 多帧 zstd JSONL（DeepSeek Harness）

DeepSeek Harness（dsh）把会话写进 **多帧 zstd 容器**（每 flush 一帧，magic `28 B5 2F FD`），
文件形如 `session.v3.jsonl.zstd`；`compression: none` 时是纯 `session.vN.jsonl`。
`kind: "zstd-jsonl"`：有 magic → 按帧逐帧解压（残缺尾帧跳过）；无 magic → 回退普通行解析。
字段路径 / `where` / `require` / `dedup` 与 JSONL 一致。

**一条语义、两个适配器 id**（官方与三方 home 分开采，label 带来源标签）：

| id | label | 覆盖 |
|---|---|---|
| `deepseek-harness` | DeepSeek Harness（官方） | `$DSH_HOME/sessions`、`~/.dsh/sessions` |
| `deepseek-harness-wrapper` | DeepSeek Harness（DSH Desktop） | `%APPDATA%\dsh-desktop\harness\sessions` 及常见包装壳 home |

```jsonc
{
  "kind": "zstd-jsonl",
  "paths": ["%DSH_HOME%/sessions/**/*.jsonl.zstd", "{home}/.dsh/sessions/**/*.jsonl"],
  "where": { "type": "assistant/message" },
  "require": ["data.usage"],
  "fields": { "time": ["time"], "input": ["data.usage.inputTokens"], ... }
}
```

zstd 由 Go 二进制内置（`github.com/klauspost/compress`），**不需要装任何 Python 包**。
信封 `time` 是 Unix **毫秒** —— 归一化时 `>1e12` 会转 ISO，否则按 `str(ts)[:10]` 切日
会得到假日键（OpenCode 的 ms 时间戳同样受益）。

### 2.3 SQLite 型

```jsonc
{
  "id": "my-tool", "label": "My Tool", "kind": "sqlite",
  "db": "{home}/.my-tool/state.db",
  "query": "select id, session_id, created_at, payload from events order by created_at",
  "json_column": "payload",               // 这一列是 JSON，会被摊平到顶层，之后路径写法与 JSONL 完全一致
  "require": ["tokens"],
  "dedup": ["id"],
  "fields": { "model": ["modelID"], "session": ["session_id"], "time": ["created_at"], ... }
}
```

`ReadSQLite` 会**自动把 db + wal + shm 一起拷到 work-dir 后只读打开**，
所以不用担心 WAL 里最新的一段丢失，也不用担心拷到一半的源文件。

### 2.4 可选字段

| 字段 | 作用 |
|---|---|
| `inputSubtract` | 从 input 里扣掉这些路径的值（Codex 用：`cached_input_tokens` + `cache_write_input_tokens`） |
| `outputAdd` | 把这些路径的值加进 output（OpenCode 用：`tokens.reasoning`） |
| `reasoningIsSubsetOfOutput` | 默认 `true`（自动 clamp 到 output）；`false` 表示 reasoning 独立 |
| `enabled` | `false` 则不参与采集（默认 `true`） |
| `group` | `builtin`（默认）/ `aggregator`（第三方聚合器）；仅用于 `--list` 的分组展示 |
| `driver` | 交给 `drivers.go` 里的函数处理（见下） |
| `caveat` | 已知的坑，展示在页面卡片上 |

### 2.5 什么时候需要 driver

声明式表达不了的两类：

1. **记录的是累计值而非每次的量** → 必须差分（Codex 的 `total_token_usage`）。
   直接累加 `last_token_usage` 会重复计数：本机实测累加得 109.2M，而会话累计只有 94.5M。
2. **跨行状态** → 模型名在别的行里、要向后携带（Codex 的 `turn_context`），
   甚至要「挂起再补记」（本机有 18M token 靠这步才归到正确模型）。

写法（`internal/sources/drivers.go`）：

```go
// driver 签名：吃下整个文件按行解析出的 raw 记录流，回吐规范记录。
func myTool(raw []*rawObj, m *Manifest, ctx *Context) []Record {
    var out []Record
    for _, o := range raw {
        out = append(out, Record{
            Tool: m.ID, Model: digStr(o, "model"), Session: digStr(o, "session"),
            Ts: ..., Input: ..., CacheRead: ..., CacheWrite: ...,
            Output: ..., Reasoning: ...,
        })
    }
    return out
}
```

取值用 `dig*` 系列辅助函数，诊断计数塞进 `ctx`，会被自动写进页面说明。

---

## 3. 跨平台路径

适配器是跨平台的：路径一律写正斜杠，**没有** `//go:build` 平台分支，glob 走标准库
`filepath.WalkDir` + `filepath.Match`（`**` 在 `reader.go` 里折叠成 `*`），不依赖 doublestar。

`paths` 里可以用三种占位符：

| 写法 | 含义 |
|---|---|
| `{home}` | 用户主目录（`USERPROFILE`，否则 `os.UserHomeDir()`） |
| `%VAR%` | 环境变量；未设置时按下面的回落链解析 |
| `$VAR%` / `${VAR}` | 同样支持（`os.ExpandEnv` 先跑一遍） |

### Windows 变量的跨平台回落

适配器沿用 Windows 风格的 `%VAR%`（原始 Python 采集器就是这么写的），但工具也要能出 macOS / Linux
二进制。所以未设置的 Windows 变量会按语义找等价物（`internal/sources/pathvars.go`）：

| Windows 变量 | 回落顺序 |
|---|---|
| `%APPDATA%` | `$XDG_DATA_HOME` → `$XDG_CONFIG_HOME` → Linux `~/.local/share` / macOS `~/Library/Application Support` |
| `%LOCALAPPDATA%` | `$XDG_STATE_HOME` → `$XDG_DATA_HOME` → 同上 |
| `%PROGRAMDATA%` | `$XDG_DATA_HOME` |
| `%HOMEDRIVE%` / `%HOMEPATH%` | **无对应，不解析**（假造一个值会把采集器指到不存在的根目录） |

真实环境变量永远优先——在 Linux 上显式 `export APPDATA=...`（比如 Wine 安装）依然生效。

于是同一份 manifest 三平台通用：`deepseek-harness-wrapper` 的 14 条路径里 12 条是
`%APPDATA%` 相对，在 Linux 上会解析到 `~/.local/share` 而不是一个匹配不到任何文件的字面量。

### 静默失败会变成告警

一个 `%VAR%` 解析不出来，路径就仍是字面量字符串，永远匹配不到文件——症状只是「这个工具的数字不见了」，
没有任何线索指向原因。`tokanary collect` 现在会检查：

```
[warn] deepseek-harness-wrapper: 路径变量在本机无法解析，该适配器贡献为 0 —— 需要 APPDATA
```

**只在「所有路径都依赖它」时才报。** 像 `deepseek-harness` 那样把 `%DSH_HOME%` 当**可选覆盖**、
同时还有 `{home}/.dsh` 兜底的，解析不出来只是少一条路径，不值得报警。

### 校验

```bash
tokanary collect --validate          # 只校验声明本身（不解析文件）
tokanary collect --list              # 看适配器清单与启用状态
tokanary collect --tools my-tool     # 只跑一个，快速试
tokanary collect                     # 真正采集；路径变量解析不了会在这里告警
```

---

其它可用 flag：`--adapters`（指定目录）、`--home`、`--work-dir`、
`--include-aggregators`（把 `enabled:false` 的也一并纳入）、
单价只用 models.dev 公开价（`.cache/prices-raw.json`，由 `tokanary prices` 生成）。

`--validate` 会检查：必填字段、kind 合法、fields 至少能取到 model/input/output、driver 是否存在。
配错了不会静默出错，会明确告诉你哪一条有问题。`tokanary refresh` 也会先跑一遍校验。

---

## 4. 已内置的适配器

| id | kind | 难点 | 状态 |
|---|---|---|---|
| `claude-code` | jsonl | 流式重复行，**58% 是重复**，必须按 `message.id` 去重 | 本机验证 |
| `codex` | jsonl + driver | input 含缓存；累计值需差分；模型名跨行 | 本机验证 |
| `opencode` | sqlite | reasoning 可加，需并入 output | 本机验证 |
| `deepseek-harness` | zstd-jsonl | 官方 home（`~/.dsh` / `$DSH_HOME`）；`inputTokens` 已非缓存（禁 inputSubtract）；信封 `time` 为 Unix ms → ISO；按 `data.message.id` 去重 | 本机无原生数据（0 文件，路径就绪） |
| `deepseek-harness-wrapper` | zstd-jsonl | 三方包装壳 home（本机 dataelement DSH Desktop → `%APPDATA%\dsh-desktop\harness`）；schema 同官方 | 本机验证（191 文件 / 2006.7M token） |

> 早期版本还带一个聚合器适配器（`enabled:false`，兜底覆盖 39 个工具）。
> 它与内置适配器重叠、同时开会重复计数，且其 `input_tokens` 仍含 `cache_creation_input_tokens`，
> 已随 Python 管线一并移除。如果你在 Cursor / Gemini 这类没有内置适配器的工具上有数据，
> 写一份自己的适配器比开聚合器更安全。
