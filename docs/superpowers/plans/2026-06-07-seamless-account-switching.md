# 会话内无缝换号 + 云端配额中枢 — 实现计划

> **For agentic workers:** 本计划面向并行 subagent 执行。按文件归属切分,互不冲突。每个 agent 用 TDD,频繁提交,只在本地跑 `go test ./... -race` / `npm run build`。**禁止部署、禁止连真实账号、禁止读 ~/.cube20/state.json。** 线上 10.37.6.166 不受影响。

**Goal:** 交互式 codex TUI 撞 5h 限额时自动换号 + `codex resume` 续接(短暂闪热);dashboard 集中展示各账号限额/刷新。

**Architecture:** 客户端拥有机制(检测+换号),云端拥有策略(心跳下发 `shouldSwap` 建议 + 集中配额视图)。详见 `docs/superpowers/specs/2026-06-07-cube20-seamless-account-switching-design.md`。

**Tech Stack:** Go(quota/manager/web/cmd)、React+TS+Vite+HeroUI+Recharts(web/src)。

---

## 冻结契约(跨 agent 协调合同 —— 任何人不得擅改)

### C1. 会话日志 rate_limits 解析(package usage,新文件)

codex 把 `token_count` 事件写入 `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`,每行 `{timestamp,type,payload}`,`payload.type=="token_count"` 时含:
```
payload.rate_limits = {
  primary:   {used_percent: float, window_minutes: int(=300 5h), resets_at: int(unix)},
  secondary: {used_percent: float, window_minutes: int(=10080 7d), resets_at: int(unix)},
  rate_limit_reached_type: string|null
}
```
新增 `internal/usage/ratelimits.go`,导出:
```go
type RateLimits struct {
    FiveHourUsedPercent float64
    FiveHourResetsAt    time.Time // zero if absent
    SevenDayUsedPercent float64
    SevenDayResetsAt    time.Time
    ReachedType         string    // "" = 未撞限额;非空 = 硬限额已触发
    CapturedAt          time.Time // 该事件 timestamp
    Found               bool      // 是否解析到任何 token_count.rate_limits
}
// 扫描 codexHome 下所有 rollout *.jsonl,返回时间上最新的一条 rate_limits。
func LatestRateLimits(codexHome string) RateLimits
```
镜像 `internal/usage/codex.go` 的 `collectFiles`/`parseFile`(4MiB scanner、子串预过滤 `"rate_limits"`、`map[string]any`、`parseTime`)。不引入对 quota 包的依赖。

### C2. 心跳请求/响应(client ⇄ web 契约)

**请求**(PATCH `/api/sync/leases/{id}` 与 `/api/sync/leases/{id}/heartbeat`,在现有 body 上**新增可选字段**):
```json
{ "accountId":"..","client":"..","holder":"..","ttlSeconds":80,
  "fiveHour": {"key":"five_hour","label":"5h","usedPercent":0,"remainingPercent":100,"resetsAt":"RFC3339"},
  "rateLimitReached": false }
```
`fiveHour` 为 `quota.Window`(omitempty);缺省时服务端不更新配额。

**响应**(原本是裸 `manager.Lease`;现在追加同级字段 `shouldSwap`,**向后兼容**——旧字段位置不变):
```json
{ "id":"..","accountId":"..","clientId":"..","holder":"..","generation":0,
  "startedAt":"..","heartbeatAt":"..","expiresAt":"..", "shouldSwap": false }
```
两个心跳分支(server.go:615-632 与 651-671)必须改成调用同一个 helper,输出一致。

### C3. Manager 新增方法(Wave1-M 提供,Wave2-W 消费)

```go
// 写入已租账号的 client 上报配额到 QuotaCache(source=client),校验该 client 持有该 lease,
// 但【绝不】翻转 OwnerMode(区别于 RecordQuotaReport)。文件+Postgres 双模式。
func (m *Manager) RecordLeasedQuota(accountID, leaseID, clientID string, result quota.Result, now time.Time) error

// 读 QuotaCache 的 5h 窗口,remaining < swapRemainingThreshold 则建议换号。无缓存或不支持 → false。
func (m *Manager) ShouldSwapLease(accountID string) (bool, error)

const swapRemainingThreshold = 10.0 // 5h 剩余 < 10% 即建议换号(主动阈值)
```

### C4. 竞态修复(Wave1-M)

`FetchQuota`(manager.go:3401):在 `quota.FetchForCodexHome` 网络调用**之前**,持 `run-round-robin.lock` 复检该账号 `accountLeaseActive`,若已被租则跳过网络、返回缓存(避免对刚租出账号触发轮换)。锁不得跨网络调用持有(`recordQuotaResult` 自身要取同锁,会死锁)。再在 `writeQuotaResultLocked` 内:当 `source==cloud` 且账号此刻 lease-active 时,跳过写入(不覆盖 client 上报)。

