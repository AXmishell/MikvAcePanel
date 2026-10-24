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
            <span class="badge" :class="runtimeMode(n) === 'docker' ? 'badge-info' : 'badge-muted'">{{ runtimeLabel(runtimeMode(n)) }}</span>
            <span v-if="n.runtime && n.runtime !== runtimeMode(n)" class="badge badge-warn" :title="`Agent 实际运行方式为 ${n.runtime}，请重新安装以应用面板配置`">实际 {{ n.runtime }}</span>
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
          <router-link class="btn btn-sm" :to="adminPath('logs') + '?nodeId=' + n.id"><ScrollText :size="13" /> 日志</router-link>
          <button class="btn btn-sm" @click="openConfig(n)"><FileCode2 :size="13" /> 配置</button>
          <button class="btn btn-sm" @click="sync(n)"><RefreshCw :size="13" /> 同步</button>
          <button class="btn btn-sm" :disabled="!n.connected" @click="command(n, 'restart')"><RotateCcw :size="13" /> 重启</button>
          <button class="btn btn-sm" :disabled="!n.connected" @click="command(n, 'stop')"><Square :size="13" /> 停止</button>
          <button class="btn btn-sm" @click="openEdit(n)"><Pencil :size="13" /> 编辑</button>
          <button v-tip="'删除节点'" class="icon-btn danger" @click="askDelete(n)"><Trash2 :size="15" /></button>
        </div>
      </article>
    </div>

    <Modal v-model="modalOpen" :title="editing ? '编辑节点' : '新建节点'" size="lg">
      <div v-if="formError" class="banner banner-danger">{{ formError }}</div>
      <div class="tabs">
        <button class="tab" :class="{ active: tab === 'basic' }" @click="tab = 'basic'">连接</button>
        <button class="tab" :class="{ active: tab === 'tls' }" @click="tab = 'tls'">TLS 与传输</button>
        <button v-if="hasHysteria" class="tab" :class="{ active: tab === 'hy2' }" @click="tab = 'hy2'">Hysteria 2 设置</button>
        <button v-if="hasHysteria" class="tab" :class="{ active: tab === 'advanced' }" @click="tab = 'advanced'">ACL 与高级</button>
        <button class="tab" :class="{ active: tab === 'ssh' }" @click="tab = 'ssh'">SSH 自动安装</button>
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
        <div v-if="hasXray" class="field full">
          <label>Xray 协议</label>
          <div class="chip-list">
            <button v-for="p in XRAY_PROTOCOLS" :key="p.id" type="button" class="chip" :class="{ on: protocolEnabled(p.id) }" @click="toggleProtocol(p.id)">{{ p.label }}</button>
          </div>
          <span class="hint">勾选即启用；取消只禁用，端口、REALITY 密钥和传输配置会保留。</span>
        </div>
        <div v-if="hasXray" class="field full">
          <label>快速预设</label>
          <div class="flex" style="gap: 8px; flex-wrap: wrap">
            <button v-for="p in XRAY_PRESETS" :key="p.id" type="button" class="btn btn-sm" @click="applyPreset(p.protocols)">{{ p.label }}</button>
          </div>
          <span class="hint">预设会自动创建缺失的入站并启用对应协议，仍可在“TLS 与传输”中调整参数。</span>
        </div>
        <div v-if="hasXray && enabledInboundCount() === 0" class="field full">
          <div class="banner banner-danger" style="margin: 0">至少启用一个 Xray 入站，否则无法保存。</div>
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
        <div class="field">
          <label>Agent 运行方式</label>
          <Select v-model="form.runtimeMode" :options="RUNTIME_OPTIONS" />
          <span class="hint">Docker 模式需要挂载 <span class="mono">/var/run/docker.sock</span>，修改后需重新安装 Agent 才会生效。</span>
        </div>
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
        <div class="field full"><strong>证书与 TLS</strong></div>
        <div class="field">
          <label>TLS 模式</label>
          <Select v-model="form.tlsMode" :options="TLS_MODE_OPTIONS" />
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

      <div v-show="tab === 'tls' && hasXray" class="form-grid" :class="{ 'section-gap': hasHysteria }">
        <div class="field full"><strong>Xray 入站</strong></div>
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
        <div v-for="(ib, idx) in form.xray.inbounds" :key="ib._key" class="card" :style="'grid-column: 1 / -1; padding: 12px; margin-bottom: 8px;' + (ib.enabled === false ? ' opacity: .55' : '')">
          <div class="flex" style="justify-content: space-between; align-items: center; margin-bottom: 8px">
            <div class="flex" style="gap: 10px; align-items: center">
              <label class="checkbox" style="margin: 0"><input v-model="ib.enabled" type="checkbox" /> 启用</label>
              <strong>#{{ idx + 1 }} {{ ib.protocol }} : {{ ib.port }}</strong>
            </div>
            <button class="icon-btn danger" type="button" :disabled="form.xray.inbounds.length === 1" @click="removeInbound(idx)"><X :size="15" /></button>
          </div>
          <div class="form-grid">
            <div class="field">
              <label>协议</label>
              <Select v-model="ib.protocol" :options="PROTOCOL_OPTIONS" @change="normalizeInbound(ib)" />
            </div>
            <div class="field"><label>入站标签</label><input v-model="ib.tag" type="text" placeholder="vless-in" /></div>
            <div class="field"><label>端口</label><input v-model.number="ib.port" type="number" min="1" max="65535" placeholder="443" /></div>
            <div class="field"><label>监听地址</label><input v-model="ib.listen" type="text" placeholder="0.0.0.0（默认）" /></div>
            <div class="field">
              <label>传输方式</label>
              <Select v-model="ib.transport" :options="TRANSPORT_OPTIONS" @change="normalizeInbound(ib)" />
            </div>
            <div class="field">
              <label>安全</label>
              <Select v-model="ib.security" :options="securityOptions(ib)" :disabled="ib.protocol === 'shadowsocks'" />
            </div>
            <div v-if="ib.protocol === 'vless' && ib.transport === 'tcp' && ib.security !== 'none'" class="field"><label>Flow</label><input v-model="ib.flow" type="text" placeholder="xtls-rprx-vision" /></div>
            <div v-if="ib.protocol === 'shadowsocks'" class="field">
              <label>加密方式</label>
              <Select v-model="ib.ssMethod" :options="SS_METHOD_OPTIONS" />
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
            <Select v-model="o.type" :options="OUTBOUND_TYPE_OPTIONS" size="sm" style="max-width: 110px" />
            <input v-model="o.addr" type="text" placeholder="1.2.3.4:1080 或 http URL" />
            <input v-model="o.username" type="text" placeholder="用户名" style="max-width: 110px" />
            <input v-model="o.password" type="text" placeholder="密码" style="max-width: 110px" />
            <button class="icon-btn danger" type="button" @click="form.outbounds.splice(i, 1)"><X :size="15" /></button>
          </div>
          <button class="btn btn-sm" type="button" style="align-self: flex-start" @click="form.outbounds.push({ name: '', type: 'socks5', addr: '', username: '', password: '' })"><Plus :size="13" /> 添加出站</button>
          <span class="hint">可在 ACL 规则中通过名称引用这些出站。</span>
        </div>
      </div>

      <div v-show="tab === 'hy2'" class="form-grid">
        <div class="field full">
          <label>配置模板</label>
          <div class="flex" style="gap: 8px; flex-wrap: wrap">
            <button v-for="t in HY2_TEMPLATE_PRESETS" :key="t.id" type="button" class="btn btn-sm" @click="applyHy2Template(t.id)">{{ t.label }}</button>
          </div>
          <span class="hint">
            模板只覆盖 QUIC、拥塞控制、嗅探、伪装、SNI 校验和基础开关；ECH、mTLS、ACL、Realms、mimic 与额外 YAML 保持不变。应用后仍需点击“保存节点”才会下发。
          </span>
        </div>
        <div class="field full hy2-subtabs">
          <button v-for="sec in HY2_SECTIONS" :key="sec.id" type="button" class="hy2-subtab" :class="{ active: hy2Section === sec.id }" @click="hy2Section = sec.id">{{ sec.label }}</button>
        </div>

        <template v-if="hy2Section === 'basic'">
        <div class="field full"><strong>运行模式与监听</strong></div>
        <div class="field full">
          <label>Realms 牵线 URI</label>
          <input v-model="form.hy2.realm.uri" type="text" placeholder="realm://public@realm.hy2.io/my-server-8f3a" />
          <span class="hint">需要 Hysteria 2.9+。启用后 listen 由面板改写为 realm URI，并与端口跳跃互斥；分享链接无法表达 Realms，客户端需手动配置。</span>
        </div>
        <div class="field"><label>流量统计监听主机</label><input v-model="form.hy2.trafficListen" type="text" placeholder="127.0.0.1" />
          <span class="hint">只改主机，端口仍由“统计端口”控制；默认仅本机。</span>
        </div>

        <div class="field full"><strong>QUIC 参数</strong></div>
        <div class="field"><label>初始流接收窗口</label><input v-model.number="form.hy2.quic.initStreamReceiveWindow" type="number" min="0" placeholder="8388608" /></div>
        <div class="field"><label>最大流接收窗口</label><input v-model.number="form.hy2.quic.maxStreamReceiveWindow" type="number" min="0" placeholder="8388608" /></div>
        <div class="field"><label>初始连接接收窗口</label><input v-model.number="form.hy2.quic.initConnReceiveWindow" type="number" min="0" placeholder="20971520" /></div>
        <div class="field"><label>最大连接接收窗口</label><input v-model.number="form.hy2.quic.maxConnReceiveWindow" type="number" min="0" placeholder="20971520" /></div>
        <div class="field"><label>最大并发入站流</label><input v-model.number="form.hy2.quic.maxIncomingStreams" type="number" min="0" placeholder="1024" /></div>
        <div class="field"><label>最大空闲超时</label><input v-model="form.hy2.quic.maxIdleTimeout" type="text" placeholder="30s" /></div>
        <div class="field full">
          <label class="checkbox"><input v-model="form.hy2.quic.disablePathMTUDiscovery" type="checkbox" /> 禁用路径 MTU 探测</label>
          <label class="checkbox"><input v-model="form.hy2.quic.disableStatelessReset" type="checkbox" /> 禁用 stateless reset</label>
          <span class="hint">默认流/连接窗口为 8MB / 20MB，建议保持 2:5 比例；不清楚时留空。</span>
        </div>

        </template>

        <template v-if="hy2Section === 'transport'">
        <div class="field full"><strong>端口跳跃</strong></div>
        <div class="field full"><span class="hint">端口跳跃范围与内置端口范围互斥；同时填写时以内置端口范围为准。</span></div>
        <div class="field"><label>端口跳跃范围</label><input v-model="form.portRange" type="text" placeholder="20000-50000" />
          <span class="hint">Agent 会自动安装 iptables REDIRECT 规则，留空关闭。</span>
        </div>
        <div class="field"><label>内置端口范围（listen）</label><input v-model="form.hy2.listenPortRange" type="text" placeholder="20000-50000" />
          <span class="hint">Hysteria 2.8+ 自行监听并维护防火墙规则。</span>
        </div>
        <div class="field"><label>跳跃间隔</label><input v-model="form.hopInterval" type="text" placeholder="30s" />
          <span class="hint">仅端口跳跃模式生效，支持 <span class="mono">30s</span>、<span class="mono">1m</span> 等写法。</span>
        </div>

        <div class="field full"><strong>混淆与带宽</strong></div>
        <div class="field">
          <label>混淆</label>
          <Select v-model="form.obfsType" :options="OBFS_OPTIONS" />
        </div>
        <div class="field"><label>混淆密码</label><input v-model="form.obfsPassword" type="text" placeholder="启用时必填" /></div>
        <div class="field"><label>上行带宽</label><input v-model="form.bandwidthUp" type="text" placeholder="1 gbps" /></div>
        <div class="field"><label>下行带宽</label><input v-model="form.bandwidthDown" type="text" placeholder="1 gbps" />
          <span class="hint">用于 Brutal 拥塞控制；选择 BBR 时可留空。</span>
        </div>
        <div class="field"><label>Gecko 最小分片</label><input v-model.number="form.hy2.geckoMinPacketSize" type="number" min="0" max="2048" placeholder="512" /></div>
        <div class="field"><label>Gecko 最大分片</label><input v-model.number="form.hy2.geckoMaxPacketSize" type="number" min="0" max="2048" placeholder="1200" />
          <span class="hint">仅在选择 Gecko 混淆时生效，需 Hysteria 2.9.2+。</span>
        </div>
        </template>

        <template v-if="hy2Section === 'performance'">
        <div class="field full"><strong>拥塞控制与限速</strong></div>
        <div class="field"><label>拥塞控制器</label><Select v-model="form.hy2.congestion.type" :options="CONGESTION_OPTIONS" /></div>
        <div class="field"><label>BBR 预设</label><Select v-model="form.hy2.congestion.bbrProfile" :options="BBR_PROFILE_OPTIONS" :disabled="form.hy2.congestion.type !== 'bbr'" /></div>
        <div class="field"><label>UDP 空闲超时</label><input v-model="form.hy2.udpIdleTimeout" type="text" placeholder="60s" /></div>
        <div class="field full">
          <label class="checkbox"><input v-model="form.hy2.ignoreClientBandwidth" type="checkbox" /> 忽略客户端带宽设置（强制使用非 Brutal 控制器）</label>
          <label class="checkbox"><input v-model="form.hy2.disableLossCompensation" type="checkbox" /> 关闭 Brutal 丢包补偿（2.10+）</label>
          <label class="checkbox"><input v-model="form.hy2.disableUDP" type="checkbox" /> 禁用 UDP 转发（只允许 TCP）</label>
          <label class="checkbox"><input v-model="form.hy2.speedTest" type="checkbox" /> 允许客户端进行速度测试</label>
        </div>

        <div class="field full"><strong>直连出站</strong></div>
        <div class="field"><label>IP 栈模式</label><Select v-model="form.hy2.direct.mode" :options="DIRECT_MODE_OPTIONS" /></div>
        <div class="field"><label>绑定 IPv4</label><input v-model="form.hy2.direct.bindIPv4" type="text" placeholder="2.4.6.8" /></div>
        <div class="field"><label>绑定 IPv6</label><input v-model="form.hy2.direct.bindIPv6" type="text" placeholder="::" /></div>
        <div class="field"><label>绑定网卡</label><input v-model="form.hy2.direct.bindDevice" type="text" placeholder="eth0" />
          <span class="hint">绑定网卡与绑定 IP 互斥。</span>
        </div>
        <div class="field"><label class="checkbox"><input v-model="form.hy2.direct.fastOpen" type="checkbox" /> 启用 TCP Fast Open</label></div>

        </template>

        <template v-if="hy2Section === 'masquerade'">
        <div class="field full"><strong>伪装（Masquerade）</strong></div>
        <div class="field"><label>伪装类型</label><Select v-model="form.hy2.masquerade.type" :options="MASQ_TYPE_OPTIONS" /></div>
        <div class="field"><label>HTTP 伪装监听</label><input v-model="form.hy2.masquerade.listenHttp" type="text" placeholder=":80（可选）" /></div>
        <div class="field"><label>HTTPS 伪装监听</label><input v-model="form.hy2.masquerade.listenHttps" type="text" placeholder=":443（可选）" /></div>
        <div class="field"><label class="checkbox"><input v-model="form.hy2.masquerade.forceHttps" type="checkbox" /> 强制跳转 HTTPS</label></div>
        <template v-if="form.hy2.masquerade.type === 'proxy'">
          <div class="field full"><label>反代目标 URL</label><input v-model="form.hy2.masquerade.proxyUrl" type="url" placeholder="https://www.bing.com" /></div>
          <div class="field"><label class="checkbox"><input v-model="form.hy2.masquerade.rewriteHost" type="checkbox" /> 重写 Host</label></div>
          <div class="field"><label class="checkbox"><input v-model="form.hy2.masquerade.insecure" type="checkbox" /> 跳过目标站 TLS 校验</label></div>
          <div class="field"><label class="checkbox"><input v-model="form.hy2.masquerade.xForwarded" type="checkbox" /> 附加 X-Forwarded-* 头</label></div>
        </template>
        <template v-if="form.hy2.masquerade.type === 'file'">
          <div class="field full"><label>静态目录</label><input v-model="form.hy2.masquerade.fileDir" type="text" placeholder="/www/masq" /></div>
        </template>
        <template v-if="form.hy2.masquerade.type === 'string'">
          <div class="field full"><label>返回内容</label><textarea v-model="form.hy2.masquerade.content" rows="3" placeholder="hello"></textarea></div>
          <div class="field full"><label>响应头（每行 key: value）</label><textarea v-model="form.hy2.masquerade.headersText" rows="3" placeholder="content-type: text/plain"></textarea></div>
          <div class="field"><label>状态码</label><input v-model.number="form.hy2.masquerade.statusCode" type="number" min="100" max="599" placeholder="200" /></div>
        </template>

        </template>

        <template v-if="hy2Section === 'network'">
        <div class="field full"><strong>DNS 与协议嗅探</strong></div>
        <div class="field"><label>解析器类型</label><Select v-model="form.hy2.resolver.type" :options="RESOLVER_TYPE_OPTIONS" /></div>
        <div class="field"><label>解析器地址</label><input v-model="form.hy2.resolver.addr" type="text" placeholder="8.8.8.8:53" /></div>
        <div class="field"><label>查询超时</label><input v-model="form.hy2.resolver.timeout" type="text" placeholder="4s" /></div>
        <div class="field"><label>DoT/DoH SNI</label><input v-model="form.hy2.resolver.sni" type="text" placeholder="cloudflare-dns.com" /></div>
        <div class="field"><label class="checkbox"><input v-model="form.hy2.resolver.insecure" type="checkbox" /> 跳过 DoT/DoH TLS 校验</label></div>
        <div class="field"><label class="checkbox"><input v-model="form.hy2.sniff.enable" type="checkbox" /> 启用协议嗅探</label></div>
        <div class="field"><label>嗅探超时</label><input v-model="form.hy2.sniff.timeout" type="text" placeholder="2s" /></div>
        <div class="field"><label>TCP 端口</label><input v-model="form.hy2.sniff.tcpPorts" type="text" placeholder="80,443,8000-9000" /></div>
        <div class="field"><label>UDP 端口</label><input v-model="form.hy2.sniff.udpPorts" type="text" placeholder="all" /></div>
        <div class="field full"><label class="checkbox"><input v-model="form.hy2.sniff.rewriteDomain" type="checkbox" /> 对已是域名的目标也进行嗅探</label></div>

        <div class="field full"><strong>ACL 与 Geo 数据</strong></div>
        <div class="field"><label>ACL 文件路径</label><input v-model="form.hy2.aclFile" type="text" placeholder="/data/acl.txt" />
          <span class="hint">填写后忽略上面的内联 ACL 规则，二者不能同时使用。</span>
        </div>
        <div class="field"><label>GeoIP 数据库</label><input v-model="form.hy2.geoip" type="text" placeholder="geoip.dat（留空自动下载）" /></div>
        <div class="field"><label>GeoSite 数据库</label><input v-model="form.hy2.geosite" type="text" placeholder="geosite.dat（留空自动下载）" /></div>
        <div class="field"><label>数据库刷新间隔</label><input v-model="form.hy2.geoUpdateInterval" type="text" placeholder="168h" /></div>

        </template>

        <template v-if="hy2Section === 'security'">
        <div class="field full"><strong>TLS / ACME 高级</strong></div>
        <div class="field"><label>SNI 校验</label><Select v-model="form.hy2.tls.sniGuard" :options="SNI_GUARD_OPTIONS" />
          <span class="hint">面板签发证书建议 dns-san；自签名或 Realms 使用 disable。</span>
        </div>
        <div class="field"><label>ACME CA</label><Select v-model="form.hy2.acme.ca" :options="ACME_CA_OPTIONS" /></div>
        <div class="field"><label>ACME 验证方式</label><Select v-model="form.hy2.acme.type" :options="ACME_TYPE_OPTIONS" /></div>
        <div class="field"><label>ACME 监听主机</label><input v-model="form.hy2.acme.listenHost" type="text" placeholder="0.0.0.0" /></div>
        <div class="field"><label>HTTP-01 端口</label><input v-model.number="form.hy2.acme.httpPort" type="number" min="0" max="65535" placeholder="80" /></div>
        <div class="field"><label>TLS-ALPN 端口</label><input v-model.number="form.hy2.acme.tlsPort" type="number" min="0" max="65535" placeholder="443" /></div>
        <template v-if="form.hy2.acme.type === 'dns'">
          <div class="field"><label>DNS 提供商</label><input v-model="form.hy2.acme.dnsName" type="text" placeholder="cloudflare" /></div>
          <div class="field full"><label>DNS 凭据（每行 key=value）</label><textarea v-model="form.hy2.acme.dnsConfigText" rows="3" placeholder="api_token=..."></textarea></div>
        </template>
        <div class="field full">
          <label>客户端 CA（mTLS）</label>
          <textarea v-model="form.hy2.clientCA" rows="4" placeholder="-----BEGIN CERTIFICATE-----"></textarea>
          <span class="hint">填写后启用客户端证书校验，证书会下发到节点；客户端需自行携带证书。不能与节点自签 ACME 同时使用。</span>
        </div>

        <div class="field full"><strong>ECH（Encrypted Client Hello）</strong></div>
        <div class="field full">
          <label>ech.pem 内容</label>
          <textarea v-model="form.hy2.echPem" rows="5" placeholder="在节点执行 hysteria ech --public-name decoy.example.com 后粘贴完整文件"></textarea>
          <span class="hint">需 Hysteria 2.10+。留空保持现有密钥；面板只保存私钥并从 ECH CONFIGS 块提取客户端配置。</span>
        </div>
        <div v-if="form.hy2.echConfig && !form.hy2.clearEch" class="field full">
          <span class="hint">当前已配置 ECH，客户端配置：<span class="mono">{{ form.hy2.echConfig.slice(0, 24) }}…</span></span>
        </div>
        <div v-if="form.hy2.echConfig" class="field full">
          <label class="checkbox"><input v-model="form.hy2.clearEch" type="checkbox" /> 清除现有 ECH 密钥</label>
        </div>

        <div class="field full"><strong>Mimic（UDP 伪装成 TCP，Linux）</strong></div>
        <div class="field full">
          <label class="checkbox"><input v-model="form.hy2.mimic.enabled" type="checkbox" /> 启用 mimic（需 Hysteria 2.12+，两端都启用，且不能与端口跳跃并用）</label>
        </div>
        <template v-if="form.hy2.mimic.enabled">
          <div class="field"><label>网卡</label><input v-model="form.hy2.mimic.interface" type="text" placeholder="eth0（留空自动选择）" /></div>
          <div class="field"><label>XDP 模式</label><Select v-model="form.hy2.mimic.xdpMode" :options="XDP_MODE_OPTIONS" /></div>
          <div class="field"><label>mimic 路径</label><input v-model="form.hy2.mimic.path" type="text" placeholder="/usr/bin/mimic" /></div>
          <div class="field full"><label>额外参数（逗号分隔）</label><input v-model="form.hy2.mimic.extraArgsText" type="text" placeholder="--pad-size 1200" /></div>
        </template>

        </template>

        <template v-if="hy2Section === 'advanced'">
        <div class="field full"><strong>Realms 调优</strong></div>
        <div class="field"><label>STUN 服务器（逗号分隔）</label><input v-model="form.hy2.realm.stunServersText" type="text" placeholder="stun.nextcloud.com:3478" /></div>
        <div class="field"><label>STUN 超时</label><input v-model="form.hy2.realm.stunTimeout" type="text" placeholder="5s" /></div>
        <div class="field"><label>打洞超时</label><input v-model="form.hy2.realm.punchTimeout" type="text" placeholder="5s" /></div>
        <div class="field"><label>心跳间隔</label><input v-model="form.hy2.realm.heartbeatInterval" type="text" placeholder="30s" /></div>
        <div class="field"><label>IP 栈</label><Select v-model="form.hy2.realm.ipMode" :options="IP_MODE_OPTIONS" /></div>
        <div class="field"><label class="checkbox"><input v-model="form.hy2.realm.portMapping" type="checkbox" /> 启用 UPnP/NAT-PMP 端口映射</label></div>
        <div class="field"><label>映射超时</label><input v-model="form.hy2.realm.portMapTimeout" type="text" placeholder="10s" /></div>
        <div class="field"><label>映射租期</label><input v-model="form.hy2.realm.portMapLifetime" type="text" placeholder="10m" /></div>
        <div class="field"><label class="checkbox"><input v-model="form.hy2.realm.insecure" type="checkbox" /> 跳过自建牵线服务器 TLS 校验（仅调试）</label></div>

        <div class="field full"><strong>高级 YAML 覆盖</strong></div>
        <div class="field full">
          <label>额外 YAML</label>
          <textarea v-model="form.hy2.extraYaml" rows="6" placeholder="congestion:&#10;  type: bbr&#10;  bbrProfile: conservative"></textarea>
          <span class="hint">会深合并到生成的配置中；listen、auth、trafficStats、tls、acme、ech、realm、mimic 由面板管理，不允许覆盖。</span>
        </div>
        </template>
      </div>

      <template #footer>
        <button class="btn" @click="modalOpen = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : editing ? '保存节点' : '创建节点' }}</button>
      </template>
    </Modal>

    <Modal v-model="installOpen" title="安装节点 Agent" size="lg">
      <div v-if="installError" class="banner banner-danger">{{ installError }}</div>
      <div v-if="install" class="stack">
        <div class="banner banner-info">
          在 VPS 上执行以下任一命令，Agent 将连接到 <span class="mono">{{ install.publicUrl }}</span>。
          当前运行方式：<strong>{{ runtimeLabel(install.runtimeMode) }}</strong>。修改运行方式后需要重新安装 Agent。
        </div>
        <div>
          <div class="flex" style="margin-bottom: 7px"><strong class="small">Docker（{{ runtimeLabel(install.runtimeMode) }}，当前配置）</strong><div class="spacer"></div><button class="btn btn-sm" @click="copy(install.installCommand)"><Copy :size="13" /> 复制</button></div>
          <pre class="code">{{ install.installCommand }}</pre>
        </div>
        <details>
          <summary class="small dim" style="cursor: pointer">其他运行方式（{{ runtimeLabel(install.runtimeMode === 'docker' ? 'process' : 'docker') }}）</summary>
          <pre class="code" style="margin-top: 8px">{{ install.runtimeMode === 'docker' ? install.processCommand : install.dockerCommand }}</pre>
        </details>
        <div>
          <div class="flex" style="margin-bottom: 7px"><strong class="small">一键安装脚本（--runtime {{ install.runtimeMode }}）</strong><div class="spacer"></div><button class="btn btn-sm" @click="copy(install.installScript)"><Copy :size="13" /> 复制</button></div>
          <pre class="code">{{ install.installScript }}</pre>
        </div>
        <div>
          <div class="flex" style="margin-bottom: 7px">
            <strong class="small">docker-compose.yml（{{ runtimeLabel(install.runtimeMode) }}）</strong>
            <div class="spacer"></div>
            <button class="btn btn-sm" @click="copy(install.compose)"><Copy :size="13" /> 复制</button>
            <button class="btn btn-sm" @click="downloadCompose"><Download :size="13" /> 下载</button>
          </div>
          <pre class="code">{{ install.compose }}</pre>
          <div class="small dim" style="margin-top: 6px">保存为 docker-compose.yml 后执行 <span class="mono">docker compose up -d</span>。修改运行方式后请重新打开安装信息。</div>
        </div>
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
  Copy, Download, FileCode2, Pencil, Plus, RefreshCw, RotateCcw, ScrollText, Server, Square, Terminal, Trash2, X, Zap,
} from 'lucide-vue-next'
import { adminPath, api } from '../api'
import { notify } from '../store'
import Modal from '../components/Modal.vue'
import Select from '../components/Select.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { copyText, fmtBytes, fmtDate, fmtRelative } from '../format'

