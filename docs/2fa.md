# 管理员两步验证（TOTP）

面板支持标准 RFC 6238 TOTP，兼容 Google Authenticator、1Password、Bitwarden、Microsoft Authenticator 等应用。

## 启用

1. 登录面板，进入 **设置 → 安全 → 两步验证**；
2. 点击“开始启用 2FA”；
3. 使用验证器扫描二维码，或手动输入密钥；
4. 保存一次性恢复码（只显示一次）；
5. 输入验证器生成的 6 位验证码，点击“确认启用”。

启用后登录流程：

```
用户名 + 密码  →  6 位动态验证码 / 恢复码  →  会话
```

密码正确后服务端返回一个 5 分钟有效的 challenge，不会直接签发会话；challenge 只能用于 `/api/login/2fa`，不能当普通会话使用。

## 恢复码

- 每次生成 10 个，格式如 `ABCDE-FGHIJ`；
- 每个只能使用一次，使用后从数据库移除；
- 可在 **设置 → 安全** 中重新生成（需要当前密码 + 动态验证码/恢复码）；
- 恢复码哈希使用 `data/jwt.secret` 作为 HMAC 密钥；TOTP 密钥使用 `data/cert.key` 加密。

## 安全细节

- TOTP 密钥使用 AES-256-GCM 加密后存入 `panel.db`；
- `data/cert.key` 与 `data/jwt.secret` 都会随面板备份归档保存；
- 密码登录和 2FA 校验分别限速；单个 challenge 最多 5 次尝试，成功后立即失效；
- 重置密码不会自动关闭 2FA；关闭 2FA 需要当前密码 + 验证码/恢复码；
- API Key 不能调用 2FA 管理接口，必须使用管理员会话。

## 无法登录时

优先使用恢复码。如果验证器和恢复码都丢失：

1. 停止面板；
2. 备份数据库；
3. 执行：

```sql
UPDATE admins
SET totp_enabled = 0,
    totp_secret = '',
    totp_pending = '',
    totp_recovery = '[]',
    totp_pending_recovery = '[]'
WHERE username = 'admin';
```

4. 重启面板，用密码登录后重新启用 2FA。

如果 `data/cert.key` 丢失但 `data/jwt.secret` 仍在，恢复码仍然有效，可以用恢复码关闭 2FA；如果两个密钥都丢失，按上面的数据库方式重置。
