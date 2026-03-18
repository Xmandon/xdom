# 蓝鲸业务指标方案

## 目标

基于当前 `xdom` 已落地的埋点，整理一版可直接用于蓝鲸指标接入、Dashboard 设计和告警配置的业务指标字典。

说明：

- 本文聚焦业务指标，不展开通用 HTTP 指标（如 `http_requests_total`、`http_request_duration_seconds`）
- 盘点范围以当前代码已实现内容为准
- 除计划中列出的 10 个核心业务指标外，代码里还额外实现了一个故障演练观测指标 `fault_activations_total`

## 1. 当前已实现指标与采集位置

| 指标名 | 类型 | 代码定义位置 | 采集位置 | 触发时机 | 当前维度 |
| --- | --- | --- | --- | --- | --- |
| `orders_created_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/order/service.go` `CreateOrder()` -> `RecordOrderCreated()` | 订单创建流程完成、支付成功、订单状态更新为 `paid` 后累加 | `payment_channel` |
| `orders_paid_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/order/service.go` `CreateOrder()` -> `RecordOrderPaid()` | 支付成功且订单状态持久化为 `paid` 后累加 | `payment_channel` |
| `orders_cancelled_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/order/service.go` `CancelOrder()` / `CancelExpiredOrders()` -> `RecordOrderCancelled()` | 手工取消订单，或 worker 取消过期订单时累加 | `result` |
| `order_create_duration_seconds` | Histogram | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/order/service.go` `CreateOrder()` -> `RecordOrderCreated()` | 订单创建成功路径中记录从进入 `CreateOrder()` 到订单完成支付的耗时 | `payment_channel` |
| `inventory_reserve_failed_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/order/service.go` `CreateOrder()` -> `RecordInventoryFailure()` | 创建待支付订单时，库存预占/订单入库失败时累加 | `result` |
| `payment_charge_failed_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/order/service.go` `CreateOrder()` -> `RecordPaymentFailure()` | 支付扣款失败且不属于超时挂起场景时累加 | `result` |
| `payment_timeout_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/order/service.go` `CreateOrder()` -> `RecordPaymentTimeout()` | 支付调用超时，订单保持 `pending` 等待后续对账/worker 处理时累加 | 无 |
| `worker_jobs_processed_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/worker/runner.go` `runOnce()` / `internal/order/service.go` `CancelExpiredOrders()` -> `RecordWorkerProcessed()` | worker 周期运行成功时记一次 `tick`；每成功取消一笔过期订单时再记一次 `expired_cancelled` | `result` |
| `worker_jobs_failed_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/worker/runner.go` `runOnce()` / `internal/order/service.go` `CancelExpiredOrders()` -> `RecordWorkerFailed()` | worker 注入 panic、恢复 panic、取消过期订单失败或执行取消流程报错时累加 | `result` |
| `active_pending_orders` | Observable Gauge | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/app/application.go` `SetActivePendingOrdersProvider()` -> `repo.CountActivePendingOrders()` | 指标采集回调触发时，实时查询当前 `pending` 订单数并上报 | 无 |
| `fault_activations_total` | Counter | `internal/telemetry/telemetry.go` `initInstruments()` | `internal/httpapi/handler.go` `handleFault()` -> `RecordFaultActivation()` | 管理接口切换故障模式时累加，用于故障演练观察 | `fault_mode` |

## 2. 指标字典

### 2.1 订单流量与结果类

| 指标名 | 中文别名 | 类型 | 单位 | 口径说明 | 推荐维度说明 |
| --- | --- | --- | --- | --- | --- |
| `orders_created_total` | 订单创建总数 | Counter | 次 | 成功走完整个创建链路，并且订单已更新为 `paid` 的次数。当前口径本质上是“成功完成创建且支付成功的订单数”，不是“请求到达数”。 | 保留 `payment_channel`，用于区分不同支付渠道的转化情况。不要扩展 `order_id`、`user_id`。 |
| `orders_paid_total` | 订单支付成功总数 | Counter | 次 | 支付成功且订单状态已写回为 `paid` 的次数。当前与 `orders_created_total` 在成功路径上同时增长。 | 保留 `payment_channel`，用于渠道支付成功率分析。 |
| `orders_cancelled_total` | 订单取消总数 | Counter | 次 | 订单被取消的次数，既包含人工取消，也包含 worker 自动取消过期订单。 | 保留 `result` 作为取消原因，如 `expired`、业务取消原因等；建议收敛为有限枚举值。 |
| `order_create_duration_seconds` | 订单创建耗时 | Histogram | 秒 | 从 `CreateOrder()` 入口开始，到支付成功并完成状态写回为止的整段耗时。失败路径和超时挂起路径当前不会写入该直方图。 | 保留 `payment_channel`，用于对比不同支付渠道的创建时延。 |

### 2.2 支付稳定性类

