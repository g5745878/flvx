# 029 latency selector 稳定性修复

## 目标

- 修复 `latency` selector 在缓存过期后不会重新探测的问题。
- 为按目标地址维度的 RTT 缓存增加过期清理，避免长期运行时单调增长。
- 验证修复不会让可用的 TCP/UDP 节点变得不可路由。

## Checklist

- [x] 审视 `latency` selector 与 `hop.Select` 的调用边界，限定修复范围。
- [x] 修复异步刷新逻辑，确保 stale RTT 会被重新探测并更新缓存。
- [x] 增加过期缓存清理，避免按目标地址分桶的 map 无界增长。
- [x] 为 stale refresh、缓存清理、TCP/UDP 路由回退补充测试。
- [x] 运行相关 Go 测试并记录结果。

## 实施记录

- `refreshAsync` 不再把已标记节点重新送回 `probeNodes`，而是直接进入新的 `probeMarked` 路径，避免 stale entry 卡死在 `probing` 集合里。
- `markForProbe` 在为当前请求保留 stale fallback 的同时，会清理其它目标地址下已过期的 RTT 缓存，控制 map 生命周期。
- 新增 selector 单测覆盖：首次探测后异步刷新、按目标地址清理过期缓存、UDP 绕过 probe、探测失败时回退。
- 新增 hop 单测覆盖：`hop.Select` 会把 `network + addr` 写入上下文，保证 selector 能区分 TCP/UDP 请求，但不会阻断可用节点选择。

## 测试记录

- 命令：`cd go-gost/x && go test ./selector ./hop ./config/parsing/selector`
- 结果：通过。
- 命令：`cd go-gost/x && go test -race ./selector ./hop`
- 结果：通过。
