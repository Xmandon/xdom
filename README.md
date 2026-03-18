# xdom

`xdom` 是一个 Go 单体 Web 服务试点仓库，用来承载后续的研发自动化闭环演练。

当前版本已经从简单 demo 升级为“订单处理型单体服务”，包含：

- 订单创建、查询、取消
- SQLite 持久化
- 模拟下游支付调用
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
go run ./cmd/xdom
```

## 故障注入

调用 `POST /admin/fault` 并带上 `X-Admin-Token` 请求头：

```json
{
  "mode": "payment_timeout",
  "delay_ms": 1500
}
```

支持的 `mode`：

- `none`
- `payment_timeout`
- `payment_error`
- `db_slow_query`
- `db_write_error`
- `inventory_conflict`
- `worker_panic`
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
- `active_pending_orders`

## 部署

查看：

- `deploy/config.env.example`
- `deploy/systemd/xdom.service`
- `scripts/deploy_binary.sh`

流水线里推荐这样调用：

```bash
chmod +x scripts/deploy_binary.sh

DEPLOY_DIR="/opt/xdom" \
SERVICE_NAME="xdom" \
LOCAL_BINARY="xdom" \
LOCAL_ENV_FILE="deploy/config.env.example" \
LOCAL_SYSTEMD_FILE="deploy/systemd/xdom.service" \
HEALTHCHECK_URL="http://127.0.0.1:8080/healthz" \
bash scripts/deploy_binary.sh
```

这个脚本适用于“蓝盾流水线就在目标 CVM 本机执行部署”的场景，因此不再需要 `DEPLOY_HOST` 和 `DEPLOY_USER`。

当前这版默认以 `root` 用户执行部署和启动服务，因此脚本里也不再调用 `sudo`。

重新部署时，脚本会先停止已经运行中的 `xdom.service`，再替换部署目录下的二进制，最后再重新拉起服务。

这样做是为了避免旧进程仍占用 `/opt/xdom/bin/xdom`，导致重复部署时 `cp` 覆盖二进制失败。

如果你的流水线构建产物不是当前目录下的 `xdom`，把 `LOCAL_BINARY` 改成实际产物路径即可。

## 验证

接入蓝鲸观测后的验证建议见：

- `docs/blueking_validation.md`
