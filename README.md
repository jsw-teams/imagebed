# imagebed

基于 Go + PostgreSQL + Cloudflare R2 的轻量图床后端示例。

## 功能概览

- 使用 Gin 构建 HTTP API
- 使用 PostgreSQL 存储桶信息 / 图片元信息
- Cloudflare R2（S3 兼容）作为对象存储
- 支持：
  - 新建 / 删除 R2 桶（以及 DB 中的记录）
  - 编辑桶最大写入存储量（配额）
- Cloudflare Turnstile 人机验证：
  - 上传图片
  - 创建 / 修改 / 删除桶
- 图片审查：
  - 一方：MIME 白名单 + 大小限制
  - 三方：预留 HTTP JSON 审查接口，可接自定义平台
- 安全：
  - 严格限制上传大小
  - 使用 `http.DetectContentType` 防止 Content-Type 绕过
  - 仅以 JSON / 重定向输出，不渲染用户 HTML
  - 设置常见安全响应头，防 XSS / clickjacking

## 配置示例

`config.example.json`:

```json
{
  "http": {
    "addr": ":8080"
  },
  "database": {
    "dsn": "postgres://user:pass@localhost:5432/imagebed?sslmode=disable"
  },
  "r2": {
    "account_id": "YOUR_R2_ACCOUNT_ID",
    "access_key_id": "YOUR_R2_ACCESS_KEY_ID",
    "secret_access_key": "YOUR_R2_SECRET",
    "region": "auto",
    "endpoint": "",
    "public_base_url": ""
  },
  "turnstile": {
    "enabled": true,
    "secret_key": "YOUR_TURNSTILE_SECRET"
  },
  "moderation": {
    "enabled": false,
    "third_party_endpoint": "",
    "third_party_api_key": ""
  },
  "app": {
    "max_upload_bytes": 10485760,
    "allowed_mime_types": [
      "image/jpeg",
      "image/png",
      "image/gif",
      "image/webp"
    ]
  }
}
