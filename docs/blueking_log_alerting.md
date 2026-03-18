# 蓝鲸日志关键词告警方案

## 目标

为 `xdom` 提供一版可演示的日志关键词告警规则，使蓝鲸在命中关键错误日志时能够：

- 触发日志告警
- 携带稳定日志字段进入编排层
- 关联 release / trace / 候选源码文件
- 输出源码分析结论并收敛候选负责人

## 推荐第一批关键词

### 支付超时

- `payment timeout injected`
- `order left pending after payment timeout`

推荐字段过滤：

- `fault_mode=payment_timeout`
- `error_code=payment_timeout`

推荐级别：

- `warning`

## 支付失败

- `payment charge failed`
- `order payment failed`

推荐字段过滤：

- `fault_mode=payment_error`
- `error_code=payment_charge_failed` 或 `error_code=order_payment_failed`

推荐级别：

- `critical`

## Worker Panic

- `worker panic recovered`

推荐字段过滤：

- `fault_mode=worker_panic`
- `error_code=worker_panic`

推荐级别：

- `critical`

## 库存冲突

- `inventory conflict injected`
- `inventory conflict detected`

推荐字段过滤：

- `fault_mode=inventory_conflict`
- `error_code=inventory_conflict`

推荐级别：

- `warning`

## 告警 payload 最小字段集

建议蓝鲸告警回调至少输出：

- `event_type=alert.fired`
- `service`
- `env`
- `alert_rule`
- `severity`
- `summary`
- `status`
- `occurrences`
- `log_keyword`
- `message`
- `fault_mode`
- `error_code`
- `trace_id`
- `span_id`
- `order_id`
- `code_location`
- `stack_trace`
- `related_release_id`

示例：

```json
{
  "event_type": "alert.fired",
  "service": "xdom",
  "env": "prod",
  "source": "blueking-log-alert",
  "payload": {
    "alert_kind": "log_keyword",
    "alert_rule": "worker-panic-keyword",
    "severity": "critical",
    "summary": "Worker panic keyword matched in xdom logs",
    "status": "open",
    "occurrences": 1,
    "log_keyword": "worker panic recovered",
    "message": "worker panic recovered",
    "fault_mode": "worker_panic",
    "error_code": "worker_panic",
    "trace_id": "trace-id-placeholder",
    "span_id": "span-id-placeholder",
    "code_location": "internal/worker/runner.go:runOnce",
    "stack_trace": "stack-trace-placeholder",
    "related_release_id": "rel-placeholder"
  }
}
```

## 源码分析预期

编排层收到这类 payload 后，会进一步：

- 结合 `log_keyword` / `fault_mode` / `error_code` 匹配候选模块和文件
- 结合 `related_release_id` / `commit_sha` 关联最近发布
- 生成 RCA 输入
- 收敛候选负责人
- 输出 TAPD / 通知所需的源码分析结论
