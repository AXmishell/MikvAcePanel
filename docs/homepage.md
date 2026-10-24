# 自定义首页

面板根路径 `/` 默认显示内置落地页。管理员可以在 **设置 → 首页** 上传自定义 HTML，或使用 ZIP 同时上传 CSS、JavaScript、图片和字体。

## 两种模式

| 模式 | 行为 |
| --- | --- |
| 内置落地页 | 使用 `HomeView.vue` 渲染的默认首页 |
| 自定义 HTML | 返回一个全屏沙箱 iframe，加载 `/homepage/index.html` |

切换模式、启用某个上传版本或恢复内置首页都不需要重启面板。

## 安全模型

自定义页面**可以执行 JavaScript**，但运行在 opaque origin 中：

- `/` 只是一个可信的薄壳，真正的内容位于 `<iframe sandbox="...">`
- iframe 没有 `allow-same-origin`，无法读取面板的 `localStorage`、Cookie 或父页面 DOM
- `/homepage/*` 响应同时带有 `Content-Security-Policy: sandbox ...`，直接打开该 URL 也会被隔离
- 自定义页面不能调用需要管理员 Bearer Token 的 API
- 沙箱页面的 Origin 是 `null`，同源 API 请求会被面板 CORS 策略阻止；页面应自带静态内容，或调用明确允许 `null` Origin 的外部接口
- `/admin`、`/user`、`/api`、`/install` 不会被自定义页面覆盖

!!! warning "仍然需要信任上传内容"

    沙箱阻止页面读取管理员登录态，但不能阻止它伪装界面、诱导访问者输入信息，或跳转到外部网站。只上传自己信任的 HTML/ZIP。

## 上传格式与限制

- 单文件：`.html` / `.htm`，最大 2 MB
- 打包：`.zip`，最大 25 MB；解压后最大 50 MB，最多 200 个文件
- 必须包含 `index.html`；如果 ZIP 只有一个顶层目录，会自动去掉该目录
- 允许的扩展名：HTML、CSS、JS/MJS、JSON、Map、图片、SVG、字体、音视频、PDF、WASM
- 拒绝路径穿越、符号链接、加密 ZIP、嵌套 ZIP 和未知扩展名
- 每次上传生成独立版本，保留最近 5 个版本；当前版本不会被自动清理

页面内使用**相对路径**引用资源，例如：

```html
<link rel="stylesheet" href="./assets/style.css">
<script src="./assets/app.js"></script>
<img src="./assets/logo.png" alt="">
```

这样在正式页面和预览页面中都能正确解析。

## 预览与启用

1. 上传 `.html` 或 `.zip`
2. 在版本列表中点 **预览**，通过短时 token 在沙箱 iframe 中查看
3. 确认后点 **启用**，或把模式切换为“自定义 HTML”
4. 出现问题时点 **恢复内置首页**，上传文件会保留

预览链接默认 10 分钟失效，面板自身日志会对 token 做脱敏；如果反向代理记录完整 URL，token 仍可能出现在代理日志中，因此不要把预览链接长期公开。

## 接口

### 公开

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/` | 内置首页或自定义页薄壳 |
| GET | `/homepage/*` | 当前启用版本的静态资源 |
| GET | `/preview/homepage/:token/*` | 短时预览，10 分钟有效 |

### 管理员

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/homepage` | 当前模式、启用版本、版本列表 |
| POST | `/api/homepage/upload` | 上传 `.html` / `.zip` |
| POST | `/api/homepage/activate` | 启用指定版本 |
| POST | `/api/homepage/preview` | 生成预览链接 |
| PUT | `/api/homepage/mode` | `builtin` / `upload` |
| DELETE | `/api/homepage/versions/:version` | 删除非当前版本 |
| DELETE | `/api/homepage` | 恢复内置首页 |

## 存储

```text
<data_dir>/homepage/
  versions/
    20261024-173000-a1b2c3/
      index.html
      manifest.json
      assets/
```

- 版本 ID 使用时间戳和随机后缀
- `manifest.json` 记录来源文件名、文件数、大小、上传时间和 SHA256
- 写入使用临时目录 + 原子重命名，失败不会影响正在使用的版本
- SQLite 备份会包含当前选择的自定义首页版本；恢复后在下一次启动时换入
- PostgreSQL / MySQL 的多实例部署中，`data_dir/homepage` 必须位于共享存储，否则不同实例可能返回不同的首页或回退到内置页
- 预览 token 保存在当前进程内存中，多实例下预览请求可能落到另一台实例并返回 404；正式启用的页面不受影响

## 搜索引擎

自定义首页始终返回 `noindex, nofollow`，并且内容位于 iframe 中，不适合作为 SEO 落地页。需要搜索引擎收录时，应把首页部署到独立域名，而不是使用本功能。
