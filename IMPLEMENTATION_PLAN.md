# 限速功能重构实施计划

## 一、需求概述

**原始需求**: 限速功能当前绑定到具体隧道，需要改为不绑定隧道，创建限速后可以自由在隧道上限速，也可以在转发上限速。

**核心变更**:
1. 限速规则(SpeedLimit)与隧道的绑定关系改为可选
2. 转发(Forward)支持独立的限速规则

---

## 二、实施计划清单

### 2.0 计划状态（审计更新：2026-02-26）

- 总体状态：**进行中（未验收通过）**
- 已完成：模型、仓储查询、限速 CRUD、控制面优先级、限速页与类型改造、编译与测试通过
- 未完成：**Forward 独立限速写入链路**（前端表单 -> API handler -> repository 落库 `forward.speed_id`）

### 2.1 后端模型层 (Model)

| 序号 | 任务 | 文件 | 状态 |
|------|------|------|------|
| M1 | SpeedLimit.TunnelID 改为 sql.NullInt64 (可空) | `go-backend/internal/store/model/model.go` | ✅ 完成 |
| M2 | SpeedLimit.TunnelName 改为 sql.NullString (可空) | `go-backend/internal/store/model/model.go` | ✅ 完成 |
| M3 | Forward 添加 SpeedID sql.NullInt64 字段 | `go-backend/internal/store/model/model.go` | ✅ 完成 |
| M4 | ForwardRecord 添加 SpeedID sql.NullInt64 字段 | `go-backend/internal/store/model/model.go` | ✅ 完成 |
| M5 | SpeedLimitBackup.TunnelID 改为指针类型 | `go-backend/internal/store/model/model.go` | ✅ 完成 |
| M6 | ForwardBackup 添加 SpeedID *int64 字段 | `go-backend/internal/store/model/model.go` | ✅ 完成 |

### 2.2 后端仓储层 (Repository)

| 序号 | 任务 | 文件 | 状态 |
|------|------|------|------|
| R1 | ListSpeedLimits() 返回可空 tunnelId/tunnelName | `go-backend/internal/store/repo/repository.go` | ✅ 完成 |
| R2 | ListForwards() 返回 speedId 字段 | `go-backend/internal/store/repo/repository.go` | ✅ 完成 |
| R3 | CreateSpeedLimit() 参数 tunnelID 改为 *int64 | `go-backend/internal/store/repo/repository_mutations.go` | ✅ 完成 |
| R4 | UpdateSpeedLimit() 参数 tunnelID 改为 *int64 | `go-backend/internal/store/repo/repository_mutations.go` | ✅ 完成 |
| R5 | GetSpeedLimitTunnelID() 返回 sql.NullInt64 | `go-backend/internal/store/repo/repository_mutations.go` | ✅ 完成 |
| R6 | exportSpeedLimits() 处理可空字段 | `go-backend/internal/store/repo/repository.go` | ✅ 完成 |
| R7 | importSpeedLimits() 处理可空字段 | `go-backend/internal/store/repo/repository.go` | ✅ 完成 |
| R8 | GetSpeedLimitSpeed() 新增方法 | `go-backend/internal/store/repo/repository_flow.go` | ✅ 完成 |
| R9 | ListForwardsByTunnel() 返回 SpeedID | `go-backend/internal/store/repo/repository_control.go` | ✅ 完成 |
| R10 | ListActiveForwardsByUser() 返回 SpeedID | `go-backend/internal/store/repo/repository_flow.go` | ✅ 完成 |
| R11 | ListActiveForwardsByUserTunnel() 返回 SpeedID | `go-backend/internal/store/repo/repository_flow.go` | ✅ 完成 |
| R12 | GetForwardRecord() 返回 SpeedID | `go-backend/internal/store/repo/repository_flow.go` | ✅ 完成 |

### 2.3 后端处理器层 (Handler)

| 序号 | 任务 | 文件 | 状态 |
|------|------|------|------|
| H1 | speedLimitCreate 处理可选 tunnelId | `go-backend/internal/http/handler/mutations.go` | ✅ 完成 |
| H2 | speedLimitUpdate 处理可选 tunnelId | `go-backend/internal/http/handler/mutations.go` | ✅ 完成 |
| H3 | speedLimitDelete 处理可空 tunnelID | `go-backend/internal/http/handler/mutations.go` | ✅ 完成 |

### 2.4 后端控制平面 (Control Plane)

| 序号 | 任务 | 文件 | 状态 |
|------|------|------|------|
| C1 | syncForwardServices 优先使用 Forward.SpeedID | `go-backend/internal/http/handler/control_plane.go` | ✅ 完成 |
| C2 | 回退到 UserTunnel 的 speed limit | `go-backend/internal/http/handler/control_plane.go` | ✅ 完成 |