### C5. 稳定 CODEX_HOME + 精确清除(Wave2-C)

- run 目录:`~/.cube20/runs/<runID>/`(替代 `os.MkdirTemp`)。`runID` 用 crypto/rand hex。
- 退出 defer:删除 `auth.json` 与 `config.toml`(软链),**保留 sessions/**(供 resume)。替代现有 `os.RemoveAll(codexHome)`。
- 启动时 best-effort 清理无 `auth.json` 且 mtime 超 7 天的旧 run 目录。

### C6. 换号流程(Wave2-C)

```
codexHome := stableRunHome(); defer scrubAuth(codexHome)
lease := claim(); writeAuth(codexHome, lease.snapshot.auth)
args := codexArgs                  // 首轮
for {
    cmd := codexCommandForHome(codexHome, args)
    swapReq := runCommandWithLease(cmd, lease, codexHome)  // 心跳内监测;需换号时 SIGTERM 子进程并置位
    if !swapReq { break }                                  // 用户正常退出 / 子进程自退
    sid := newestSessionID(codexHome)                      // rollout 文件名解析 UUID
    newLease := claim()                                    // 旧 lease 未释放→不会再选到同账号
    release(lease); lease = newLease
    writeAuth(codexHome, newLease.snapshot.auth)
    args = []string{"resume", sid}                         // 续接
}
pushUsage(); release(lease)
```
换号触发(心跳 goroutine 每 tick 用 C1 解析 codexHome):被动 `ReachedType!=""` → 立即换;主动 `shouldSwap`(心跳响应)或本地 `5h remaining<阈值` → 换。换号时心跳 goroutine 给主循环置 `swapRequested` 并 `cmd.Process.Signal(SIGTERM)`;主循环据此区分"换号 kill" vs "用户退出"。

### C7. Dashboard 数据源(Wave1-F)

复用**现有** `GET /api/refresh-queue`(admin,返回 `[]RefreshQueueItem`,已含 accountId/label/status/resetsAt/usedPercent/remainingPercent/quotaSource/leaseActive/leaseClientId/leaseExpiresAt)。**不新增后端端点。** 前端据此渲染总览表 + Recharts 刷新时间轴。

---

## Wave 1(并行,3 agent,文件互不相交)

### Task A — usage rate_limits 解析器(Agent Q)
**Files:** Create `internal/usage/ratelimits.go`; Create `internal/usage/ratelimits_test.go`
- [ ] 写失败测试:在 `t.TempDir()` 造 `sessions/2026/06/07/rollout-x-uuid.jsonl`,含两条 `token_count`(不同 timestamp、不同 used_percent),断言 `LatestRateLimits` 取**最新**那条的 5h used_percent、resets_at(unix→time)、`ReachedType`、`Found=true`;空目录 → `Found=false`。
- [ ] 跑测试确认 FAIL。
- [ ] 实现 C1:`collectFiles` 同款 walk + 4MiB scanner + 子串预过滤 `"rate_limits"` + `map[string]any` + `parseTime`;按 timestamp 取最新。
- [ ] 跑测试 PASS;`go vet ./internal/usage/`。
- [ ] 提交 `feat(usage): parse latest rate_limits from codex session logs`。

### Task B — manager 配额上报/换号建议/竞态(Agent M)
**Files:** Modify `internal/manager/manager.go`; Modify `internal/manager/manager_test.go`
- [ ] 写失败测试(用既有 `newTestManager`/`saveTestAccounts`/`saveTestQuota`):
  - `RecordLeasedQuota` 写入 cache(source=client、含 FiveHour)且 `OwnerMode` **仍为 cloud**;非持租 client 调用返回 error。
  - `ShouldSwapLease`:5h remaining=5 → true;remaining=50 → false;无 cache → false。
  - 竞态:账号 lease-active 时 `FetchQuota` 不触网(用一个会 panic/计数的 seam 或断言 cache 未被 cloud 覆盖)。
- [ ] 跑测试 FAIL。
- [ ] 实现 C3 + C4(`RecordLeasedQuota`、`ShouldSwapLease`、`swapRemainingThreshold`、FetchQuota 网络前持锁复检、writeQuotaResultLocked 的 cloud-skip-if-leased;Postgres 路径同步)。
- [ ] 跑 `go test ./internal/manager/ -race` PASS。
- [ ] 提交 `feat(manager): leased-quota report, swap hint, refresh race guard`。

### Task C — dashboard 总览表 + 刷新时间轴(Agent F)
**Files:** Modify `web/src/App.tsx`(加 view);Create `web/src/views/QuotaOverview.tsx`
- [ ] 扩展 `DashboardView` 加 `"overview"`;sidebar 加 `<NavItem>`(lucide 新图标,如 `Gauge`);content 容器加 `{activeView==="overview" && (<section className="cube-view-panel"><QuotaOverview queue={refreshQueue} accounts={accounts}/></section>)}`。
- [ ] `QuotaOverview.tsx`:总览表(每账号 5h used%/remaining%、`shortTime(resetsAt)` 倒计时、`leaseClientId`、status、`quotaSource`)复用 `tokens/shortTime/shortID` 风格;Recharts 横向时间轴(各账号 `resetsAt` 散点/条)——首次引入 `import { ... } from "recharts"`(已在依赖)。
- [ ] `cd web && npm run lint && npm run build` 通过(dist 重新生成)。
- [ ] 提交 `feat(web): account quota overview table + reset timeline`。

## Wave 2(并行,2 agent;依赖 Wave1 的 B、A 契约)

### Task D — web 心跳扩展(Agent W)— blockedBy B
**Files:** Modify `internal/web/server.go`; Modify `internal/web/server_test.go`
- [ ] 写失败测试(`newTestServer` + httptest):PATCH 心跳带 `fiveHour.remainingPercent=5` → 响应含 `shouldSwap:true`;带 `remainingPercent=80` → `shouldSwap:false`;且 leased 账号上报后 `OwnerMode` 不变(经 manager 验证)。
- [ ] 跑 FAIL。
- [ ] 把两个心跳分支重构为共享 helper `writeHeartbeat(w,r,leaseID,auth)`:解析含 `fiveHour/rateLimitReached` 的 body;若 `fiveHour` 非空 → `m.RecordLeasedQuota(...)`;`m.TouchLease(...)`;`swap,_ := m.ShouldSwapLease(accountID)`;`writeJSON(200, 匿名 struct{manager.Lease;ShouldSwap bool})`(C2)。
- [ ] 跑 `go test ./internal/web/ -race` PASS。
- [ ] 提交 `feat(web): heartbeat carries leased quota + shouldSwap hint`。

### Task E — client 换号循环(Agent C)— blockedBy A、B
**Files:** Modify `cmd/cube/main.go`; Create `cmd/cube/run_swap_test.go`
- [ ] 写失败测试:`newestSessionID` 从造好的 rollout 文件名解析 UUID(取最新);`stableRunHome`/`scrubAuth` 行为(scrub 后 auth.json 不存在、sessions 仍在);swap 决策纯函数 `swapDecision(rl usage.RateLimits, shouldSwap bool) (bool, reason)`(reached→true、remaining<阈值→true、否则 false)。
- [ ] 跑 FAIL。
- [ ] 实现 C5/C6:稳定 home + scrub + 旧目录 prune;`runCloudRun` 改换号循环;`heartbeatLease` 发送 C2 请求(带 `usage.LatestRateLimits` 映射成 `quota.Window`)并读取 `shouldSwap`;心跳 goroutine 监测→置位+SIGTERM;`newestSessionID`;resume 续接。
- [ ] 跑 `go test ./cmd/cube/ -race` PASS。
- [ ] 提交 `feat(cube): in-session account swap with codex resume`。

## Wave 3 — 集成与验证(编排者本人,不部署)
- [ ] `gofmt -l` 干净;根目录 `go build ./...`;`go test ./... -race` 全绿。
- [ ] `cd web && npm run build`;再 `go build ./...` 确认新 dist 被嵌入。
- [ ] 安全冒烟(隔离):throwaway `HOME`、文件模式(无 `CUBE_DATABASE_URL`)、`--quota-refresh-interval 0s`、loopback 端口,验证 boot + `/readyz` + SIGTERM。**不连真实账号、不部署。**
- [ ] 汇总变更给用户(由用户决定是否部署)。

## 自查
- 契约 C1-C7 均有对应 Task:C1→A、C3/C4→B、C7→C、C2→D(+E 消费)、C5/C6→E。✓
- 无占位符:方法签名/字段/阈值/文件路径均具体。✓
- 类型一致:`RecordLeasedQuota`/`ShouldSwapLease`/`LatestRateLimits`/`swapRemainingThreshold`/`shouldSwap` 全程同名。✓
- 反复确认:**不复用** `RecordQuotaReport`(会翻 owner),改用 `RecordLeasedQuota`。✓