const nodes = ref([])
const groups = ref([])
const loading = ref(false)
const syncingAll = ref(false)
const error = ref('')

const modalOpen = ref(false)
const tab = ref('basic')
const hy2Section = ref('basic')
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
    enabled: true,
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
      enabled: ib.enabled !== false,
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

function hy2Defaults() {
  return {
    quic: {
      initStreamReceiveWindow: 0, maxStreamReceiveWindow: 0,
      initConnReceiveWindow: 0, maxConnReceiveWindow: 0,
      maxIdleTimeout: '', maxIncomingStreams: 0,
      disablePathMTUDiscovery: false, disableStatelessReset: false,
    },
    congestion: { type: '', bbrProfile: '' },
    resolver: { type: '', addr: '', timeout: '', sni: '', insecure: false },
    sniff: { enable: false, timeout: '', rewriteDomain: false, tcpPorts: '', udpPorts: '' },
    masquerade: {
      type: '', proxyUrl: '', rewriteHost: true, insecure: false, xForwarded: false,
      fileDir: '', content: '', headersText: '', statusCode: 0,
      listenHttp: '', listenHttps: '', forceHttps: false,
    },
    acme: { ca: '', listenHost: '', type: '', httpPort: 0, tlsPort: 0, dnsName: '', dnsConfigText: '' },
    tls: { sniGuard: 'disable' },
    direct: { mode: '', bindIPv4: '', bindIPv6: '', bindDevice: '', fastOpen: false },
    realm: {
      uri: '', stunServersText: '', stunTimeout: '', punchTimeout: '', heartbeatInterval: '',
      insecure: false, ipMode: '', portMapping: false, portMapTimeout: '', portMapLifetime: '',
    },
    mimic: { enabled: false, interface: '', xdpMode: '', path: '', extraArgsText: '' },
    listenPortRange: '', trafficListen: '',
    ignoreClientBandwidth: false, disableLossCompensation: false,
    disableUDP: false, udpIdleTimeout: '', speedTest: false,
    geckoMinPacketSize: 0, geckoMaxPacketSize: 0,
    aclFile: '', geoip: '', geosite: '', geoUpdateInterval: '',
    clientCA: '', echPem: '', echConfig: '', clearEch: false,
    extraYaml: '',
  }
}

