# Merchant portal (dev 环境真实联调工具)

`cmd/merchant-portal` 是一个本地「模拟商户站点」代理，用于对接任意环境的
V3 托管链路（典型场景：对接 AWS dev 部署）做真实浏览器测试。浏览器不直接
调用目标 API，所有请求由本地 Go 代理服务端转发，V2 HMAC 签名由代理计算，
因此没有 CORS 问题，也无需在浏览器里持有商户密钥。

```bash
# 默认对接本地 API
make merchant-portal

# 对接 dev 部署
PORTAL_API=https://market.afx-game.dev make merchant-portal
```

打开 `http://localhost:8091`，按页面步骤：

1. **探测环境**：GET `<target>/readyz`，确认部署可达。
2. **注册测试商户**：POST `/api/v1/merchants/register`（公开接口），
   自动把 `merchant_id / api_key / api_secret` 存到 localStorage。
3. **创建演示事件 + 市场**（需要 dev 的 admin key，由运维提供）：
   代理依次调用创建事件 → 激活事件 → 创建市场的 admin 接口。
4. **启动托管页**：代理以商户密钥计算 HMAC，POST `/api/v2/sessions`
   换取 `launch_url`，在新标签页打开 —— 托管前端在真实浏览器中走完
   launch → exchange → 行情 → 下单的完整链路。

前置条件：目标环境必须启用 V3 路由。dev 部署从 AWS Secrets Manager
`predictmarket/dev/application` 读取 `merchant_secret_encryption_key`、
`session_jwt_secret`、`hosted_ui_url`（见 `docs/DEPLOYMENT.md`）；三个键
缺失时部署照常但 V3 不启用，页面会提示。

## 说明

- 仅作开发/验收辅助，不进入生产链路；不要在生产环境用它保存凭据。
- 商户密钥只保存在浏览器 localStorage（本地机器）和本地代理内存中，不落盘。
