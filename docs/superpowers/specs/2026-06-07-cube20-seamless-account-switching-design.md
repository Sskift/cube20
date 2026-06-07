# cube20 会话内无缝换号 + 云端配额中枢 — 设计

日期:2026-06-07
分支:`cube20-init-43981`
目标:让交互式 `codex` TUI 会话在撞到 5 小时限额时,自动切换到云端"最合理"的有额度账号并 `codex resume` 续接(用户只见 1-2s 重绘);同时让 dashboard 集中展示所有账号的限额/刷新情况。

## 项目初衷(为什么做这个)

用户能保证**可用账号数始终多于在用人数**。痛点是单账号易撞 5 小时限额,届时必须**手动切号 + 退出所有会话再 resume 刷新权限**,体验极差;且无法跟踪每个账号何时刷新、统一管理多账号配额。期望云端 Server 统一处理:每次跑 Codex 都分发到一个**有额度且最合理**(最快被刷新等指标)的账号,并在会话进行中自动完成换号。这就是项目的初衷。

已确认的关键决策:
- 主要使用形态 = **交互式 TUI 会话**(会话进行中撞限额的场景)。
- 期望换号体验 = **自动重连 + resume(短暂闪热)**:cube 检测到耗尽 → 自动 claim 新账号 → `codex resume` 重连 → 用户见 1-2s 重绘,对话上下文保留。
- 切换触发 = **Hybrid(主动 + 被动都要)**。
- 架构 = **A+B 混合**:客户端拥有机制,云端拥有策略。
- Dashboard 形态 = **总览表 + 刷新时间轴**。

## 背景与现状(经代码验证)

`cube20` 是 Codex 账号池云管理器。线上部署在 `10.37.6.166`,Postgres 模式,持有 3 个真实 ChatGPT 账号。详见 `docs/superpowers/specs/2026-06-06-cube20-review-fixes-design.md` 与项目记忆。

**用户愿景的三分之二已存在:**
- 云端配额追踪:`internal/web/quota_worker.go` 的 `StartQuotaWorker` 后台轮询,经 `Manager.FetchQuota` → `quota.FetchForCodexHome` 读每个账号的 5h/7d 窗口与刷新时间,写入 `QuotaCache`。
- "最合理"账号挑选:`internal/manager/manager.go` 的 `loadBalanceQuotaScore`(5h 剩余% + 临近刷新加成)+ `ClaimLease` 的 round-robin。

**缺失、且最难的部分:** 交互式 TUI 在会话中途撞 5h 限额时的**无缝换号**——既要检测,又要在不丢上下文的前提下换账号续接。

**codex 0.137.0 已验证的能力(本设计的地基):**
- 会话以自包含的 `rollout-<时间戳>-<uuid>.jsonl` 持久化在 `$CODEX_HOME/sessions/YYYY/MM/DD/`,文件可在不同 HOME 间移动;UUID 即 `codex resume` 接收的 session id。
- `codex resume <UUID>` 按 id 重连指定会话(显式传 UUID 可绕过默认按 cwd 过滤的 picker)。
- 每个 `token_count` 事件携带 `rate_limits` 对象:`primary`(`window_minutes=300` = 5h 窗口)有 `used_percent` / `resets_at`;`secondary`(`10080` = 周窗口)同构;另有 `rate_limit_reached_type`(未撞限额时为 null)。这与服务端 `quota.FetchForCodexHome` 读取的 ChatGPT usage API 同构 —— 客户端检测与云端追踪共用一套数据模型。
- 实验性 `codex remote-control` / `app-server daemon` / `resume --remote ws://` 提供 daemon-attach 的零闪烁形态 —— **本期不采用**(实验性、跨版本不稳定;用户已接受"短暂闪热")。

## 架构:职责划分

```
┌─ cube run(客户端 = 机制)────────────┐         ┌─ cube dashboard(云端 = 策略)──────┐
│ • 稳定 CODEX_HOME 上启动 codex TUI    │         │ • 配额 worker(已有)读未租账号     │
│ • tail 会话 .jsonl,解析 rate_limits  │  心跳    │ • 已租账号:接收 client 上报,不自读 │
│ • 被动:rate_limit_reached → 立即切换  │ ⇄ HTTP  │ • 心跳回包下发换号建议 {shouldSwap}  │
│ • 主动:执行云端"现在切换"指令        │         │ • loadBalanceScore 选"最合理"账号  │
│ • 上报已租账号实时配额(顺心跳带上)  │         │ • dashboard 集中展示所有账号配额    │
│ • 切换:claim→写auth→kill→resume     │         │                                    │
└──────────────────────────────────────┘         └────────────────────────────────────┘
```