function mergeDefaults(base, src) {
  if (!src || typeof src !== 'object') return base
  const out = Array.isArray(base) ? [...base] : { ...base }
  for (const k of Object.keys(src)) {
    const v = src[k]
    if (v === null || v === undefined) continue
    if (typeof v === 'object' && !Array.isArray(v) && base && typeof base[k] === 'object' && !Array.isArray(base[k])) {
      out[k] = mergeDefaults(base[k], v)
    } else {
      out[k] = v
    }
  }
  return out
}

function mapToText(map) {
  if (!map || typeof map !== 'object') return ''
  return Object.entries(map).map(([k, v]) => `${k}=${v}`).join('\n')
}

function textToMap(text) {
  const out = {}
  for (const line of String(text || '').split('\n')) {
    const i = line.indexOf('=')
    if (i <= 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim()
    if (k) out[k] = v
  }
  return out
}

function hy2FromNode(n) {
  const h = mergeDefaults(hy2Defaults(), n.hy2 || {})
  if (!h.masquerade.type && n.masqueradeUrl) {
    h.masquerade.type = 'proxy'
    h.masquerade.proxyUrl = n.masqueradeUrl
    h.masquerade.rewriteHost = true
  }
  h.masquerade.headersText = mapToText(h.masquerade.headers)
  delete h.masquerade.headers
  h.acme.dnsConfigText = mapToText(h.acme.dnsConfig)
  delete h.acme.dnsConfig
  h.realm.stunServersText = (h.realm.stunServers || []).join(', ')
  delete h.realm.stunServers
  h.mimic.extraArgsText = (h.mimic.extraArgs || []).join(', ')
  delete h.mimic.extraArgs
  h.echPem = ''
  h.clearEch = false
  return h
}

function hy2Payload() {
  const h = JSON.parse(JSON.stringify(form.hy2))
  h.masquerade.headers = textToMap(h.masquerade.headersText)
  delete h.masquerade.headersText
  h.acme.dnsConfig = textToMap(h.acme.dnsConfigText)
  delete h.acme.dnsConfigText
  h.realm.stunServers = splitList(h.realm.stunServersText)
  delete h.realm.stunServersText
  h.mimic.extraArgs = splitList(h.mimic.extraArgsText)
  delete h.mimic.extraArgsText
  delete h.echConfig
  delete h.echPem
  delete h.clearEch
  return h
}

// hy2Template builds the sections controlled by a preset. Unmanaged sections
// (resolver/direct/realm/mimic/ECH/mTLS/ACL/extra YAML) are intentionally
// absent so applying a template never wipes advanced state.
function hy2Template(overrides = {}) {
  const base = {
    quic: {
      initStreamReceiveWindow: 0, maxStreamReceiveWindow: 0,
      initConnReceiveWindow: 0, maxConnReceiveWindow: 0,
      maxIdleTimeout: '', maxIncomingStreams: 0,
      disablePathMTUDiscovery: false, disableStatelessReset: false,
    },
    congestion: { type: '', bbrProfile: '' },
    sniff: { enable: false, timeout: '2s', rewriteDomain: false, tcpPorts: '80,443', udpPorts: 'all' },
    masquerade: {
      type: '', proxyUrl: '', rewriteHost: true, insecure: false, xForwarded: false,
      fileDir: '', content: '', headersText: '', statusCode: 0,
      listenHttp: '', listenHttps: '', forceHttps: false,
    },
    tls: { sniGuard: 'disable' },
    ignoreClientBandwidth: false,
    disableLossCompensation: false,
    disableUDP: false,
    udpIdleTimeout: '',
    speedTest: false,
    trafficListen: '',
    geckoMinPacketSize: 0,
    geckoMaxPacketSize: 0,
  }
  return mergeDefaults(base, overrides)
}

const HY2_SECTIONS = [
  { id: 'basic', label: '基础与 QUIC' },
  { id: 'transport', label: '传输与带宽' },
  { id: 'performance', label: '拥塞与出站' },
  { id: 'masquerade', label: '伪装' },
  { id: 'network', label: 'DNS / ACL' },
  { id: 'security', label: '安全 / ECH' },
  { id: 'advanced', label: 'Realms / 高级' },
]

const HY2_TEMPLATE_PRESETS = [
  { id: 'official', label: '官方默认', values: hy2Template() },
  {
    id: 'balanced',
    label: '推荐（均衡）',
    values: hy2Template({
      quic: { maxIdleTimeout: '30s', maxIncomingStreams: 1024 },
      udpIdleTimeout: '60s',
      trafficListen: '127.0.0.1',
    }),
  },
  {
    id: 'performance',
    label: '高性能',
    values: hy2Template({
      quic: {
        initStreamReceiveWindow: 8388608, maxStreamReceiveWindow: 8388608,
        initConnReceiveWindow: 20971520, maxConnReceiveWindow: 20971520,
        maxIdleTimeout: '30s', maxIncomingStreams: 4096,
      },
      congestion: { type: 'bbr', bbrProfile: 'aggressive' },
      sniff: { enable: true },
      udpIdleTimeout: '60s',
      trafficListen: '127.0.0.1',
    }),
  },
  {
    id: 'anticensorship',
    label: '抗审查',
    values: hy2Template({
      quic: { maxIdleTimeout: '30s', maxIncomingStreams: 1024 },
      sniff: { enable: true },
      masquerade: {
        type: 'proxy', proxyUrl: 'https://www.bing.com',
        rewriteHost: true, xForwarded: true,
      },
      udpIdleTimeout: '60s',
      trafficListen: '127.0.0.1',
    }),
  },
  {
    id: 'weak',
    label: '弱网优化',
    values: hy2Template({
      quic: { maxIdleTimeout: '60s', maxIncomingStreams: 2048 },
      congestion: { type: 'bbr', bbrProfile: 'conservative' },
      ignoreClientBandwidth: true,
      udpIdleTimeout: '120s',
      trafficListen: '127.0.0.1',
    }),
  },
]

function applyHy2Template(id, silent = false) {
  const tpl = HY2_TEMPLATE_PRESETS.find((t) => t.id === id)
  if (!tpl) return
  form.hy2 = mergeDefaults(form.hy2, tpl.values)
  if (!silent) notify(`已应用模板：${tpl.label}`)
}

function emptyForm() {
  return {
    name: '', flag: '', ip: '', domain: '', sni: '', listen: '', port: 443, statsPort: 9999,
    tlsMode: 'selfsigned', acmeEmail: '', portRange: '', hopInterval: '30s', obfsType: '',
    obfsPassword: '', bandwidthUp: '', bandwidthDown: '', masqueradeUrl: '',
    type: 'hysteria', cores: ['hysteria'], runtimeMode: 'process', maxOnline: 0, ranking: 1, active: true, groupIds: [], aclText: 'direct(all)',
    outbounds: [], sshPort: 22, sshUser: 'root', sshPassword: '', sshKey: '', sshHostKey: '', sshInstallDocker: false,
    hy2: hy2Defaults(),
    xray: { apiPort: 61000, inbounds: [emptyInbound()] },
  }
}
function resetForm() { Object.assign(form, emptyForm()) }
function splitList(v) { return String(v || '').split(',').map((s) => s.trim()).filter(Boolean) }

const XRAY_PROTOCOLS = [
  { id: 'vless', label: 'VLESS' },
  { id: 'vmess', label: 'VMess' },
  { id: 'trojan', label: 'Trojan' },
  { id: 'shadowsocks', label: 'Shadowsocks' },
]
const XRAY_PRESETS = [
  { id: 'vless', label: 'VLESS only', protocols: ['vless'] },
  { id: 'vless-vmess', label: 'VLESS + VMess', protocols: ['vless', 'vmess'] },
  { id: 'all', label: '全协议', protocols: ['vless', 'vmess', 'trojan', 'shadowsocks'] },
]
const RUNTIME_OPTIONS = [
  { value: 'process', label: '进程模式（同容器内运行代理核心）' },
  { value: 'docker', label: 'Docker 模式（管理独立代理容器）' },
]
const TLS_MODE_OPTIONS = [
  { value: 'selfsigned', label: '自签名（无需域名）' },
  { value: 'managed', label: '面板签发（CSR，推荐）' },
  { value: 'acme', label: '节点自签 ACME（TLS-ALPN）' },
]
const OBFS_OPTIONS = [
  { value: '', label: '已禁用' },
  { value: 'salamander', label: 'Salamander' },
  { value: 'gecko', label: 'Gecko（Hysteria 2.9+）' },
]
const PROTOCOL_OPTIONS = [
  { value: 'vless', label: 'VLESS' },
  { value: 'vmess', label: 'VMess' },
  { value: 'trojan', label: 'Trojan' },
  { value: 'shadowsocks', label: 'Shadowsocks' },
]
const TRANSPORT_OPTIONS = [
  { value: 'tcp', label: 'TCP（RAW）' },
  { value: 'ws', label: 'WebSocket' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'xhttp', label: 'XHTTP' },
  { value: 'mkcp', label: 'mKCP' },
]
const SS_METHOD_OPTIONS = [
  { value: '2022-blake3-aes-128-gcm', label: '2022-blake3-aes-128-gcm' },
  { value: '2022-blake3-aes-256-gcm', label: '2022-blake3-aes-256-gcm' },
]
const OUTBOUND_TYPE_OPTIONS = [
  { value: 'socks5', label: 'socks5' },
  { value: 'http', label: 'http' },
  { value: 'direct', label: 'direct' },
]
const CONGESTION_OPTIONS = [
  { value: '', label: '默认（BBR）' },
  { value: 'bbr', label: 'BBR' },
  { value: 'reno', label: 'Reno' },
]
const BBR_PROFILE_OPTIONS = [
  { value: '', label: 'standard（默认）' },
  { value: 'standard', label: 'standard' },
  { value: 'conservative', label: 'conservative' },
  { value: 'aggressive', label: 'aggressive' },
]
const MASQ_TYPE_OPTIONS = [
  { value: '', label: '关闭' },
  { value: 'proxy', label: '反向代理站点（proxy）' },
  { value: 'file', label: '静态目录（file）' },
  { value: 'string', label: '固定字符串（string）' },
]
const RESOLVER_TYPE_OPTIONS = [
  { value: '', label: '使用系统 DNS' },
  { value: 'udp', label: 'UDP' },
  { value: 'tcp', label: 'TCP' },
  { value: 'tls', label: 'DNS over TLS' },
  { value: 'https', label: 'DNS over HTTPS' },
]
const DIRECT_MODE_OPTIONS = [
  { value: '', label: 'auto（默认双栈）' },
  { value: 'auto', label: 'auto' },
  { value: '4', label: '仅 IPv4' },
  { value: '6', label: '仅 IPv6' },
  { value: '46', label: '优先 IPv4' },
  { value: '64', label: '优先 IPv6' },
]
const SNI_GUARD_OPTIONS = [
  { value: 'disable', label: 'disable（兼容自签名）' },
  { value: 'dns-san', label: 'dns-san（推荐用于有效证书）' },
  { value: 'strict', label: 'strict（严格匹配）' },
]
const ACME_CA_OPTIONS = [
  { value: '', label: '默认' },
  { value: 'letsencrypt', label: "Let's Encrypt" },
  { value: 'zerossl', label: 'ZeroSSL' },
]
const ACME_TYPE_OPTIONS = [
  { value: '', label: '保持默认（TLS-ALPN）' },
  { value: 'tls', label: 'TLS-ALPN' },
  { value: 'http', label: 'HTTP-01' },
  { value: 'dns', label: 'DNS-01' },
]
const XDP_MODE_OPTIONS = [
  { value: '', label: '自动' },
  { value: 'native', label: 'native' },
  { value: 'skb', label: 'skb' },
]
const IP_MODE_OPTIONS = [
  { value: '', label: 'dual（默认）' },
  { value: 'dual', label: 'dual' },
  { value: 'v4', label: '仅 IPv4' },
  { value: 'v6', label: '仅 IPv6' },
]
function securityOptions(ib) {
  const reality = ['tcp', 'xhttp', 'grpc'].includes(ib.transport)
  return [
    { value: 'reality', label: 'REALITY', disabled: !reality },
    { value: 'tls', label: 'TLS' },
    { value: 'none', label: '无加密' },
  ]
}
function protocolDefaults(proto) {
  switch (proto) {
    case 'vmess':
      return { tag: 'vmess-in', port: 8443, transport: 'ws', security: 'tls', wsPath: '/vm', flow: '' }
    case 'trojan':
      return { tag: 'trojan-in', port: 8444, transport: 'grpc', security: 'tls', grpcServiceName: 'trojan', flow: '' }
    case 'shadowsocks':
      return { tag: 'ss-in', port: 8445, transport: 'tcp', security: 'none', ssMethod: '2022-blake3-aes-128-gcm', flow: '' }
    default:
      return { tag: 'vless-in', port: 443, transport: 'tcp', security: 'reality', flow: 'xtls-rprx-vision' }
  }
}
function protocolInbounds(proto) { return form.xray.inbounds.filter((ib) => ib.protocol === proto) }
function protocolEnabled(proto) { return protocolInbounds(proto).some((ib) => ib.enabled !== false) }
function enabledInboundCount() { return form.xray.inbounds.filter((ib) => ib.enabled !== false).length }
function usedXrayPorts() {
  const used = new Set()
  for (const ib of form.xray.inbounds) {
    const p = Number(ib.port)
    if (p) used.add(p)
  }
  const api = Number(form.xray.apiPort)
  if (api) used.add(api)
  return used
}
function allocatePort(start) {
  const used = usedXrayPorts()
  let port = Math.max(1, Number(start) || 443)
  while (port <= 65535 && used.has(port)) port++
  return port <= 65535 ? port : 443
}
function uniqueTag(base) {
  let tag = base
  let i = 2
  while (form.xray.inbounds.some((ib) => ib.tag === tag)) tag = `${base}-${i++}`
  return tag
}
function makeInbound(proto) {
  const d = protocolDefaults(proto)
  const ib = { ...emptyInbound(), protocol: proto, ...d }
  ib.port = allocatePort(d.port)
  ib.tag = uniqueTag(d.tag)
  ib.enabled = true
  normalizeInbound(ib)
  return ib
}
function setProtocolEnabled(proto, on) {
  const list = protocolInbounds(proto)
  if (on) {
    if (!list.length) form.xray.inbounds.push(makeInbound(proto))
    else list.forEach((ib) => { ib.enabled = true })
  } else {
    list.forEach((ib) => { ib.enabled = false })
  }
}
function toggleProtocol(proto) { setProtocolEnabled(proto, !protocolEnabled(proto)) }
function applyPreset(protocols) {
  if (!form.cores.includes('xray')) {
    form.cores.push('xray')
    form.type = form.cores[0]
  }
  for (const p of XRAY_PROTOCOLS) setProtocolEnabled(p.id, protocols.includes(p.id))
}

function toggleCore(core) {
  const i = form.cores.indexOf(core)
  if (i >= 0) {
    if (form.cores.length > 1) form.cores.splice(i, 1)
  } else {
    form.cores.push(core)
    if (core === 'xray' && enabledInboundCount() === 0) {
      if (!form.xray.inbounds.length) form.xray.inbounds.push(makeInbound('vless'))
      else form.xray.inbounds[0].enabled = true
    }
  }
  form.type = form.cores[0]
  if (!form.cores.includes('hysteria') && (tab.value === 'advanced' || tab.value === 'hy2')) tab.value = 'tls'
}
function addInbound() {
  const ib = emptyInbound()
  const last = Number(form.xray.inbounds[form.xray.inbounds.length - 1]?.port) || Number(form.port) || 443
  ib.port = allocatePort(last + 1)
  ib.tag = uniqueTag(`${ib.protocol}-in`)
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
function runtimeMode(n) {
  return n?.runtimeMode === 'docker' ? 'docker' : 'process'
}
function runtimeLabel(mode) {
  return mode === 'docker' ? 'Docker 模式' : '进程模式'
}
function versionLabel(n) {
  const cores = (n.cores && n.cores.length ? n.cores : [n.type || 'hysteria'])
  const parts = []
  if (cores.includes('hysteria')) parts.push('Hysteria ' + (n.hy2Version || 'unknown'))
  if (cores.includes('xray')) parts.push('Xray ' + (n.xrayVersion || 'unknown'))
  return parts.join(' · ') || 'unknown'
}
watch(() => form.cores.slice(), () => {
  if (!form.cores.includes('hysteria') && (tab.value === 'advanced' || tab.value === 'hy2')) tab.value = 'tls'
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
  applyHy2Template('balanced', true)
  tab.value = 'basic'
  hy2Section.value = 'basic'
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
    runtimeMode: n.runtimeMode || 'process',
    sshPort: n.sshPort || 22, sshUser: n.sshUser || '', sshPassword: '', sshKey: '',
    sshHostKey: n.sshHostKey || '', sshInstallDocker: !!n.sshInstallDocker,
    groupIds: (n.groups || []).map((g) => g.id),
    aclText: (n.acl || []).join('\n'),
    outbounds: (n.outbounds || []).map((o) => ({ ...o })),
    hy2: hy2FromNode(n),
    xray: { apiPort: n.xray?.apiPort || 61000, inbounds: inboundsFromNode(n) },
  })
  tab.value = 'basic'
  hy2Section.value = 'basic'
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
    if (form.cores.includes('xray') && enabledInboundCount() === 0) {
      formError.value = '至少启用一个 Xray 入站'
      return
    }
    const hy2 = hy2Payload()
    const payload = {
      type: form.cores[0],
      cores: form.cores.slice(),
      name: form.name, flag: form.flag, ip: form.ip, domain: form.domain, sni: form.sni,
      listen: form.listen, port: Number(form.port) || 443, statsPort: Number(form.statsPort) || 9999,
      tlsMode: form.tlsMode, acmeEmail: form.acmeEmail, portRange: form.portRange,
      hopInterval: form.hopInterval, obfsType: form.obfsType, obfsPassword: form.obfsPassword,
      bandwidthUp: form.bandwidthUp, bandwidthDown: form.bandwidthDown,
      masqueradeUrl: hy2.masquerade.type === 'proxy' ? hy2.masquerade.proxyUrl : '',
      hy2,
      echPem: form.hy2.echPem,
      clearEch: !!form.hy2.clearEch,
      maxOnline: Number(form.maxOnline) || 0, ranking: Number(form.ranking) || 1, active: form.active,
      runtimeMode: form.runtimeMode,
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
            enabled: rest.enabled !== false,
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
      const rev = res.configRevision || res.node?.configRevision || 0
      if (res.configError) notify(res.configError, 'error')
      else if (!res.configChanged) notify(`节点已更新，生成的配置没有变化（版本 ${rev}）`)
      else if (res.pushed) notify(`节点已更新，配置版本 ${rev} 已推送`)
      else notify(`节点已更新，配置版本 ${rev} 已保存，Agent 连接后自动推送`)
      modalOpen.value = false
      await load()
    } else {
      res = await api.createNode(payload)
      const rev = res.configRevision || res.node?.configRevision || 0
      if (res.configError) notify(res.configError, 'error')
      else notify(`节点已创建，配置版本 ${rev} 已保存 — 请安装 Agent 以连接`)
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
    if (res.configError) notify(res.configError, 'error')
    else if (!res.configChanged) notify(res.pushed ? `配置未变化，已重新推送版本 ${res.revision}` : `配置未变化（版本 ${res.revision}）`)
    else if (res.pushed) notify(`配置版本 ${res.revision} 已推送`)
    else if (!res.connected) notify(`配置版本 ${res.revision} 已保存（Agent 离线，连接后自动推送）`)
    else notify(`配置版本 ${res.revision} 已保存（推送失败，请查看日志）`)
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
    install.value = {
      ...install.value, token: res.token, runtimeMode: res.runtimeMode,
      installScript: res.installScript, installCommand: res.installCommand,
      processCommand: res.processCommand, dockerCommand: res.dockerCommand,
      compose: res.compose,
    }
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

function downloadCompose() {
  if (!install.value?.compose) return
  const blob = new Blob([install.value.compose], { type: 'text/yaml;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  const name = String(install.value.nodeName || install.value.nodeId || 'node').replace(/[^\w.-]+/g, '-')
  a.href = url
  a.download = `mikvace-node-${name}.yml`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
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
