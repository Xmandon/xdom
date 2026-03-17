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
