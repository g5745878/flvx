# 028 多出口节点按延迟优先选择

## 目标

- 为隧道转发的多出口节点增加“按网络延迟优先”选择能力。
- 保持现有 `fifo` / `round` / `rand` / `hash` 行为不变，新增策略仅在显式选择时生效。
- 让延迟测量发生在真实发起拨号的节点侧，而不是面板侧，避免错误的全局最优假设。

## 当前结论

- [x] 确认前端已经支持多出口节点与统一策略选择。
- [x] 确认后端会把多出口节点组装成单个 chain hop，并通过 `selector.strategy` 控制出口选择。
- [x] 确认当前运行时 selector 仅支持 `round` / `rand` / `fifo` / `hash`，没有官方 `latency` 策略。
- [x] 确认项目已有节点侧 `TcpPing` 诊断能力，但尚未接入运行时选路。

## Checklist

- [x] 设计新策略名与兼容规则，采用 `latency` 作为对外配置值，并兼容 `rtt` / `fastest` 别名。
- [x] 在 `vite-frontend` 隧道出口策略下拉中增加“延迟优先”选项，并保持旧值回显兼容。
- [x] 在后端隧道创建/更新请求处理链路中允许新策略透传，不改变旧策略默认值。
- [x] 在 `go-gost/x/config/parsing/selector` 中注册新策略解析分支。
- [x] 在 `go-gost/x/selector` 中实现延迟感知 selector，支持最近 RTT 缓存、首次探测与失败降级。
- [x] 明确延迟探测目标与刷新模型，采用首次有限同步探测 + 缓存过期后的异步刷新，避免每次选路阻塞。
- [x] 为多入口、多跳场景定义 RTT 缓存粒度，按当前 agent 对 `target network + remoteAddr + candidate node` 的观测结果缓存。
- [x] 明确探测实现方案，首版直接在 agent 内执行完整路径 TCP connect probe，不复用控制面 `TcpPing`。
- [x] 为探测失败、全部候选超时、缓存为空等情况设计回退策略。
- [x] 增加单元测试覆盖 selector 策略解析、最低 RTT 选择与失败回退。
- [x] 增加后端测试覆盖多出口延迟优先策略的配置下发与兼容行为。
- [x] 运行最小必要测试并记录结果。

## 实施建议

1. 第一阶段只做“按最近 RTT 选择最快节点”，不引入加权、平滑、抖动窗口等复杂调度。
2. RTT 探测应后台定时刷新，selector 读取缓存后立即决策，避免把实时探测放进请求热路径。
3. 当 RTT 数据缺失时，优先回退到 `round` 或 `fifo`，避免新增策略导致链路完全不可用。
4. 首版不做跨节点共享 RTT；每个 agent 只维护“自己看到的出口质量”，这更符合真实路径。
5. UDP 不参与 RTT 排序；当请求网络不是 TCP 时直接回退到现有策略，不因为 TCP probe 失败否决转发规则。

## 风险点

- 延迟最低不等于吞吐最好，可能对长连接或高丢包链路不一定最优。
- 频繁 TCP 探测会放大节点到节点的背景流量，需要限制周期和超时。
- 多入口节点会观察到不同 RTT，不能把某个入口节点的测量结果直接复用给其他入口节点。
- 远程联邦节点如果无法直接执行探测，需要明确使用本地代理诊断还是跳过延迟优化。

## 验证思路

- 构造两个出口节点，其中一个注入更高连接延迟，验证 selector 长期偏向低 RTT 节点。
- 将低 RTT 节点临时置为超时，验证策略能回退到其他可用节点。
- 删除 RTT 缓存或首次启动时，验证链路仍可建立且不会卡死在探测阶段。
- 使用旧配置导入/更新，验证未选择“延迟优先”的隧道行为完全不变。

## 实施记录

- `latency` 策略已接入 `go-gost/x/config/parsing/selector`，仅对 hop 内节点选择生效，不影响 chain group selector。
- `hop.Select` 现在会把最终目标 `network + remoteAddr` 写入上下文，供 selector 在选下一条路径时做完整路径探测。
- selector 首次无缓存时会对候选节点做有限时的并发 TCP connect probe；当节点具备 transport 时，probe 会实际走 `Dial -> Handshake -> Connect(remoteAddr)`，测量“当前 agent 经该节点到最终目标”的建连耗时。
- RTT 缓存键已细化为 `target network + remoteAddr + candidate node`，避免不同目标地址共享错误的测量结果。
- 当请求网络不是 TCP 时，`latency` 不做 RTT 排序，直接回退到现有 selector；当所有 TCP probe 都失败时，同样回退到现有 round-robin，避免因为测量失败导致选路完全不可用。
- 前端已在“转发链节点”和“出口节点”两处策略下拉中增加“延迟优先”。

## 测试记录

- 命令：`cd go-gost/x && go test ./selector ./config/parsing/selector`
- 结果：通过。
- 命令：`cd go-backend && go test ./internal/http/handler -run 'TestBuildTunnelChainConfig_PreservesLatencyStrategy|TestBuildTunnelChainServiceConfig_UsesConnectIPForListen|TestBuildTunnelChainServiceConfig_FallsBackToNodeListenAddr|TestBuildTunnelChainServiceConfig_DefaultListenAddrWhenConnectIPEmpty'`
- 结果：通过。