| 指标名 | 中文别名 | 类型 | 单位 | 口径说明 | 推荐维度说明 |
| --- | --- | --- | --- | --- | --- |
| `payment_charge_failed_total` | 支付扣款失败次数 | Counter | 次 | 支付调用返回失败，且不属于“支付超时后订单保持 pending”的场景时累加。 | 当前使用 `result` 承载 `err.Error()`，存在高基数风险。建议收敛为固定错误码或原因枚举，如 `payment_error`、`channel_rejected`。 |
| `payment_timeout_total` | 支付超时次数 | Counter | 次 | 支付调用超时，订单未立即失败，而是保持 `pending` 等待后续 reconciliation/worker 处理时累加。 | 当前无维度。若后续需要细分，可仅增加 `payment_channel`，不要挂 `order_id`。 |

### 2.3 库存冲突类

| 指标名 | 中文别名 | 类型 | 单位 | 口径说明 | 推荐维度说明 |
| --- | --- | --- | --- | --- | --- |
| `inventory_reserve_failed_total` | 库存预占失败次数 | Counter | 次 | 创建待支付订单时，库存预占或订单入库阶段失败的次数。 | 当前使用 `result` 承载 `err.Error()`，同样存在高基数风险。建议改为稳定原因枚举，如 `inventory_conflict`、`db_write_error`。 |

### 2.4 Worker 处理类

| 指标名 | 中文别名 | 类型 | 单位 | 口径说明 | 推荐维度说明 |
| --- | --- | --- | --- | --- | --- |
| `worker_jobs_processed_total` | Worker 处理成功次数 | Counter | 次 | 当前口径同时统计两类事件：`tick` 表示一次调度循环成功完成；`expired_cancelled` 表示成功取消一笔过期订单。该指标更像“worker 成功事件数”，而非单一 job 数。 | 保留 `result`，并限制在少量稳定枚举，如 `tick`、`expired_cancelled`。 |
| `worker_jobs_failed_total` | Worker 处理失败次数 | Counter | 次 | 统计 worker 运行失败事件，包括注入 panic、panic 恢复、取消过期订单失败、执行取消流程报错等。 | 保留 `result`，并限制在稳定枚举，如 `panic`、`panic_recovered`、`cancel_expired`、`cancel_expired_failed`。 |

### 2.5 时延与积压类

| 指标名 | 中文别名 | 类型 | 单位 | 口径说明 | 推荐维度说明 |
| --- | --- | --- | --- | --- | --- |
| `active_pending_orders` | 当前待处理订单数 | Observable Gauge | 个 | 采集回调执行时实时查询数据库中 `status='pending'` 的订单数量，反映当前积压水位。 | 当前无维度，保持无维度即可，避免因业务实体标签导致基数膨胀。 |

### 2.6 故障演练观察类

| 指标名 | 中文别名 | 类型 | 单位 | 口径说明 | 推荐维度说明 |
| --- | --- | --- | --- | --- | --- |
| `fault_activations_total` | 故障模式切换次数 | Counter | 次 | 每次通过管理接口修改故障模式时累加，主要用于故障演练和观测联调验证。 | 保留 `fault_mode`，用于区分 `payment_timeout`、`inventory_conflict`、`worker_panic` 等演练模式。 |

## 3. 维度治理建议

建议保留的低基数维度：

- `payment_channel`
- `result`
- `fault_mode`

不建议作为指标维度的高基数字段：

- `order_id`
- `user_id`
- `trace_id`
- 原始错误消息全文

当前实现中的两个重点风险：

1. `inventory_reserve_failed_total`
   的 `result` 来自 `err.Error()`，不同错误文本可能导致标签爆炸。
2. `payment_charge_failed_total`
   的 `result` 同样来自 `err.Error()`，不利于蓝鲸聚合分析和告警收敛。

建议后续将失败原因统一收敛为稳定枚举值，例如：

- `inventory_conflict`
- `db_write_error`
- `payment_error`
- `payment_timeout`
- `expired`

## 4. 可直接用于告警和大盘的核心指标

建议优先使用以下指标作为第一版蓝鲸观测核心输入：

- `orders_created_total`：看订单成功流量趋势
- `orders_paid_total`：看支付成功趋势与转化
- `payment_timeout_total`：看支付超时异常
- `payment_charge_failed_total`：看支付失败突增
- `inventory_reserve_failed_total`：看库存冲突或下单链路异常
- `order_create_duration_seconds`：看 P95/P99 创建时延
- `worker_jobs_failed_total`：看 worker 处理异常
- `active_pending_orders`：看待处理订单积压

## 5. 补充说明

- `orders_created_total` 与 `orders_paid_total` 当前在同一成功路径上同时累加，因此两者短期内可能几乎一致。
- `worker_jobs_processed_total` 当前混合了“调度成功”和“业务处理成功”两类语义，配置大盘时建议按 `result` 维度拆开看。
- `active_pending_orders` 是可观测 Gauge，不是事件数累加；更适合做当前水位卡片和趋势图。
