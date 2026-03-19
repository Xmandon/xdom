# xdom

`xdom` 是一个 Go 单体 Web 服务试点仓库，用来承载后续的研发自动化闭环演练。

当前版本已经从简单 demo 升级为“订单处理型单体服务”，包含：

- 订单创建、查询、取消
- SQLite 持久化
- 最小下游支付服务 `xpay`
- 后台 worker 处理超时订单
- OpenTelemetry traces / metrics / logs 接入基础设施
- 内嵌式 Web 控制台
- 故障注入接口
- CVM 二进制部署脚本
- `systemd` 服务文件

## 接口

- `GET /healthz`
- `POST /api/orders`
- `GET /api/orders/{id}`
- `POST /api/orders/{id}/cancel`
- `GET /api/inventory`
- `POST /admin/fault`
- `GET /metrics`
- `GET /version`
- `GET /ui`

## Web 控制台

浏览器可直接访问：

- `http://127.0.0.1:8080/ui`
- `http://<CVM_IP>:8080/ui`

控制台首版支持：

- 查看健康状态、版本信息、当前故障模式
- 查看库存
- 创建订单
- 查询和取消订单
- 手动触发故障注入

故障注入需要你在页面里输入 `ADMIN_TOKEN`，页面不会在前端代码里硬编码这个值。

## 本地运行

```bash
go run ./cmd/xpay
go run ./cmd/xdom
```

## 故障注入

`xdom` 的故障注入只保留订单与存储相关故障。调用 `POST /admin/fault` 并带上 `X-Admin-Token` 请求头：

```json
{
  "mode": "worker_panic",
  "delay_ms": 1500
}
```

支持的 `mode`：

- `none`
- `db_slow_query`
- `db_write_error`
- `inventory_conflict`
- `worker_panic`
- `health_fail`

支付相关故障现在下沉到 `xpay`，通过 `http://127.0.0.1:8081/admin/fault` 注入，支持：

- `none`
- `payment_timeout`
- `payment_error`
- `health_fail`

## 蓝鲸观测配置

按照 iWiki 文档《Go（OpenTelemetry SDK）接入》的要求，至少需要配置：

- `TOKEN`
- `OTLP_ENDPOINT`
- `SERVICE_NAME`
- `ENABLE_TRACES`
- `ENABLE_METRICS`
- `ENABLE_LOGS`

注意：

- `OTLP_ENDPOINT` 不要带 `http://` 前缀
- Header 会自动使用 `x-bk-token`
- `service.name` 会使用 `SERVICE_NAME`
- 默认会按较短周期持续导出 telemetry，便于在低流量场景下也稳定看到 traces / logs / metrics

可选的持续上报调优项：

- `OTLP_INSECURE`
- `OTLP_EXPORT_INTERVAL_SEC`
- `OTLP_EXPORT_TIMEOUT_MS`
- `OTLP_TRACE_BATCH_TIMEOUT_MS`
- `OTLP_LOG_BATCH_TIMEOUT_MS`
- `HEARTBEAT_LOG_INTERVAL_SEC`

当前 trace 语义分层：

- HTTP 入站请求由 `otelhttp` 自动生成 `Server` span
- 订单编排和 worker 周期任务使用 `Internal` span
- `xdom` 的 `payment.charge` 使用 `Client` span
- `xpay` 的 `/charge` 使用 `Server` span

## 业务指标

服务会围绕订单流程输出业务指标，重点包括：

- `orders_created_total`
- `orders_paid_total`
- `orders_cancelled_total`
- `order_create_duration_seconds`
- `inventory_reserve_failed_total`
- `payment_charge_failed_total`
- `payment_timeout_total`
- `worker_jobs_processed_total`
- `worker_jobs_failed_total`
- `service_heartbeat_total`
- `service_last_heartbeat_unixtime`
- `active_pending_orders`

## 部署

查看：

- `deploy/config.env.example`
- `deploy/systemd/xdom.service`
- `deploy/systemd/xpay.service`
- `scripts/deploy_binary.sh`

流水线里推荐分别部署两个 binary：

```bash
chmod +x scripts/deploy_binary.sh

DEPLOY_DIR="/opt/xdom" \
SERVICE_NAME="xdom" \
LOCAL_BINARY="xdom" \
LOCAL_SYSTEMD_FILE="deploy/systemd/xdom.service" \
HEALTHCHECK_URL="http://127.0.0.1:8080/healthz" \
bash scripts/deploy_binary.sh

DEPLOY_DIR="/opt/xdom" \
SERVICE_NAME="xpay" \
LOCAL_BINARY="xpay" \
LOCAL_SYSTEMD_FILE="deploy/systemd/xpay.service" \
HEALTHCHECK_URL="http://127.0.0.1:8081/healthz" \
bash scripts/deploy_binary.sh
```

这个脚本适用于“蓝盾流水线就在目标 CVM 本机执行部署”的场景，因此不再需要 `DEPLOY_HOST` 和 `DEPLOY_USER`。

当前这版默认以 `root` 用户执行部署和启动服务，因此脚本里也不再调用 `sudo`。

重新部署时，脚本会先停止已经运行中的 `xdom.service`，再替换部署目录下的二进制，最后再重新拉起服务。

这样做是为了避免旧进程仍占用 `/opt/xdom/bin/xdom`，导致重复部署时 `cp` 覆盖二进制失败。

部署脚本不会覆盖 `/opt/xdom/conf/config.env`。运行时配置需要你提前在目标机准备好，并在目标机本地持续维护。

`deploy/config.env.example` 只作为初始化参考模板，不再作为部署脚本的输入参数。

如果你的流水线构建产物不是当前目录下的 `xdom` 或 `xpay`，把 `LOCAL_BINARY` 改成实际产物路径即可。

## 验证

接入蓝鲸观测后的验证建议见：

- `docs/blueking_validation.md`

建议额外核对：

- 服务空闲 1-2 分钟时，metrics 仍按 `OTLP_EXPORT_INTERVAL_SEC` 持续刷新
- 空闲期仍能看到低频 `telemetry heartbeat` 日志
- 正常下单时 trace 能看到 `xdom Server -> Internal -> xdom Client -> xpay Server` 的层次
- 对 `xpay` 注入 `payment_timeout` 后，`xdom` 与 `xpay` 两侧日志都能通过同一条 trace 关联
