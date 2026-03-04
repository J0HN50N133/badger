# q_learning_savings

离散时间模拟实验：评估 `Q-learning` 调度策略相对基线策略的成本节省。

## 设计映射

该实验按你的建模实现：

1. 连续观测：
   - `x_t=(rho_t, c_hat_t, w_hat_t, s_hat_t, r_hat_t)`
2. 分桶离散：
   - `b_t^(i)=B_i(x_t^(i); Tau_i)`
3. 状态编码：
   - `s_t=(b_t^(1),...,b_t^(5))`
4. 动作集合：
   - `A={wait, trigger_gc}`
5. 回报：
   - `r_t=-(w_s*DeltaC_store + w_g*C_gc + w_r*DeltaC_scan)`
6. ROI 文件选择：
   - 先过滤 `b_net(f)>0`
   - 再按 `ROI(f)` 降序，在预算 `B_t` 下贪心选择

## 配置

所有运行参数来自 JSON，必须传入配置文件路径：

```bash
go run ./experiment/q_learning_savings ./experiment/q_learning_savings/config.example.json
```

关键配置块：

- `discretizer`：`Tau_1..Tau_5` 阈值
- `learning`：`eta, gamma, epsilonStart, epsilonMin, epsilonDecay`
- `cost`：成本权重与价格参数
- `environment`：负载与数据演化参数
- `gc`：候选文件、预算、ROI 估计参数
- `baseline`：对照策略触发阈值
- `safetyGate`：`deltaE, deltaB` 安全门控

## 输出

程序会输出：

- `q_learning.mean_cost`
- `baseline.mean_cost`
- `mean_savings`
- `mean_savings_pct`
- 分位数节省（`p50/p75/p90`）
- 双方成本分解（`store/gc/scan`）

`mean_savings > 0` 表示 Q-learning 比基线更省钱。
