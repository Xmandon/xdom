# 蓝鲸观测验证清单

## 1. 本地接口验证

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/version
curl -fsS http://127.0.0.1:8080/api/inventory
```

浏览器控制台入口：

- `http://127.0.0.1:8080/ui`
- `http://<CVM_IP>:8080/ui`

如果你想手工演练整条链路，优先从控制台完成一次：

- 查看健康状态和版本
- 创建订单
- 查询订单
- 注入并恢复故障

创建订单：

```bash
curl -fsS -X POST "http://127.0.0.1:8080/api/orders" \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "u-demo",
    "sku": "sku-book",
    "quantity": 1,
    "amount": 99.9,
    "payment_channel": "mockpay"
  }'
```

## 2. Trace 验证

- 在蓝鲸 APM 中按 `service.name=xdom` 检索
- 确认能看到这些 span：
  - `http.server`
  - `order.create`
  - `payment.charge`
  - `repository.create_pending_order`
  - `repository.reserve_inventory`
  - `worker.run_once`

## 3. Metrics 验证

检查是否已出现这些业务指标：

- `orders_created_total`
- `orders_paid_total`
- `orders_cancelled_total`
- `order_create_duration_seconds`
- `inventory_reserve_failed_total`
- `payment_charge_failed_total`
- `payment_timeout_total`
- `worker_jobs_processed_total`
- `worker_jobs_failed_total`
- `active_pending_orders`

## 4. Logs 验证

- 确认日志中可检索：
  - `order_id`
  - `trace_id`
  - `fault_mode`
  - `payment_channel`
  - `service=xdom`

## 5. 故障演练

### 支付超时

```bash
curl -fsS -X POST "http://127.0.0.1:8080/admin/fault" \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: replace-me" \
  -d '{"mode":"payment_timeout","delay_ms":2500}'
```

### 库存冲突

```bash
curl -fsS -X POST "http://127.0.0.1:8080/admin/fault" \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: replace-me" \
  -d '{"mode":"inventory_conflict","delay_ms":0}'
```

### Worker Panic

```bash
curl -fsS -X POST "http://127.0.0.1:8080/admin/fault" \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: replace-me" \
  -d '{"mode":"worker_panic","delay_ms":0}'
```

恢复默认：

```bash
curl -fsS -X POST "http://127.0.0.1:8080/admin/fault" \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: replace-me" \
  -d '{"mode":"none","delay_ms":0}'
```