- **客户端拥有机制**:检测(tail 日志)+ kill/resume/swap,含被动硬限额快路径。版本无关、对服务端数据新鲜度零依赖,是兜底安全网。
- **云端拥有策略**:主动决定"该不该切"——经由**已有的心跳通道**只下发*换号建议信号*(如 `shouldSwap=true`),client 收到后在安全边界走下方统一的切换流程(真正的账号 claim 仍由 client 发起、云端 `loadBalanceScore` 选号)。这使配额智能集中在云端,符合"云端统一管理"愿景,并为全局再平衡留出空间。
- **不做**:实验性 daemon 零闪烁形态(YAGNI;列为未来升级)。

## 切换机制 + 凭据卫生 + 配额数据流

### CODEX_HOME:临时 → 稳定

今天 `cmd/cube/main.go` 的 `runCloudRun` 建临时目录、`defer os.RemoveAll(codexHome)`(约 main.go:412)。问题:resume 需要会话 rollout `.jsonl` 跨切换存活,而删目录会把它一并删掉。

改为每次 run 使用**稳定目录** `~/.cube20/runs/<run-id>/`(run-id 为本次 run 生成的稳定标识)。会话日志在此长存,使 `codex resume <UUID>` 可用。

### 凭据卫生:删整目录 → 精确清除 auth.json

不再 `RemoveAll` 整个目录。改为:
- **每次账号切换之间**:用新账号的 `auth.json` 覆盖写入(旧凭据被覆盖)。
- **run 退出时(defer)**:清除/置空该目录里的 `auth.json`,保留会话日志(对话内容,非凭据)。

保持原有"凭据不在磁盘残留"的安全姿态,同时让会话可续接。

### 切换流程(被动 / 主动同一套机制)

1. 记录当前 codex 子进程的 session UUID(启动时即从会话日志/目录获知)。
2. 向云端 `claim` 新租约;云端用 `loadBalanceQuotaScore` 选"最合理"账号。
3. 将新账号 `auth.json` 写入同一稳定 CODEX_HOME。
4. 优雅 kill 当前 codex 子进程。
5. `codex resume <UUID>` 重新拉起:接管 TTY、重放会话 = 上下文保留;用户见 1-2s 重绘。
6. 释放旧租约。

集成点:`cmd/cube/main.go` 的 `runCommandWithLease`(约 main.go:515)—— 它已拥有 codex 子进程与心跳 goroutine,换号逻辑归属于此。今天它把 `cmd.Run()` 当作单次阻塞子进程;新逻辑改为"子进程可被换号循环重启"。

### 检测:两个触发,一个数据源

客户端 tail 自己会话日志里的 `token_count` 事件,解析 `rate_limits`:
- **主动**:`primary.used_percent`(`window_minutes=300`)越过阈值 → 在下一个安全边界(codex 完成一次回复、等待用户输入时,而非生成中途)换号(可结合云端心跳的换号建议)。
- **被动**:`rate_limit_reached_type != null` → 立即换号(硬限额快路径,不依赖服务端)。

### 解决"云端不能刷新已租账号"

ChatGPT OAuth refresh token 是**轮换式**:刷新一次即作废旧 token,并发使用触发 `refresh_token_reused`。但**读 usage(用仍有效的 access token 发 GET)不轮换**。代码验证(`internal/quota/codex.go`):仅当 access token 为空、或读 usage 返回 401 时才会触发会轮换的 `refreshAuthFile`;正常 worker 只发 GET,不轮换。

现有双重防护:
- `quota_worker.go:58`:选刷新对象时 `item.LeaseActive` 为真即跳过。
- `FetchQuota`(manager.go:3427):`accountLeaseActive` 为真则直接返回缓存,走不到网络请求。

剩余缺口与修复:
- **语义缺口**:云端需知道**已租账号**的实时配额(才能判断"快耗尽,该切号"),但又不能自读(会轮换 client 在用的 token)。
  → **数据来源反转**:已租账号配额改由**持有租约的 client 顺心跳上报**(数据来自 codex 写进日志的 `rate_limits`,零额外网络、零轮换风险)。云端经现有 `RecordQuotaReport` / `QuotaSourceClient`(manager.go:3453)写入 `QuotaCache`。worker 对已租账号继续不碰;租约释放后恢复自读。
