<template>
  <div>
    <div class="toolbar">
      <div>
        <div class="page-title">节点</div>
        <div class="dim small">{{ onlineCount }}/{{ nodes.length }} 个 Agent 已连接 · 通过 WebSocket 下发配置</div>
      </div>
      <div class="spacer"></div>
      <button class="btn btn-sm" :disabled="loading" @click="load"><RefreshCw :size="14" :class="{ spin: loading }" /> 刷新</button>
      <button class="btn btn-sm" :disabled="syncingAll || !nodes.length" @click="syncAll"><RefreshCw :size="14" :class="{ spin: syncingAll }" /> 全部同步</button>
      <button class="btn btn-primary btn-sm" @click="openCreate"><Plus :size="14" /> 添加节点</button>
    </div>

    <div v-if="error" class="banner banner-danger">{{ error }}</div>

    <div v-if="!nodes.length && !loading" class="card">
      <div class="empty" style="padding: 60px 20px">
        <Server :size="34" />
        <div style="font-size: 14px; color: var(--text); margin-bottom: 6px">尚未配置节点</div>
        <div>添加 Hysteria 2 或 Xray 节点并运行生成的 Docker 命令。</div>
        <button class="btn btn-primary" style="margin-top: 16px" @click="openCreate"><Plus :size="14" /> 添加第一个节点</button>
      </div>
    </div>

    <div class="node-grid">
      <article v-for="n in nodes" :key="n.id" class="card node-card">
        <div class="node-head">
          <div class="node-flag">{{ n.flag || '🌐' }}</div>
          <div style="min-width: 0; flex: 1">
            <div class="node-name truncate">{{ n.name }}</div>
            <div class="node-host truncate">{{ n.domain || n.ip || 'no address' }}:{{ n.port }}</div>
          </div>
          <div class="flex" style="gap: 6px">
            <span class="badge badge-info">{{ coreLabel(n) }}</span>
            <StatusBadge :status="n.status" />
          </div>
        </div>

        <div v-if="n.configError" class="banner banner-danger" style="margin: 0; font-size: 11px">{{ n.configError }}</div>

        <div class="node-metrics">
          <div class="metric"><div class="k">在线</div><div class="v">{{ n.onlineUsers || 0 }}<span v-if="n.maxOnline" class="dim" style="font-size: 10px"> / {{ n.maxOnline }}</span></div></div>
          <div class="metric"><div class="k">流量</div><div class="v" style="font-size: 11px">{{ fmtBytes((n.trafficTx || 0) + (n.trafficRx || 0)) }}</div></div>
          <div class="metric"><div class="k">配置版本</div><div class="v">{{ n.appliedRevision || 0 }}<span class="dim" style="font-size: 10px"> / {{ n.configRevision || 0 }}</span></div></div>
        </div>

        <div v-if="n.metrics" class="node-metrics">
          <div class="metric"><div class="k">CPU</div><div class="v">{{ (n.metrics.cpuPct || 0).toFixed(1) }}%</div><div class="progress" style="margin-top: 5px"><i :style="{ width: Math.min(100, n.metrics.cpuPct || 0) + '%' }"></i></div></div>
          <div class="metric"><div class="k">内存</div><div class="v">{{ memPct(n) }}%</div><div class="progress" style="margin-top: 5px"><i :style="{ width: memPct(n) + '%' }"></i></div></div>
          <div class="metric"><div class="k">磁盘</div><div class="v">{{ diskPct(n) }}%</div><div class="progress" style="margin-top: 5px"><i :style="{ width: diskPct(n) + '%' }"></i></div></div>
        </div>

        <div class="small dim">
          <div class="flex" style="justify-content: space-between; gap: 8px">
            <span>{{ versionLabel(n) }}</span>
            <span>{{ n.lastSeenAt ? fmtRelative(n.lastSeenAt) : '从未连接' }}</span>
          </div>
          <div v-if="n.groups?.length" class="chip-list" style="margin-top: 7px">
            <span v-for="g in n.groups" :key="g.id" class="badge badge-muted">{{ g.name }}</span>
          </div>
        </div>

        <div class="node-actions">
          <button class="btn btn-sm" @click="openInstall(n)"><Terminal :size="13" /> 安装</button>
          <button class="btn btn-sm" :disabled="!n.sshUser || sshBusy === n.id" @click="sshSetup(n)"><Zap :size="13" /> 自动安装</button>
          <router-link class="btn btn-sm" :to="'/logs?nodeId=' + n.id"><ScrollText :size="13" /> 日志</router-link>
          <button class="btn btn-sm" @click="openConfig(n)"><FileCode2 :size="13" /> 配置</button>
          <button class="btn btn-sm" @click="sync(n)"><RefreshCw :size="13" /> 同步</button>
          <button class="btn btn-sm" :disabled="!n.connected" @click="command(n, 'restart')"><RotateCcw :size="13" /> 重启</button>
          <button class="btn btn-sm" :disabled="!n.connected" @click="command(n, 'stop')"><Square :size="13" /> 停止</button>
          <button class="btn btn-sm" @click="openEdit(n)"><Pencil :size="13" /> 编辑</button>
          <button class="icon-btn danger" title="删除节点" @click="askDelete(n)"><Trash2 :size="15" /></button>
        </div>
      </article>
    </div>

    <Modal v-model="modalOpen" :title="editing ? '编辑节点' : '新建节点'" size="lg">
      <div v-if="formError" class="banner banner-danger">{{ formError }}</div>
      <div class="tabs">
        <button class="tab" :class="{ active: tab === 'basic' }" @click="tab = 'basic'">连接</button>
        <button class="tab" :class="{ active: tab === 'tls' }" @click="tab = 'tls'">TLS 与传输</button>
        <button class="tab" :class="{ active: tab === 'ssh' }" @click="tab = 'ssh'">SSH 自动安装</button>
        <button v-if="hasHysteria" class="tab" :class="{ active: tab === 'advanced' }" @click="tab = 'advanced'">ACL 与高级</button>
      </div>

      <div v-show="tab === 'basic'" class="form-grid">
        <div class="field full">
          <label>运行核心</label>
          <div class="chip-list">
            <button type="button" class="chip" :class="{ on: form.cores.includes('hysteria') }" @click="toggleCore('hysteria')">Hysteria 2（QUIC / UDP）</button>
            <button type="button" class="chip" :class="{ on: form.cores.includes('xray') }" @click="toggleCore('xray')">Xray（TCP：VLESS / VMess / Trojan / SS）</button>
          </div>
          <span class="hint">可同时启用两个核心；第一个为核心类型，订阅会自动合并两个核心的入站。</span>
        </div>
        <div class="field"><label>名称</label><input v-model="form.name" type="text" placeholder="Tokyo Edge" /></div>
        <div class="field"><label>国旗 Emoji</label><input v-model="form.flag" type="text" placeholder="🇯🇵" maxlength="8" /></div>
        <div class="field"><label>公网 IP / 主机名</label><input v-model="form.ip" type="text" placeholder="203.0.113.10" /></div>
        <div class="field"><label>域名（ACME）</label><input v-model="form.domain" type="text" placeholder="jp1.example.com" /></div>
        <div class="field"><label>自定义 SNI（仅自签名）</label><input v-model="form.sni" type="text" placeholder="www.bing.com" /></div>
        <div class="field"><label>监听地址</label><input v-model="form.listen" type="text" placeholder=":443（默认）" /></div>
        <div class="field"><label>端口</label><input v-model.number="form.port" type="number" min="1" max="65535" placeholder="443" /></div>
        <div v-if="hasHysteria" class="field"><label>统计端口</label><input v-model.number="form.statsPort" type="number" min="1" max="65535" placeholder="9999" /></div>
        <div class="field"><label>最大在线用户数</label><input v-model.number="form.maxOnline" type="number" min="0" placeholder="0 = 不限制" /></div>
        <div class="field"><label>排序权重</label><input v-model.number="form.ranking" type="number" min="0" step="0.1" placeholder="1.0" />
          <span class="hint">权重越高，订阅中越靠前。</span>
        </div>
        <div class="field full">
          <label class="checkbox"><input v-model="form.active" type="checkbox" /> 启用 — 加入订阅并允许 Agent 连接</label>
        </div>
        <div class="field full">
          <label>服务器组</label>
          <div class="chip-list">
            <button v-for="g in groups" :key="g.id" type="button" class="chip" :class="{ on: form.groupIds.includes(g.id) }" @click="toggleId(form.groupIds, g.id)">{{ g.name }}</button>
            <span v-if="!groups.length" class="dim small">暂无分组 — 该节点将公开。</span>
          </div>
        </div>
      </div>

      <div v-show="tab === 'tls' && (hasHysteria || hasXray)" class="form-grid">
        <div class="field">
          <label>TLS 模式</label>
          <select v-model="form.tlsMode">
            <option value="selfsigned">自签名（无需域名）</option>
            <option value="managed">面板签发（CSR，推荐）</option>
            <option value="acme">节点自签 ACME（TLS-ALPN）</option>
          </select>
          <span class="hint">面板签发使用 DNS-01，证书由面板申请后通过 WebSocket 下发；Xray 的 TLS 入站也使用该证书，REALITY 不需要证书。</span>
        </div>
        <div v-if="hasHysteria && form.tlsMode === 'acme'" class="field"><label>ACME 邮箱</label><input v-model="form.acmeEmail" type="email" placeholder="admin@example.com" /></div>
        <div v-if="form.tlsMode === 'managed'" class="field full">
          <div class="banner banner-info" style="margin: 0 0 8px">
            证书由面板统一申请并下发。保存节点后，Agent 会自动生成密钥并发起 CSR。
          </div>
          <div v-if="editing" class="flex" style="gap: 8px; flex-wrap: wrap; margin-bottom: 8px">
            <button class="btn btn-sm" type="button" @click="refreshCert">刷新证书状态</button>
            <button class="btn btn-sm" type="button" :disabled="!editing.connected" @click="issueCert">立即申请 / 续期</button>
            <button class="btn btn-sm" type="button" @click="openCertUpload">上传证书</button>
          </div>
          <div v-if="certInfo" class="small">
            <div>状态：<strong>{{ certStatusText(certInfo.status) }}</strong><span v-if="certInfo.certificate" class="dim"> · {{ certInfo.certificate.issuer }}</span></div>
            <div v-if="certInfo.certificate" class="dim">到期：{{ fmtDate(certInfo.certificate.notAfter) }} · 指纹：<span class="mono">{{ certInfo.certificate.fingerprint.slice(0, 16) }}…</span></div>
            <div v-if="certInfo.lastError" class="banner banner-danger" style="margin: 8px 0 0">{{ certInfo.lastError }}</div>
            <div v-if="certInfo.manual" class="banner banner-info" style="margin: 8px 0 0">
              <div>请在 DNS 中添加以下 TXT 记录，然后点击“验证并继续”：</div>
              <div v-for="(r, i) in certInfo.manual.records" :key="i" class="mono" style="margin-top: 6px; word-break: break-all">
                {{ r.name }} = {{ r.value }}
              </div>
              <div class="flex" style="gap: 8px; margin-top: 8px">
                <button class="btn btn-sm btn-primary" type="button" @click="confirmCert">验证并继续</button>
                <button class="btn btn-sm" type="button" @click="cancelCert">取消</button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-show="tab === 'tls' && hasHysteria" class="form-grid">
        <div class="field"><label>端口跳跃范围</label><input v-model="form.portRange" type="text" placeholder="20000-50000" />
          <span class="hint">Agent 会自动安装 iptables REDIRECT 规则，留空关闭。</span>
        </div>
        <div class="field"><label>跳跃间隔</label><input v-model="form.hopInterval" type="text" placeholder="30s" /></div>
        <div class="field">
          <label>混淆</label>
          <select v-model="form.obfsType">
            <option value="">已禁用</option>
            <option value="salamander">Salamander</option>
            <option value="gecko">Gecko（Hysteria 2.9+）</option>
          </select>
        </div>
        <div class="field"><label>混淆密码</label><input v-model="form.obfsPassword" type="text" placeholder="启用时必填" /></div>
        <div class="field"><label>上行带宽</label><input v-model="form.bandwidthUp" type="text" placeholder="1 gbps" /></div>
        <div class="field"><label>下行带宽</label><input v-model="form.bandwidthDown" type="text" placeholder="1 gbps" /></div>
        <div class="field full"><label>伪装代理 URL</label><input v-model="form.masqueradeUrl" type="url" placeholder="https://www.bing.com" />
          <span class="hint">访问 Hysteria 端口的 HTTP 请求会反代到该站点。</span>
        </div>
      </div>



      <div v-show="tab === 'tls' && hasXray" class="form-grid">
        <div class="field">
          <label>API 端口</label>
          <input v-model.number="form.xray.apiPort" type="number" min="1" max="65535" placeholder="61000" />
          <span class="hint">仅监听 127.0.0.1，用于流量统计。</span>
        </div>
        <div class="field full">
          <div class="flex" style="justify-content: space-between; align-items: center">
            <label style="margin: 0">Xray 入站（{{ form.xray.inbounds.length }}）</label>
            <button class="btn btn-sm" type="button" @click="addInbound"><Plus :size="13" /> 添加入站</button>
          </div>
          <span class="hint">每个入站可独立选择协议、端口、传输和安全方式。</span>
        </div>
        <div v-for="(ib, idx) in form.xray.inbounds" :key="ib._key" class="card" style="grid-column: 1 / -1; padding: 12px; margin-bottom: 8px">
          <div class="flex" style="justify-content: space-between; align-items: center; margin-bottom: 8px">
            <strong>#{{ idx + 1 }} {{ ib.protocol }} : {{ ib.port }}</strong>
            <button class="icon-btn danger" type="button" :disabled="form.xray.inbounds.length === 1" @click="removeInbound(idx)"><X :size="15" /></button>
          </div>
          <div class="form-grid">
            <div class="field">
              <label>协议</label>
              <select v-model="ib.protocol" @change="normalizeInbound(ib)">
                <option value="vless">VLESS</option>
                <option value="vmess">VMess</option>
                <option value="trojan">Trojan</option>
                <option value="shadowsocks">Shadowsocks</option>
              </select>
            </div>
            <div class="field"><label>入站标签</label><input v-model="ib.tag" type="text" placeholder="vless-in" /></div>
            <div class="field"><label>端口</label><input v-model.number="ib.port" type="number" min="1" max="65535" placeholder="443" /></div>
            <div class="field"><label>监听地址</label><input v-model="ib.listen" type="text" placeholder="0.0.0.0（默认）" /></div>
            <div class="field">
              <label>传输方式</label>
              <select v-model="ib.transport" @change="normalizeInbound(ib)">
                <option value="tcp">TCP（RAW）</option>
                <option value="ws">WebSocket</option>
                <option value="grpc">gRPC</option>
                <option value="xhttp">XHTTP</option>
                <option value="mkcp">mKCP</option>
              </select>
            </div>
            <div class="field">
              <label>安全</label>
              <select v-model="ib.security" :disabled="ib.protocol === 'shadowsocks'">
                <option value="reality" :disabled="!['tcp', 'xhttp', 'grpc'].includes(ib.transport)">REALITY</option>
                <option value="tls">TLS</option>
                <option value="none">无加密</option>
              </select>
            </div>
            <div v-if="ib.protocol === 'vless' && ib.transport === 'tcp' && ib.security !== 'none'" class="field"><label>Flow</label><input v-model="ib.flow" type="text" placeholder="xtls-rprx-vision" /></div>
            <div v-if="ib.protocol === 'shadowsocks'" class="field">
              <label>加密方式</label>
              <select v-model="ib.ssMethod">
                <option value="2022-blake3-aes-128-gcm">2022-blake3-aes-128-gcm</option>
                <option value="2022-blake3-aes-256-gcm">2022-blake3-aes-256-gcm</option>
              </select>
            </div>
            <div class="field"><label>uTLS 指纹</label><input v-model="ib.fingerprint" type="text" placeholder="chrome" /></div>
            <template v-if="ib.security === 'reality'">
              <div class="field"><label>REALITY 目标</label><input v-model="ib.realityDest" type="text" placeholder="www.cloudflare.com:443" /></div>
              <div class="field"><label>服务器名称（逗号分隔）</label><input v-model="ib.sniText" type="text" placeholder="www.cloudflare.com" /></div>
              <div class="field"><label>ALPN（逗号分隔）</label><input v-model="ib.alpnText" type="text" placeholder="h2,http/1.1" /></div>
              <div class="field"><label>SpiderX</label><input v-model="ib.realitySpiderX" type="text" placeholder="/" /></div>
              <div class="field"><label>REALITY 私钥</label><input v-model="ib.realityPrivateKey" type="text" placeholder="留空时自动生成" /></div>
              <div class="field"><label>REALITY 公钥</label><input v-model="ib.realityPublicKey" type="text" placeholder="留空时自动生成" /></div>
              <div class="field full"><label>Short ID（逗号分隔）</label><input v-model="ib.sidText" type="text" placeholder="随机 8 位十六进制" /></div>
            </template>
            <template v-if="ib.security === 'tls' && ib.protocol !== 'shadowsocks'">
              <div class="field"><label>服务器名称（逗号分隔）</label><input v-model="ib.sniText" type="text" placeholder="node.example.com" /></div>
              <div class="field"><label>ALPN（逗号分隔）</label><input v-model="ib.alpnText" type="text" placeholder="h2,http/1.1" /></div>
            </template>
            <template v-if="ib.transport === 'ws'">
              <div class="field"><label>WebSocket 路径</label><input v-model="ib.wsPath" type="text" placeholder="/" /></div>
              <div class="field"><label>WebSocket Host</label><input v-model="ib.wsHost" type="text" placeholder="cdn.example.com" /></div>
            </template>
            <template v-if="ib.transport === 'grpc'">
              <div class="field"><label>gRPC 服务名</label><input v-model="ib.grpcServiceName" type="text" placeholder="grpc" /></div>
            </template>
            <template v-if="ib.transport === 'xhttp'">
              <div class="field"><label>XHTTP 路径</label><input v-model="ib.xhttpPath" type="text" placeholder="/" /></div>
              <div class="field"><label>XHTTP Host</label><input v-model="ib.xhttpHost" type="text" placeholder="cdn.example.com" /></div>
              <div class="field"><label>XHTTP 模式</label><input v-model="ib.xhttpMode" type="text" placeholder="auto" /></div>
            </template>
            <div v-if="ib.transport === 'mkcp'" class="field full"><label>mKCP Seed</label><input v-model="ib.kcpSeed" type="text" placeholder="可留空" /></div>
          </div>
        </div>
        <div class="field full"><span class="hint">REALITY 密钥留空时由面板自动生成；TLS 使用节点 Agent 管理的证书。Shadowsocks 使用 2022 系列以支持按用户限速与流量统计。</span></div>
      </div>
      <div v-show="tab === 'ssh'" class="form-grid">
        <div class="field"><label>SSH 端口</label><input v-model.number="form.sshPort" type="number" min="1" max="65535" placeholder="22" /></div>
        <div class="field"><label>SSH 用户</label><input v-model="form.sshUser" type="text" placeholder="root" /></div>
        <div class="field full">
          <label>SSH 密码</label>
          <input v-model="form.sshPassword" type="password" autocomplete="new-password" :placeholder="editing && editing.sshPasswordSet ? '已配置 — 留空保持不变' : '使用密钥时可留空'" />
        </div>
        <div class="field full">
          <label>SSH 私钥</label>
          <textarea v-model="form.sshKey" rows="6" :placeholder="editing && editing.sshKeySet ? '已配置 — 留空保持不变' : '-----BEGIN OPENSSH PRIVATE KEY-----'"></textarea>
          <span class="hint">面板从服务端发起连接，请使用专用密钥并限制面板出口 IP。</span>
        </div>
        <div class="field full">
          <label>SSH 主机公钥（可选）</label>
          <textarea v-model="form.sshHostKey" rows="3" placeholder="ssh-ed25519 AAAA..."></textarea>
          <span class="hint">留空时首次连接会自动固定主机密钥；已固定后再次安装会严格校验，如需更换请粘贴新公钥。</span>
        </div>
        <div class="field full">
          <label class="checkbox"><input v-model="form.sshInstallDocker" type="checkbox" /> 如果缺少 Docker 则自动安装</label>
        </div>
      </div>
      <div v-show="tab === 'advanced'" class="form-grid">
        <div v-if="hasHysteria" class="field full">
          <label>ACL 规则（每行一条）</label>
          <textarea v-model="form.aclText" rows="7" placeholder="reject(geoip:private)&#10;reject(suffix:doubleclick.net)&#10;direct(all)"></textarea>
        </div>
        <div v-if="hasHysteria" class="field full">
          <label>自定义出站</label>
          <div v-for="(o, i) in form.outbounds" :key="i" class="outbound-row">
            <input v-model="o.name" type="text" placeholder="名称" style="max-width: 130px" />
            <select v-model="o.type" style="max-width: 110px"><option value="socks5">socks5</option><option value="http">http</option><option value="direct">direct</option></select>
            <input v-model="o.addr" type="text" placeholder="1.2.3.4:1080 或 http URL" />
            <input v-model="o.username" type="text" placeholder="用户名" style="max-width: 110px" />
            <input v-model="o.password" type="text" placeholder="密码" style="max-width: 110px" />
            <button class="icon-btn danger" type="button" @click="form.outbounds.splice(i, 1)"><X :size="15" /></button>
          </div>
          <button class="btn btn-sm" type="button" style="align-self: flex-start" @click="form.outbounds.push({ name: '', type: 'socks5', addr: '', username: '', password: '' })"><Plus :size="13" /> 添加出站</button>
          <span class="hint">可在 ACL 规则中通过名称引用这些出站。</span>
        </div>
      </div>

      <template #footer>
        <button class="btn" @click="modalOpen = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : editing ? '保存节点' : '创建节点' }}</button>
      </template>
    </Modal>

    <Modal v-model="installOpen" title="安装节点 Agent" size="lg">
      <div v-if="installError" class="banner banner-danger">{{ installError }}</div>
      <div v-if="install" class="stack">
        <div class="banner banner-info">在 VPS 上执行以下任一命令，Agent 将连接到 <span class="mono">{{ install.publicUrl }}</span>。</div>
        <div>
          <div class="flex" style="margin-bottom: 7px"><strong class="small">Docker（进程模式，推荐）</strong><div class="spacer"></div><button class="btn btn-sm" @click="copy(install.installCommand)"><Copy :size="13" /> 复制</button></div>
          <pre class="code">{{ install.installCommand }}</pre>
        </div>
        <div>
          <div class="flex" style="margin-bottom: 7px"><strong class="small">Docker（管理独立代理容器）</strong><div class="spacer"></div><button class="btn btn-sm" @click="copy(install.dockerCommand)"><Copy :size="13" /> 复制</button></div>
          <pre class="code">{{ install.dockerCommand }}</pre>
        </div>
        <div>
          <div class="flex" style="margin-bottom: 7px"><strong class="small">一键安装脚本</strong><div class="spacer"></div><button class="btn btn-sm" @click="copy(install.installScript)"><Copy :size="13" /> 复制</button></div>
          <pre class="code">{{ install.installScript }}</pre>
        </div>
        <details>
          <summary class="small dim" style="cursor: pointer">docker-compose.yml</summary>
          <pre class="code" style="margin-top: 8px">{{ install.compose }}</pre>
        </details>
        <div class="flex">
          <span class="dim small">令牌: <span class="mono">{{ install.token }}</span></span>
          <div class="spacer"></div>
          <button class="btn btn-danger btn-sm" @click="resetToken">重置令牌</button>
        </div>
      </div>
      <div v-else class="empty">正在加载安装信息…</div>
      <template #footer><button class="btn" @click="installOpen = false">关闭</button></template>
    </Modal>

    <Modal v-model="certUploadOpen" title="上传节点证书" size="lg">
      <div class="stack">
        <div class="banner banner-info">上传的证书与私钥会加密保存在面板，并通过 WebSocket 下发到节点。证书必须匹配节点域名。</div>
        <div class="field full"><label>证书链 PEM（含中间证书）</label><textarea v-model="certPEMText" rows="7" placeholder="-----BEGIN CERTIFICATE-----"></textarea></div>
        <div class="field full"><label>私钥 PEM</label><textarea v-model="certKeyText" rows="7" placeholder="-----BEGIN PRIVATE KEY-----"></textarea></div>
        <div v-if="certUploadError" class="banner banner-danger">{{ certUploadError }}</div>
      </div>
      <template #footer>
        <button class="btn" @click="certUploadOpen = false">取消</button>
        <button class="btn btn-primary" :disabled="certUploading" @click="uploadCert">{{ certUploading ? '上传中…' : '上传并下发' }}</button>
      </template>
    </Modal>

    <Modal v-model="sshOpen" title="SSH 自动安装" size="lg">
      <div v-if="sshResult" class="stack">
        <div class="banner" :class="sshResult.ok ? 'banner-info' : 'banner-danger'">
          {{ sshResult.ok ? '安装脚本执行成功。' : (sshResult.error || '安装脚本执行失败。') }}
        </div>
        <pre class="code">{{ sshResult.output || '无输出。' }}</pre>
        <details v-if="sshResult.command"><summary class="dim small" style="cursor:pointer">安装命令</summary><pre class="code">{{ sshResult.command }}</pre></details>
      </div>
      <div v-else class="empty">正在通过 SSH 连接…</div>
      <template #footer><button class="btn" @click="sshOpen = false">关闭</button></template>
    </Modal>

    <Modal v-model="configOpen" :title="'生成的节点配置'" size="lg">
      <div v-if="configInfo">
        <div v-if="configCores.length > 1" class="tabs" style="margin-bottom: 9px">
          <button v-for="c in configCores" :key="c" class="tab" :class="{ active: configCore === c }" @click="configCore = c">{{ c === 'xray' ? 'Xray JSON' : 'Hysteria YAML' }}</button>
        </div>
        <div class="flex small dim" style="margin-bottom: 9px">
          <span>配置版本 {{ configInfo.revision }}</span><span>·</span><span>{{ configInfo.hash ? configInfo.hash.slice(0, 16) + '…' : '' }}</span>
          <div class="spacer"></div>
          <button class="btn btn-sm" @click="copy(configText)"><Copy :size="13" /> 复制配置</button>
        </div>
        <pre class="code">{{ configText || '# 尚未生成配置' }}</pre>
      </div>
      <template #footer><button class="btn" @click="configOpen = false">关闭</button></template>
    </Modal>

    <Modal v-model="deleteOpen" title="删除节点" size="sm">
      <p>删除 <strong>{{ deleteTarget?.name }}</strong>? Agent 将被断开，面板中的配置数据也会删除。</p>
      <template #footer>
        <button class="btn" @click="deleteOpen = false">取消</button>
        <button class="btn btn-danger" @click="confirmDelete">删除节点</button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import {
  Copy, FileCode2, Pencil, Plus, RefreshCw, RotateCcw, ScrollText, Server, Square, Terminal, Trash2, X, Zap,
} from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../store'
import Modal from '../components/Modal.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { copyText, fmtBytes, fmtDate, fmtRelative } from '../format'

