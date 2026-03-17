# xdom

`xdom` 是一个 Go 单体 Web 服务试点仓库，用来承载后续的研发自动化闭环演练。

当前第一版包含：

- Go Web 服务
- 健康检查接口
- 演示业务接口
- 故障注入接口
- 基础指标接口
- CVM 二进制部署脚本
- `systemd` 服务文件

## 接口

- `GET /healthz`
- `GET /api/demo`
- `POST /admin/fault`
- `GET /metrics`
- `GET /version`

## 本地运行

```bash
go run ./cmd/xdom
```

## 故障注入

调用 `POST /admin/fault` 并带上 `X-Admin-Token` 请求头：

```json
{
  "mode": "timeout",
  "delay_ms": 1500
}
```

支持的 `mode`：

- `none`
- `timeout`
- `error500`
- `panic`
- `health_fail`

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

如果你的流水线构建产物不是当前目录下的 `xdom`，把 `LOCAL_BINARY` 改成实际产物路径即可。