- **竞态窗口**:worker 在 t0 判断"未租用"通过、t1 账号被租走,交叠时仍可能对刚租出的账号发请求(若恰为 401 触发 refresh 则轮换在用 token)。
  → worker 真正发请求前,在**持锁状态**下复检一次 `LeaseActive`,堵住该窗口。

结果:云端对每个账号**永远有配额视图** —— 未租靠 worker 自读、已租靠 client 上报,两侧都不轮换在用 token。

## Dashboard 展示:总览表 + 刷新时间轴

### 数据已就绪

`QuotaCache` 每账号已含 `FiveHour`(用量%、剩余%、`ResetsAt`)+ 数据来源标记。后端或补一个聚合端点(把 per-account 数据汇成一张表),或前端直接用现有 `/api/accounts` + `/api/refresh-queue` 拼装。

### 总览表

每行一账号,列:5h 用量%/剩余%、刷新倒计时、当前租用者、状态(就绪/租用中/恢复中/耗尽)、数据来源(云端读 / client 上报)。

### 刷新时间轴

用已在依赖中的 **Recharts**,把所有账号的 5h `ResetsAt` 画在一条时间线上,直观呈现"下一个回血的是哪个、何时" —— 直接服务"最快被刷新"的调度直觉。

### 顺手重构(有针对性,非无关)

前端目前全部堆在单文件 `web/src/App.tsx`(1947 行),已有 4 个视图(`accounts | load-balancer | people | import`),`recharts` 已在依赖。新增本视图时,把账号总览 / 时间轴抽为独立组件(如 `web/src/views/AccountsOverview.tsx`),共享 fetch/类型抽出。只动为本功能服务的部分,不碰其他视图。

技术栈:React + TypeScript + Vite + HeroUI + Tailwind + Recharts;前端 `web/dist` 经 `webdist.DistFS` 嵌入二进制(`internal/web/server.go`)。

## 涉及文件汇总

| 区域 | 文件 | 改动 |
|------|------|------|
| 客户端 run | `cmd/cube/main.go` | CODEX_HOME 临时→稳定;精确清除 auth.json;`runCommandWithLease` 改为可换号循环;tail 会话日志检测两触发;心跳上报已租配额、接收换号建议信号;换号流程(claim→写auth→kill→resume) |
| 云端 manager | `internal/manager/manager.go` | worker 发请求前持锁复检 `LeaseActive`;心跳/claim 路径支持下发换号建议信号与接收 client 配额上报(复用 `RecordQuotaReport`) |
| 云端 web | `internal/web/server.go`、`quota_worker.go` | 心跳/sync 端点扩展(换号建议信号 + 配额上报);可能新增 dashboard 聚合端点 |
| 配额 | `internal/quota/codex.go` | 复用现有 usage 数据模型;按需暴露解析 `rate_limits` 的共享工具供 client 用 |
| 前端 | `web/src/App.tsx` → 拆出 `web/src/views/AccountsOverview.tsx` 等 | 总览表 + Recharts 刷新时间轴;共享 fetch/类型抽出 |

## 测试策略

- **客户端换号**:把 codex 子进程与 claim/release 抽象成可注入的 seam,用假 codex(产出可控的 `token_count`/`rate_limit_reached` 事件 + 可被 resume 的假会话文件)驱动,断言:主动阈值触发、被动硬限额触发、换号后旧租约释放、上下文保留(UUID 不变)。
- **竞态复检**:针对 worker 发请求前持锁复检 `LeaseActive` 写并发/竞态测试。
- **配额上报**:断言已租账号经 client 上报进入 `QuotaCache`(`source=client`),且 worker 不自读已租账号。
- **整体**:`go test ./... -race`。
- **前端**:`npm run lint` + `npm run build` 通过;视图渲染可加轻量测试。
- **安全冒烟**(线上):遵循记忆中的隔离方式 —— throwaway HOME、文件模式、worker off、loopback 端口,验证 boot + `/readyz` + 优雅 SIGTERM,不触碰真实账号。

## 非目标(YAGNI / 本期不做)

- 实验性 codex daemon 零闪烁形态(未来升级)。
- 多租约 per-client 上限 bug 与无后台 reaper(lazy 回收)弱点 —— 用户已明确去优先级(保证可用账号绝对多于在用人数);除非用户另行要求,本期不动。
- 周窗口(7d)的换号策略 —— 本期聚焦 5h 主窗口。
