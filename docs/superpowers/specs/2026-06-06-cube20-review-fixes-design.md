# cube20 代码审查修复 — 设计

日期:2026-06-06
分支:`cube20-init-43981`
目标:修复代码审查发现的 9 项问题,本地 TDD 验证,交叉编译后在线上 `10.37.6.166` 灰度部署。

## 背景

`cube20` 是 Codex 账号池云管理器。线上部署在 `10.37.6.166`:
- 路径 `/data00/home/liushiao/cube20-deploy-test`,以用户 `liushiao` 运行(无需 sudo)。
- Postgres 模式(`cube20` 库),绑定 `0.0.0.0:8720`,设置了 admin token。
- **持有真实数据**:3 个真实 ChatGPT 账号 auth 快照 + 1 条活跃租约。
- 远端**无 Go 工具链** → 本地交叉编译 `GOOS=linux GOARCH=amd64`。

## 修复清单

| # | 文件 | 问题 | 修复 |
|---|------|------|------|
| 1 | `internal/quota/codex.go` | access token 为空、刷新成功后,若 auth 同时含 API key,仍误判 `unsupported`,丢弃刚刷新的 OAuth token | `apiKey` unsupported 检查仅在刷新后 `accessToken` 仍为空时执行 |
| 2 | `internal/manager/manager.go` | 文件模式 `recordQuotaResult` 无锁 `Load→改→Save`,与持锁的租约写并发,可覆盖租约 → 账号被二次分发 | 文件分支用 `acquireLock(run-round-robin.lock)` 包住读改写 |
| 3 | `internal/manager/manager.go` | `postgresDB()` 每次 `Open+Ping+Close`,11 处调用,放弃连接池价值 | Manager 持有进程级 `*sql.DB`(懒开,文件模式不建);新增 `Manager.Close()` |
| 4 | `internal/manager/manager.go` | `loadPostgresState` 每次把所有账号 auth 重写到本地磁盘(写放大 + 凭证散落) | 内容/digest 未变则跳过写盘 |
| 5 | `internal/web/server.go` | `http.ListenAndServe` 无超时,易受慢连接攻击 | 改用配置了 Read/ReadHeader/Write/Idle 超时的 `&http.Server{}` |
| 6 | `internal/manager/manager.go` | `RecoverExpiredLeases` 末尾空 `if/continue` 无效果 | 删除无效判断或替换为真实日志 |
| 7 | `internal/manager/manager.go` | `fileModeFor` 两分支返回同值 | 折叠为常量 `0o600` |
| 8 | `internal/web/` | quota worker 以 `context.Background()` 启动,无优雅关闭 | 可取消 context + 信号处理,关闭时停 worker 并 `Manager.Close()` |
| 9 | `internal/web/` | `CloudToken==""` 时全开放;配合 `0.0.0.0` 即无鉴权 | 绑定非环回地址且无 token 时拒绝启动(或显著告警) |

## 并行 agent 分工(按包边界,文件不相交)

| Agent | 拥有文件 | 修复 | 执行方式 |
|-------|---------|------|---------|
| A — quota | `internal/quota/codex.go`(+test) | #1 | 后台 agent,共享工作树 |
| B — web | `internal/web/server.go`、`quota_worker.go`、`health.go`(+test) | #5 #8 #9 | 后台 agent,共享工作树 |
| C — manager | `internal/manager/manager.go`(+test) | #2 #3 #4 #6 #7 | 编排者本人 |

依赖链 `quota ← manager ← web`。跨 agent 唯一耦合:#8 调 `Manager.Close()`(#3 新增)。

### 为何共享工作树而非 worktree 隔离

默认 `worktree.baseRef=fresh` 会从 `origin/main` 拉新分支,而当前 HEAD 领先 `origin/main` 三个提交(quota-aware LB、dispatch history)。隔离 agent 会基于陈旧代码,集成困难。改为:**所有 agent 在同一工作树,文件车道严格不相交,agent 不执行任何 git 命令,只编辑自己的文件并 `go test` 自己的包**。集成即零操作(文件已就地)。

### 协调契约

1. **`Manager.Close()` 先行**:派发 agent 前,C 先把 `db *sql.DB` 字段 + `Manager.Close()` 真实实现写入 `manager.go` 并提交,使 web 包能直接编译引用(#3 的地基)。
2. **agent 只构建自己的包**:A 跑 `go test -race ./internal/quota/...`,B 跑 `go test -race ./internal/web/...`,**不得** `go build ./...`(避免撞上 C 正在改、可能暂不编译的 `manager.go`)。
3. **agent 不碰 git**:只编辑文件;所有 `git add/commit` 由 C 统一执行。
4. **连接池懒开**:首次 PG 操作才建池;文件模式永不建;CLI 进程退出释放。
5. **advisory lock 用专属连接**:`pg_advisory_lock` 是会话级锁,必须 `db.Conn(ctx)` 取独立连接持有并在**同一连接**释放,不能用共享池自动连接,否则 unlock 可能落到别的连接。

### 集成方式

A、B 与 C 共享工作树且文件车道不相交 → 三方编辑就地汇合,无需 apply/merge。全部完成后 C 运行 `go test ./... -race`、`go vet ./...`、`go build ./...`,统一提交。

## TDD

每项先写失败测试再实现。#1/#2/#9 必须先红灯:#2 用 `-race` 复现租约被覆盖;#9 断言非环回 + 空 token 启动报错。

## 线上灰度部署

1. **本地验证**:`go test ./... -race`、`go vet`、`go build`。
2. **交叉编译**:`GOOS=linux GOARCH=amd64 go build -o bin/cube-linux-amd64 ./cmd/cube`。
3. **Canary(零触真实数据)**:scp 新二进制 → 连一次性 `cube20_canary` 库、跑 `8721` 端口 → 验证 PG 路径(租约 claim/heartbeat/auth push、配额刷新、连接复用、auth 不再每次重写)→ 焚库停进程。
4. **灰度替换**:`cp bin/cube bin/cube.bak-<ts>` → 换新 bin → 重启 `cube20.service` → 验 `/readyz` + 3 账号 + 活跃租约仍在 + 看日志。
5. **回滚**:异常则 `cp bin/cube.bak-<ts> bin/cube` 重启,秒级还原。

## 成功标准

- 9 项全部修复,各有测试覆盖。
- `go test ./... -race` 全绿,`go vet` 干净。
- 线上 `/readyz` 返回 ready,3 个账号与活跃租约数据无损。
- 具备即时回滚能力。