const nodes = ref([])
const groups = ref([])
const loading = ref(false)
const syncingAll = ref(false)
const error = ref('')

const modalOpen = ref(false)
const tab = ref('basic')
const editing = ref(null)
const saving = ref(false)
const formError = ref('')
let inboundKey = 1
const form = reactive(emptyForm())

const sshBusy = ref(0)
const sshOpen = ref(false)
const sshResult = ref(null)
const installOpen = ref(false)
const install = ref(null)
const installError = ref('')
const configOpen = ref(false)
const configInfo = ref(null)
const configCore = ref('')
const deleteOpen = ref(false)
const deleteTarget = ref(null)
const certInfo = ref(null)
const certUploadOpen = ref(false)
const certPEMText = ref('')
const certKeyText = ref('')
const certUploadError = ref('')
const certUploading = ref(false)
let timer = null

const onlineCount = computed(() => nodes.value.filter((n) => n.connected).length)
const hasHysteria = computed(() => form.cores.includes('hysteria'))
const hasXray = computed(() => form.cores.includes('xray'))
const configCores = computed(() => {
  const info = configInfo.value
  if (!info) return []
  if (Array.isArray(info.cores) && info.cores.length) return info.cores
  return Object.keys(info.configs || {})
})
const configText = computed(() => {
  const info = configInfo.value
  if (!info) return ''
  if (configCore.value && info.configs?.[configCore.value]) return info.configs[configCore.value]
  return info.yaml || ''
})

