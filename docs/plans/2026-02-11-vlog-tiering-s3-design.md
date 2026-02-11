# Badger 单机三层存储设计（Memory -> Disk -> S3）

日期：2026-02-11  
状态：Draft（用于论文实现）

## 1. 目标与前提

本文档用于约束后续实现，先统一“我们到底要做什么”。

- 部署模型：单机 Badger。
- 存储层次：`Memory -> Disk -> S3 Object Storage`。
- 下沉对象：只下沉 `Value Log (vlog)`，LSM 不下沉。
- 论文目标：验证成本导向 GC/调度，不追求完整云原生多节点系统。

## 2. 为什么只下沉 vlog

- LSM（SST + Manifest）总体更小，且对尾延迟敏感，放本地盘更稳。
- vlog 容量更大、冷热分层更明显，最适合作为“可延迟访问”的冷数据层。
- 与 WiscKey/Badger 结构一致，改造边界清晰：尽量不触碰 LSM 正确性路径。

## 3. 数据路径（目标形态）

### 3.1 写路径

1. 前台写入仍按 Badger 现有流程：写 MemTable/WAL + 写当前活跃 vlog 文件。
2. 新数据先落本地 `ValueDir`（Disk 层），不直接写 S3。
3. 后台 offload worker 按策略将“冷 vlog 文件（fid）”上传到 S3。

### 3.2 读路径

1. 先按 value pointer 定位 fid/offset。
2. 若 fid 在本地，直接本地读。
3. 若 fid 仅在 S3，执行回源（download / prefetch）后再读。
4. 回源策略先做最小实现：按 fid 整文件回拉到本地缓存目录。

### 3.3 GC 路径

1. GC 仍按 Badger 的 rewrite 语义处理 live value。
2. 若 GC 处理到 remote fid，需要先确保可读（本地或回源）。
3. 旧 fid 在本地与远端都要做生命周期管理（删除策略后述）。

## 4. 元数据与状态机（MVP）

以 `fid` 为最小管理单元，维护以下状态：

- `LocalOnly`：只在本地。
- `Uploading`：正在上传到 S3。
- `RemoteOnly`：仅 S3 存在（本地可已删除）。
- `LocalAndRemote`：双副本并存（过渡或缓存态）。
- `Deleting`：正在删除（本地或远端）。

推荐状态迁移：

1. `LocalOnly -> Uploading -> LocalAndRemote`
2. `LocalAndRemote -> RemoteOnly`（裁剪本地）
3. `RemoteOnly -> LocalAndRemote`（回源）
4. `LocalOnly/LocalAndRemote/RemoteOnly -> Deleting -> (Removed)`

要求：迁移操作必须幂等，崩溃后可重放。

## 5. 配置语义（统一定义）

当前/计划中的关键配置：

- `ValueDir`：本地路径（POSIX 路径），用于 mmap 读写 vlog。
- `ValueLogOnObjectStorage`：启用对象存储模式。
- `ValueLogObjectStorageURL`：对象存储前缀（例如 `s3://bucket/prefix`）。

约束：

- `ValueDir` 不能直接是 URL（`s3://...`），因为现有 vlog I/O 依赖 mmap 文件。
- URL 仅用于对象键命名和远端定位，不替代本地文件语义。

## 6. 关键实现边界（先做什么，不做什么）

### 6.1 先做（MVP）

- fid 级别的上传/回源/删除。
- 本地优先读 + miss 回源。
- 崩溃恢复时重建 fid 位置信息（manifest sidecar 或独立元数据文件）。
- 与现有 GC 共存，不破坏现有一致性。

### 6.2 暂不做

- 条目级（entry-level）上传与远端随机读。
- 复杂分块、跨 fid 聚合对象。
- 多副本一致性协议。
- RL 调度（后续阶段接入）。

## 7. 一致性与恢复要求

- 上传成功确认前，不能把 fid 标记为 `RemoteOnly`。
- 删除远端前，必须确认该 fid 不再被任何有效 value pointer 引用。
- 启动恢复时，如果发现 `Uploading` 残留，必须可安全重试或回滚到 `LocalOnly`。
- 任一时刻失败都不能导致“value pointer 指向不可读 fid”。

## 8. 与论文实验的关系

该三层模型支持后续实验指标：

- 存储成本：本地容量 + S3 容量。
- 请求成本：PUT/GET/LIST 次数。
- 计算成本：后台 offload/GC worker 时间。
- 性能指标：回源命中率、回源延迟、GC 干扰。

这为后续 Smart-GC/ROI/RL 调度提供了可执行底座。

## 9. 开放问题（实现前需定稿）

1. 回源粒度：整 fid 下载 vs 分段下载。
2. 本地回源缓存上限与淘汰策略（LRU/Clock/基于会话）。
3. 远端对象命名规范（是否带 epoch/tenant/session 前缀）。
4. GC 与 offload 并发冲突的锁粒度（全局/按 fid）。

## 10. 下一步执行顺序

1. 先实现 fid 元数据与状态机持久化。
2. 再实现 offload worker（上传 + 状态切换 + 幂等重试）。
3. 接入读路径回源。
4. 最后接入 GC 的 remote-fid 感知。