### 2.5 前端类型定义 (TypeScript Types)

| 序号 | 任务 | 文件 | 状态 |
|------|------|------|------|
| T1 | SpeedLimitApiItem.tunnelId 改为可选 | `vite-frontend/src/api/types.ts` | ✅ 完成 |
| T2 | ForwardApiItem 添加 speedId 字段 | `vite-frontend/src/api/types.ts` | ✅ 完成 |
| T3 | ForwardMutationPayload 添加 speedId 字段 | `vite-frontend/src/api/types.ts` | ✅ 完成 |
| T4 | SpeedLimitMutationPayload.tunnelId 改为可选 | `vite-frontend/src/api/types.ts` | ✅ 完成 |

### 2.6 前端页面组件

| 序号 | 任务 | 文件 | 状态 |
|------|------|------|------|
| F1 | SpeedLimitRule 接口更新 | `vite-frontend/src/pages/limit.tsx` | ✅ 完成 |
| F2 | SpeedLimitForm 接口更新 | `vite-frontend/src/pages/limit.tsx` | ✅ 完成 |
| F3 | validateForm 移除 tunnelId 必填校验 | `vite-frontend/src/pages/limit.tsx` | ✅ 完成 |
| F4 | Select 组件改为可选 | `vite-frontend/src/pages/limit.tsx` | ✅ 完成 |
| F5 | 显示"未绑定"状态 | `vite-frontend/src/pages/limit.tsx` | ✅ 完成 |

### 2.7 编译验证

| 序号 | 任务 | 状态 |
|------|------|------|
| B1 | Go 后端编译通过 | ✅ 完成 |
| B2 | TypeScript 类型检查通过 | ✅ 完成 |
| B3 | `go test ./...` 全量通过 | ✅ 完成 |
| B4 | `go test ./tests/contract/... -run SpeedLimit` 通过 | ✅ 完成 |

### 2.8 Forward 独立限速写入链路补全（新增）

| 序号 | 任务 | 文件 | 状态 |
|------|------|------|------|
| N1 | forwardCreate 支持接收并校验可选 speedId，写入 Forward.SpeedID | `go-backend/internal/http/handler/mutations.go` | ✅ 完成 |
| N2 | forwardUpdate 支持更新/清空 speedId，并触发服务重下发 | `go-backend/internal/http/handler/mutations.go` | ✅ 完成 |
| N3 | CreateForwardTx 支持落库 speed_id | `go-backend/internal/store/repo/repository_mutations.go` | ✅ 完成 |
| N4 | UpdateForward 支持更新 speed_id | `go-backend/internal/store/repo/repository_mutations.go` | ✅ 完成 |
| N5 | Forward 页面新增限速选择并透传 speedId | `vite-frontend/src/pages/forward.tsx` | ✅ 完成 |
| N6 | Forward 相关契约测试补充 speedId 写入/清空断言 | `go-backend/tests/contract/forward_contract_test.go` | ✅ 完成 |

---

## 三、优先级说明

限速规则应用优先级:
1. **Forward.SpeedID** - 转发级别的限速 (最高优先)
2. **UserTunnel.SpeedID** - 用户隧道权限级别的限速 (回退)

---

## 四、数据库兼容性

- SpeedLimit 表: `tunnel_id` 和 `tunnel_name` 字段改为可空 (GORM AutoMigrate 自动处理)
- Forward 表: 新增 `speed_id` 可空字段 (GORM AutoMigrate 自动处理)

---

## 五、验证检查项

### 5.1 功能验证（审计后）

- [x] 创建不限速规则的限速 (不绑定隧道)
- [x] 创建绑定隧道的限速 (兼容旧逻辑)
- [x] 编辑限速规则，切换隧道绑定状态
- [ ] 删除限速规则
- [ ] 转发列表正确显示 speedId

### 5.2 API 验证（审计后）

- [x] GET /api/speed-limit/list 返回可选 tunnelId
- [x] POST /api/speed-limit/create 接受可选 tunnelId
- [x] POST /api/speed-limit/update 接受可选 tunnelId
- [ ] GET /api/forward/list 返回 speedId

### 5.3 兼容性验证（审计后）

- [x] 现有绑定隧道的限速规则继续正常工作
- [ ] 现有 UserTunnel 的限速继续正常工作
- [ ] 备份/恢复功能正常

### 5.4 Forward 独立限速闭环验证（新增）

- [x] POST /api/forward/create 接受 speedId 并写入 `forward.speed_id`
- [x] POST /api/forward/update 可更新/清空 speedId
- [x] Forward 表单可选择限速并提交 speedId
- [ ] `syncForwardServices` 实际使用 Forward.SpeedID 而非仅回退 UserTunnel.SpeedID