function emptyInbound() {
  return {
    _key: inboundKey++,
    protocol: 'vless', tag: 'vless-in', listen: '', port: 443,
    transport: 'tcp', security: 'reality', flow: 'xtls-rprx-vision', fingerprint: 'chrome',
    alpn: [], realityDest: 'www.cloudflare.com:443', realitySni: [], realityPrivateKey: '',
    realityPublicKey: '', realityShortIds: [], realitySpiderX: '',
    wsPath: '/', wsHost: '', grpcServiceName: 'grpc',
    xhttpPath: '/', xhttpHost: '', xhttpMode: 'auto', kcpSeed: '',
    ssMethod: '2022-blake3-aes-128-gcm',
    sniText: '', alpnText: 'h2,http/1.1', sidText: '',
  }
}
function inboundsFromNode(n) {
  const x = n.xray || {}
  if (Array.isArray(x.inbounds) && x.inbounds.length) {
    return x.inbounds.map((ib) => ({
      ...emptyInbound(), ...ib,
      port: ib.port || n.port || 443,
      sniText: (ib.realitySni || []).join(','),
      alpnText: (ib.alpn || []).join(','),
      sidText: (ib.realityShortIds || []).join(','),
    }))
  }
  return [{
    ...emptyInbound(),
    port: n.port || 443,
    transport: x.transport || 'tcp', security: x.security || 'reality',
    flow: x.flow || '', fingerprint: x.fingerprint || 'chrome',
    realityDest: x.realityDest || 'www.cloudflare.com:443',
    realitySni: x.realitySni || [], realityPrivateKey: x.realityPrivateKey || '',
    realityPublicKey: x.realityPublicKey || '', realityShortIds: x.realityShortIds || [],
    realitySpiderX: x.realitySpiderX || '', wsPath: x.wsPath || '/', wsHost: x.wsHost || '',
    grpcServiceName: x.grpcServiceName || 'grpc',
    sniText: (x.realitySni || []).join(','), alpnText: (x.alpn || []).join(','),
    sidText: (x.realityShortIds || []).join(','),
  }]
}

