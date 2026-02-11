# Badger 改造计划：面向 KVCache 的 Token-Range Smart-GC

日期：2026-02-11
负责人：@thesis 对应实现

## 1. 目标与边界

目标：在不破坏 Badger 现有正确性（事务语义、崩溃恢复、LSM/vlog 协同）的前提下，引入面向 KVCache 场景的成本感知 GC 能力。

- 核心目标
  - 支持 `SessionID + TokenIndex` 维度的区间回收（Token-range GC）。
  - 支持成本模型驱动的 GC 调度（先规则，再 RL）。
  - 支持可选 Reorg-GC（为 range scan 优化布局）。
- 非目标（第一阶段不做）
  - 不改动 Badger 的事务隔离级别与对外 API 语义。
  - 不引入外部云依赖（S3/FaaS）作为硬依赖，仅保留可插拔接口。

## 2. 总体实施策略

采用“三层解耦 + 分阶段上线”策略：

- 数据层：在 value log/元数据中增加“可按 token-range 识别垃圾”的能力。
- 策略层：先提供 deterministic policy（阈值/ROI），再接入 RL policy。
- 执行层：复用 Badger 现有 GC 执行链路，新增 range task 与可选 reorg task。

## 3. 分阶段计划

## 阶段 A：可观测性与元数据打底（1-2 周）

交付物：能观测并统计“session/token 维度垃圾与读写行为”，但不改变 GC 语义。

- 代码落点
  - `value.go`：补充 vlog entry 统计钩子（垃圾字节、存活字节、扫描字节）。
  - `db.go`：注册周期采样任务与指标导出入口。
  - `y/`：新增内部指标结构（成本相关 counters/gauges）。
- 数据模型
  - 增加逻辑标签：`session_id`、`token_index`（从 key 编码解析，不改底层 key/value 格式）。
  - 增加区间统计：`[session, token_start, token_end] -> {live, garbage, scan_freq}`。
- 验收标准
  - 单测覆盖 key 解析、区间聚合、并发更新。
  - 压测下统计开销可控（CPU 与内存增量在可接受范围）。

## 阶段 B：Token-range GC 执行路径（2-3 周）

交付物：支持“指定 session + token 区间”的局部 GC 任务。

- 代码落点
  - `value.go`：新增 range-aware GC 入口（例如内部 `runValueLogGCForRange`）。
  - `txn.go` / `iterator.go`：复用现有可见性检查，确保仅搬迁 live value。
  - `manifest.go`（如需）：记录 range task 的关键进度，保证恢复可重放或安全跳过。
- 执行语义
  - 保持与现有 GC 一致的安全边界：只回收可判定垃圾，不破坏读可见性。
  - 失败可重试，保证幂等或至少“失败不伤数据”。
- 验收标准
  - 功能单测：局部区间 GC 前后数据一致。
  - 故障注入：中断/重启后 DB 可恢复且无可见性错误。

## 阶段 C：成本模型 + 规则调度器（1-2 周）

交付物：基于成本函数的规则调度器（非 RL），用于稳定基线。

- 成本模型
  - `C_store`：存储占用（live + garbage）的时间积分近似。
  - `C_gc`：执行代价（扫描字节、重写字节、请求次数、执行时长）。
  - `C_scan`：读路径放大代价（range scan 相关请求/字节）。
- 代码落点
  - `db.go`：调度 goroutine 与节流控制。
  - 新增 `gc_policy.go`：ROI 计算与 action 选择（Wait/TriggerRange/Batch/ReorgFlag）。
- 验收标准
  - 与现有 GC 阈值策略对比，能复现实验中的“成本下降趋势”。
  - 策略可配置、可关闭，默认不影响现有用户。

## 阶段 D：Reorg-GC（可选，1-2 周）

交付物：在 GC 时按 token 顺序重组写入，优化后续 range scan。

- 代码落点
  - `value.go`：新增 reorg 写入路径与批量写策略。
  - `levels.go` / `compaction.go`（必要时）：评估对 compaction 热点的副作用。
- 验收标准
  - range scan 的请求次数与读放大下降。
  - 不显著放大写路径尾延迟。

## 阶段 E：RL 调度器接入（2-4 周）

交付物：在规则策略之上挂载 RL policy（可热切换、可回退）。

- 代码落点
  - 新增 `gc_rl_policy.go`：状态、动作、奖励、在线更新。
  - `db.go`：policy interface（rule/RL）与 fail-safe 切换。
- 机制要求
  - 冷启动：bootstrap 使用规则策略采样。
  - 安全阈值：垃圾率/占用超过阈值时强制紧急 GC。
- 验收标准
  - 长跑实验中累计成本优于规则策略基线。
  - 训练异常时可自动回退，系统保持可用。

## 4. 测试与评估计划

- 单元测试
  - key 解析与 token-range 划分。
  - ROI 计算与 action 选择。
  - GC 中断恢复一致性。
- 集成测试
  - 高 churn 写入 + 会话失效 + range scan 读恢复。
  - 不同并发度、不同会话长度分布。
- 回归测试
  - 运行 `go test ./...`，确保不回归现有行为。
- 指标输出
  - 累计成本、GC 触发频次、回收效率、scan 放大、P99 延迟。

## 5. 风险与缓解

- 风险：元数据开销过大，拖慢写路径。
  - 缓解：异步聚合 + 抽样 + 分桶统计。
- 风险：局部 GC 与 MVCC 可见性边界冲突。
  - 缓解：复用现有 discard/可见性判定逻辑，不新增旁路。
- 风险：RL 在非平稳负载下震荡。
  - 缓解：规则基线兜底、动作节流、回退开关。

## 6. 近期执行顺序（建议）

1. 先做阶段 A（观测与统计）并补齐测试。
2. 完成阶段 B（可用的 token-range GC 原型）。
3. 上线阶段 C（规则调度），形成可复现实验基线。
4. 视实验收益再做阶段 D/E（Reorg 与 RL）。

## 7. 交付检查点

- M1：完成阶段 A + 基础指标看板。
- M2：完成阶段 B + 一致性与恢复测试通过。
- M3：完成阶段 C + 与 baseline 的成本对比报告。
- M4：完成阶段 D/E（如启用）+ 最终实验复现实验脚本。