function emptyForm() {
  return {
    name: '', flag: '', ip: '', domain: '', sni: '', listen: '', port: 443, statsPort: 9999,
    tlsMode: 'selfsigned', acmeEmail: '', portRange: '', hopInterval: '30s', obfsType: '',
    obfsPassword: '', bandwidthUp: '', bandwidthDown: '', masqueradeUrl: '',
    type: 'hysteria', cores: ['hysteria'], maxOnline: 0, ranking: 1, active: true, groupIds: [], aclText: 'direct(all)',
    outbounds: [], sshPort: 22, sshUser: 'root', sshPassword: '', sshKey: '', sshHostKey: '', sshInstallDocker: false,
    xray: { apiPort: 61000, inbounds: [emptyInbound()] },
  }
}
function resetForm() { Object.assign(form, emptyForm()) }
function splitList(v) { return String(v || '').split(',').map((s) => s.trim()).filter(Boolean) }
function toggleCore(core) {
  const i = form.cores.indexOf(core)
  if (i >= 0) {
    if (form.cores.length > 1) form.cores.splice(i, 1)
  } else {
    form.cores.push(core)
  }
  form.type = form.cores[0]
  if (form.type !== 'xray' && tab.value === 'advanced' && !form.cores.includes('hysteria')) tab.value = 'tls'
}
function addInbound() {
  const ib = emptyInbound()
  ib.port = (Number(form.xray.inbounds[form.xray.inbounds.length - 1]?.port) || Number(form.port) || 443) + 1
  ib.tag = `${ib.protocol}-in-${form.xray.inbounds.length + 1}`
  form.xray.inbounds.push(ib)
}
function removeInbound(idx) {
  if (form.xray.inbounds.length > 1) form.xray.inbounds.splice(idx, 1)
}
function normalizeInbound(ib) {
  if (ib.protocol === 'shadowsocks') {
    ib.security = 'none'
    ib.flow = ''
    return
  }
  if (ib.security === 'reality' && !['tcp', 'xhttp', 'grpc'].includes(ib.transport)) {
    ib.security = 'tls'
  }
  if (ib.protocol !== 'vless') ib.flow = ''
  if (ib.security === 'none') ib.flow = ''
}
function coreLabel(n) {
  const cores = (n.cores && n.cores.length ? n.cores : [n.type || 'hysteria'])
  return cores.map((c) => (c === 'xray' ? 'Xray' : 'Hy2')).join(' + ')
}
function versionLabel(n) {
  const cores = (n.cores && n.cores.length ? n.cores : [n.type || 'hysteria'])
  const parts = []
  if (cores.includes('hysteria')) parts.push('Hysteria ' + (n.hy2Version || 'unknown'))
  if (cores.includes('xray')) parts.push('Xray ' + (n.xrayVersion || 'unknown'))
  return parts.join(' · ') || 'unknown'
}
watch(() => form.cores.slice(), () => {
  if (!form.cores.includes('hysteria') && tab.value === 'advanced') tab.value = 'tls'
})
function toggleId(list, id) {
  const i = list.indexOf(id)
  if (i >= 0) list.splice(i, 1)
  else list.push(id)
}
function memPct(n) {
  const m = n.metrics
  if (!m || !m.memTotal) return 0
  return Math.min(100, Math.round((m.memUsed / m.memTotal) * 100))
}
function diskPct(n) {
  const m = n.metrics
  if (!m || !m.diskTotal) return 0
  return Math.min(100, Math.round((m.diskUsed / m.diskTotal) * 100))
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await api.nodes()
    nodes.value = res.items
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
async function loadGroups() {
  try {
    groups.value = (await api.groups()).items
  } catch (e) {
    error.value = e.message
  }
}

function openCreate() {
  editing.value = null
  resetForm()
  tab.value = 'basic'
  formError.value = ''
  modalOpen.value = true
  certInfo.value = null
}
function openEdit(n) {
  editing.value = n
  Object.assign(form, {
    type: n.type || 'hysteria',
    cores: (n.cores && n.cores.length ? [...n.cores] : [n.type || 'hysteria']),
    name: n.name, flag: n.flag || '', ip: n.ip || '', domain: n.domain || '', sni: n.sni || '',
    listen: n.listen || '', port: n.port || 443, statsPort: n.statsPort || 9999,
    tlsMode: n.tlsMode || 'selfsigned', acmeEmail: n.acmeEmail || '', portRange: n.portRange || '',
    hopInterval: n.hopInterval || '', obfsType: n.obfsType || '', obfsPassword: n.obfsPassword || '',
    bandwidthUp: n.bandwidthUp || '', bandwidthDown: n.bandwidthDown || '', masqueradeUrl: n.masqueradeUrl || '',
    maxOnline: n.maxOnline || 0, ranking: n.ranking || 1, active: n.active,
    sshPort: n.sshPort || 22, sshUser: n.sshUser || '', sshPassword: '', sshKey: '',
    sshHostKey: n.sshHostKey || '', sshInstallDocker: !!n.sshInstallDocker,
    groupIds: (n.groups || []).map((g) => g.id),
    aclText: (n.acl || []).join('\n'),
    outbounds: (n.outbounds || []).map((o) => ({ ...o })),
    xray: { apiPort: n.xray?.apiPort || 61000, inbounds: inboundsFromNode(n) },
  })
  tab.value = 'basic'
  formError.value = ''
  modalOpen.value = true
  certInfo.value = null
  if (n.tlsMode === 'managed') refreshCert()
}
function certStatusText(status) {
  return ({
    none: '未申请', pending: '待申请', requesting: '申请中', awaiting_txt: '等待 DNS TXT',
    validating: '验证中', issued: '已签发', pushing: '下发中', active: '已生效', error: '失败',
  })[status] || status || '未知'
}
async function refreshCert() {
  if (!editing.value) return
  try {
    certInfo.value = await api.nodeCert(editing.value.id)
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function issueCert() {
  if (!editing.value) return
  try {
    await api.requestNodeCert(editing.value.id)
    notify('已通知节点申请证书')
    setTimeout(refreshCert, 800)
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function confirmCert() {
  if (!editing.value) return
  try {
    await api.confirmNodeCert(editing.value.id)
    notify('已提交 DNS 验证，证书签发中')
    certInfo.value = await api.nodeCert(editing.value.id)
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function cancelCert() {
  if (!editing.value) return
  try {
    await api.cancelNodeCert(editing.value.id)
    notify('已取消证书申请')
    await refreshCert()
  } catch (e) {
    notify(e.message, 'error')
  }
}
function openCertUpload() {
  certPEMText.value = ''
  certKeyText.value = ''
  certUploadError.value = ''
  certUploadOpen.value = true
}
async function uploadCert() {
  if (!editing.value) return
  certUploadError.value = ''
  certUploading.value = true
  try {
    await api.uploadNodeCert(editing.value.id, { certPEM: certPEMText.value, keyPEM: certKeyText.value })
    notify('证书已上传并下发')
    certUploadOpen.value = false
    await refreshCert()
  } catch (e) {
    certUploadError.value = e.message
  } finally {
    certUploading.value = false
  }
}

async function save() {
  formError.value = ''
  saving.value = true
  try {
    const payload = {
      type: form.cores[0],
      cores: form.cores.slice(),
      name: form.name, flag: form.flag, ip: form.ip, domain: form.domain, sni: form.sni,
      listen: form.listen, port: Number(form.port) || 443, statsPort: Number(form.statsPort) || 9999,
      tlsMode: form.tlsMode, acmeEmail: form.acmeEmail, portRange: form.portRange,
      hopInterval: form.hopInterval, obfsType: form.obfsType, obfsPassword: form.obfsPassword,
      bandwidthUp: form.bandwidthUp, bandwidthDown: form.bandwidthDown, masqueradeUrl: form.masqueradeUrl,
      maxOnline: Number(form.maxOnline) || 0, ranking: Number(form.ranking) || 1, active: form.active,
      sshPort: Number(form.sshPort) || 22, sshUser: form.sshUser,
      sshPassword: form.sshPassword, sshKey: form.sshKey, sshHostKey: form.sshHostKey,
      sshInstallDocker: form.sshInstallDocker,
      groupIds: form.groupIds,
      acl: form.aclText.split('\n').map((s) => s.trim()).filter(Boolean),
      outbounds: form.outbounds.filter((o) => o.name),
      xray: {
        apiPort: Number(form.xray.apiPort) || 61000,
        inbounds: form.xray.inbounds.map((ib) => {
          const { _key, sniText, alpnText, sidText, ...rest } = ib
          return {
            ...rest,
            port: Number(rest.port) || 443,
            realitySni: splitList(sniText),
            alpn: splitList(alpnText),
            realityShortIds: splitList(sidText),
          }
        }),
      },
    }
    let res
    if (editing.value) {
      res = await api.updateNode(editing.value.id, payload)
      notify('节点已更新')
      modalOpen.value = false
      await load()
    } else {
      res = await api.createNode(payload)
      notify('节点已创建 — 请安装 Agent 以连接')
      modalOpen.value = false
      await load()
      const created = res.node || res
      if (created?.id) openInstall(created)
    }
  } catch (e) {
    formError.value = e.message
  } finally {
    saving.value = false
  }
}

async function sshSetup(n) {
  if (!confirm(`Run the node installer over SSH on ${n.domain || n.ip}?`)) return
  sshBusy.value = n.id
  sshResult.value = null
  sshOpen.value = true
  try {
    sshResult.value = await api.nodeSSHInstall(n.id)
    notify('SSH 自动安装完成')
  } catch (e) {
    sshResult.value = { ok: false, error: e.message, output: e.data?.output || '' }
    notify(e.message, 'error')
  } finally {
    sshBusy.value = 0
  }
}
async function syncAll() {
  syncingAll.value = true
  try {
    const res = await api.syncAll()
    notify(`已同步 ${res.nodes} 个节点，推送 ${res.pushed} 份配置`)
    load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    syncingAll.value = false
  }
}
async function sync(n) {
  try {
    const res = await api.syncNode(n.id)
    notify(res.pushed ? `配置版本 ${res.revision} 已推送到 Agent` : `配置版本 ${res.revision} 已保存（Agent 离线）`)
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function command(n, action) {
  try {
    await api.nodeCommand(n.id, action)
    notify(`Command "${action}" sent`)
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function openInstall(n) {
  install.value = null
  installError.value = ''
  installOpen.value = true
  try {
    const data = await api.nodeInstall(n.id)
    install.value = { ...data, nodeId: n.id, nodeName: n.name }
  } catch (e) {
    installError.value = e.message
  }
}
async function resetToken() {
  if (!confirm('Resetting the token disconnects the current agent until it is reconfigured. Continue?')) return
  try {
    const res = await api.resetNodeToken(install.value.nodeId)
    install.value = { ...install.value, token: res.token, installCommand: res.installCommand, dockerCommand: res.dockerCommand }
    notify('New token generated')
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function openConfig(n) {
  configInfo.value = null
  configCore.value = ''
  configOpen.value = true
  try {
    configInfo.value = await api.nodeConfig(n.id)
    const cores = Array.isArray(configInfo.value.cores) && configInfo.value.cores.length
      ? configInfo.value.cores : Object.keys(configInfo.value.configs || {})
    configCore.value = cores[0] || ''
  } catch (e) {
    notify(e.message, 'error')
  }
}
function askDelete(n) {
  deleteTarget.value = n
  deleteOpen.value = true
}
async function confirmDelete() {
  try {
    await api.deleteNode(deleteTarget.value.id)
    deleteOpen.value = false
    notify('节点已删除')
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function copy(text) {
  await copyText(text)
  notify('已复制到剪贴板')
}

onMounted(() => {
  loadGroups()
  load()
  timer = setInterval(load, 20000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<style scoped>
.stack { display: flex; flex-direction: column; gap: 16px; }
.outbound-row { display: flex; gap: 7px; align-items: center; margin-bottom: 7px; flex-wrap: wrap; }
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
