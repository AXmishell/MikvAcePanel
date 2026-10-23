<template>
  <div class="home">
    <header class="home-top">
      <router-link to="/" class="home-brand">
        <div class="brand-logo">
          <img v-if="logoUrl && !logoFailed" :src="logoUrl" alt="" @error="logoFailed = true" />
          <span v-else>{{ initial }}</span>
        </div>
        <div>
          <div class="brand-name">{{ panelName }}</div>
          <div class="brand-sub">Connectivity · Self-hosted</div>
        </div>
      </router-link>

      <nav class="home-nav">
        <a href="#features">能力</a>
        <a href="#start">开始使用</a>
        <a href="#clients">客户端</a>
      </nav>

      <div class="spacer"></div>

      <button class="icon-btn" :title="theme.current === 'light' ? '切换为深色主题' : '切换为浅色主题'" @click="toggleTheme">
        <Moon v-if="theme.current === 'light'" :size="17" />
        <Sun v-else :size="17" />
      </button>
      <router-link class="btn btn-sm home-admin" :to="adminTarget">{{ adminLabel }}</router-link>
      <router-link v-if="portalEnabled || portalAuth.token" class="btn btn-sm btn-primary" :to="portalTarget">{{ portalLabel }}</router-link>
    </header>

    <main class="home-main">
      <section class="home-hero">
        <div class="hero-copy">
          <div class="eyebrow">Personal connectivity</div>
          <h1>一条订阅，<br>连接你的<span>每一台设备</span>。</h1>
          <p>
            由管理员统一分发 Hysteria 2 与 Xray 节点，客户端自动同步。
            登录用户中心即可查看订阅、流量、到期时间与在线设备。
          </p>
          <div class="hero-actions">
            <router-link v-if="portalEnabled || portalAuth.token" class="btn btn-primary btn-lg" :to="portalTarget">
              <UserRound :size="16" /> {{ portalLabel }}
            </router-link>
            <a v-else-if="supportUrl" class="btn btn-primary btn-lg" :href="supportUrl" target="_blank" rel="noopener">
              <LifeBuoy :size="16" /> 联系管理员获取订阅
            </a>
            <span v-else class="btn btn-primary btn-lg is-disabled">用户中心暂未开放</span>
            <a class="btn btn-lg" href="#start"><ArrowRight :size="16" /> 了解使用方式</a>
          </div>
          <div class="hero-meta">
            <span class="status"><span class="dot" :class="online === false ? 'error' : 'online'"></span>{{ online === false ? '服务暂不可用' : '服务运行中' }}</span>
            <span class="hero-sep">·</span>
            <span>{{ panelName }}</span>
            <span class="hero-sep">·</span>
            <span>Hysteria 2 / Xray</span>
          </div>
        </div>

        <aside class="hero-card">
          <div class="hero-card-top">
            <div class="eyebrow">Subscription</div>
            <span class="badge" :class="online === false ? 'badge-danger' : 'badge-ok'">
              <span class="dot" :class="online === false ? 'error' : 'online'"></span>{{ online === false ? 'Offline' : 'Online' }}
            </span>
          </div>

          <div class="hero-link">
            <span class="mono">/api/files/••••••••••••</span>
            <Check :size="15" />
          </div>

          <div class="hero-card-rows">
            <div class="hero-row"><RefreshCw :size="15" /><span>订阅自动同步节点变更</span></div>
            <div class="hero-row"><Gauge :size="15" /><span>流量、到期与在线设备可见</span></div>
            <div class="hero-row"><Layers :size="15" /><span>双核心节点自动适配客户端</span></div>
          </div>

          <div class="hero-card-foot">
            <span class="dot online"></span>
            <span>由管理员统一维护</span>
          </div>
        </aside>
      </section>

      <section id="features" class="home-section">
        <div class="section-head">
          <div>
            <div class="eyebrow">Capabilities</div>
            <h2>为日常连接准备的细节</h2>
          </div>
          <p>不只是一个订阅链接。节点、配额、设备与客户端兼容性都在同一套控制面中。</p>
        </div>

        <div class="feature-grid">
          <article v-for="f in features" :key="f.title" class="feature">
            <div class="feature-icon"><component :is="f.icon" :size="17" /></div>
            <h3>{{ f.title }}</h3>
            <p>{{ f.desc }}</p>
          </article>
        </div>
      </section>

      <section id="start" class="home-section">
        <div class="section-head">
          <div>
            <div class="eyebrow">Getting started</div>
            <h2>三步开始使用</h2>
          </div>
        </div>

        <div class="step-grid">
          <article v-for="(s, i) in steps" :key="s.title" class="step">
            <div class="step-num">0{{ i + 1 }}</div>
            <h3>{{ s.title }}</h3>
            <p>{{ s.desc }}</p>
          </article>
        </div>
      </section>

      <section id="clients" class="home-section">
        <div class="clients-card">
          <div>
            <div class="eyebrow">Clients</div>
            <h2>兼容你正在使用的客户端</h2>
            <p>订阅会按 User-Agent 自动返回 Clash、sing-box、Shadowrocket 或 URI 列表，无需手动转换格式。</p>
          </div>
          <div class="client-chips">
            <span v-for="c in clients" :key="c" class="chip">{{ c }}</span>
          </div>
        </div>
      </section>

      <section class="home-cta">
        <div>
          <h2>准备好连接了吗？</h2>
          <p v-if="portalEnabled">使用管理员提供的用户 ID 与密码登录，订阅和用量一目了然。</p>
          <p v-else>用户中心暂未开放，请联系管理员获取订阅链接或临时访问方式。</p>
        </div>
        <div class="home-cta-actions">
          <router-link v-if="portalEnabled || portalAuth.token" class="btn btn-primary btn-lg" :to="portalTarget">
            {{ portalLabel }} <ArrowRight :size="16" />
          </router-link>
          <a v-if="supportUrl" class="btn btn-lg" :href="supportUrl" target="_blank" rel="noopener">联系支持</a>
          <router-link v-else class="btn btn-lg" :to="adminTarget">{{ adminLabel }}</router-link>
        </div>
      </section>
    </main>

    <footer class="home-foot">
      <div>© {{ year }} {{ panelName }}</div>
      <div class="home-foot-links">
        <a href="#features">能力</a>
        <a href="#start">开始使用</a>
        <router-link v-if="portalEnabled || portalAuth.token" :to="portalTarget">用户中心</router-link>
        <router-link :to="adminTarget">{{ adminLabel }}</router-link>
      </div>
    </footer>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import {
  ArrowRight, Check, Gauge, Globe2, Layers, LifeBuoy, LockKeyhole, Moon, RefreshCw,
  Smartphone, Sun, UserRound, Zap,
} from 'lucide-vue-next'
import { API_BASE, portalApi } from '../api'
import { auth, portalAuth } from '../store'
import { theme, toggleTheme } from '../theme'

const config = ref(null)
const online = ref(true)
const logoFailed = ref(false)
const year = new Date().getFullYear()

const panelName = computed(() => config.value?.panelName || 'MikvAce Panel')
const logoUrl = computed(() => config.value?.logoUrl || '')
const supportUrl = computed(() => config.value?.supportUrl || '')
const portalEnabled = computed(() => (config.value ? !!config.value.enabled : true))
const initial = computed(() => (panelName.value || 'M').slice(0, 1).toUpperCase())

const portalTarget = computed(() => (portalAuth.token ? '/portal' : '/portal/login'))
const portalLabel = computed(() => (portalAuth.token ? '我的订阅' : '进入用户中心'))
const adminTarget = computed(() => (auth.token ? '/dashboard' : '/login'))
const adminLabel = computed(() => (auth.token ? '控制台' : '管理员登录'))

const features = [
  { icon: Zap, title: '双核心加速', desc: 'Hysteria 2 的 QUIC/UDP 与 Xray 的 TCP 入站由同一节点提供，按网络环境选择更合适的连接。' },
  { icon: Globe2, title: '全客户端兼容', desc: 'Clash / Mihomo、sing-box、Shadowrocket、HAPP 与原始 URI 列表，订阅按客户端自动适配。' },
  { icon: Gauge, title: '用量透明', desc: '流量、到期时间、在线设备集中在用户中心展示，配额与设备限制实时生效。' },
  { icon: LockKeyhole, title: '安全可控', desc: '独立用户会话、登录限流、设备数限制与可重置订阅 Token，账号状态随时可查。' },
]

const steps = [
  { title: '获取账号', desc: '向管理员获取用户 ID 与密码，或直接使用已经拿到的订阅链接。' },
  { title: '导入客户端', desc: '登录用户中心复制订阅地址或扫码，客户端会自动识别 Clash、sing-box 等格式。' },
  { title: '连接使用', desc: '选择节点即可连接。节点或账号状态变化会自动同步到订阅。' },
]

const clients = ['Clash / Mihomo', 'sing-box', 'Shadowrocket', 'HAPP', 'URI list', 'Xray']

onMounted(async () => {
  try {
    config.value = await portalApi.config()
  } catch {
    config.value = { enabled: true, panelName: 'MikvAce Panel' }
  }
  try {
    const res = await fetch(API_BASE + '/health', { headers: { Accept: 'application/json' } })
    online.value = res.ok
  } catch {
    online.value = false
  }
})
</script>

<style scoped>
.home { min-height: 100vh; display: flex; flex-direction: column; }

.home-top {
  position: sticky; top: 0; z-index: 20;
  display: flex; align-items: center; gap: 14px;
  height: 62px; padding: 0 26px;
  border-bottom: 1px solid var(--line);
  background: var(--topbar);
  backdrop-filter: blur(16px);
}
.home-brand { display: flex; align-items: center; gap: 12px; min-width: 0; color: var(--text); }
.home-brand:hover { color: var(--text); }
.home-brand .brand-logo img { width: 100%; height: 100%; object-fit: cover; border-radius: 9px; }
.home-nav { display: flex; align-items: center; gap: 20px; margin-left: 20px; }
.home-nav a { color: var(--muted); font-size: 12.5px; }
.home-nav a:hover { color: var(--text); }
.home-admin { color: var(--muted); }
.home-admin:hover { color: var(--text); }

.home-main { width: 100%; max-width: 1180px; margin: 0 auto; padding: 0 26px 60px; flex: 1; }

.home-hero {
  display: grid; grid-template-columns: 1.1fr 0.9fr;
  gap: 48px; align-items: center;
  padding: 74px 0 62px;
}
.hero-copy h1 {
  margin: 16px 0 20px;
  font-size: clamp(36px, 5.4vw, 62px);
  line-height: 1.02; letter-spacing: -0.055em; font-weight: 650;
}
.hero-copy h1 span { color: var(--accent); }
.hero-copy p { margin: 0; max-width: 52ch; color: var(--muted); font-size: 13.5px; line-height: 1.75; }
.hero-actions { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 26px; }
.btn-lg { padding: 11px 17px; font-size: 13px; }
.btn.is-disabled { opacity: 0.5; cursor: not-allowed; }
.hero-meta {
  display: flex; align-items: center; flex-wrap: wrap; gap: 8px;
  margin-top: 22px; font-family: var(--mono); font-size: 10px;
  letter-spacing: 0.05em; color: var(--muted-2);
}
.hero-meta .status { display: inline-flex; align-items: center; gap: 7px; color: var(--muted); }
.hero-sep { opacity: 0.45; }

.hero-card {
  position: relative; overflow: hidden;
  padding: 20px;
  border: 1px solid var(--line); border-radius: 18px;
  background: linear-gradient(155deg, var(--panel-2) 0%, var(--panel) 55%, var(--bg-soft) 100%);
  box-shadow: var(--shadow);
}
.hero-card::after {
  content: ""; position: absolute; right: -70px; bottom: -90px;
  width: 230px; height: 230px; border: 1px solid var(--line);
  border-radius: 50%; pointer-events: none;
}
.hero-card-top { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.hero-link {
  display: flex; align-items: center; justify-content: space-between; gap: 10px;
  margin-top: 18px; padding: 12px 13px;
  border: 1px solid var(--line); border-radius: 9px;
  background: var(--input-bg); color: var(--muted);
  font-size: 11px;
}
.hero-link svg { color: var(--ok); flex: none; }
.hero-card-rows { display: flex; flex-direction: column; gap: 11px; margin-top: 20px; }
.hero-row { display: flex; align-items: center; gap: 10px; color: var(--muted); font-size: 12px; }
.hero-row svg { color: var(--accent); flex: none; }
.hero-card-foot {
  display: flex; align-items: center; gap: 8px;
  margin-top: 22px; padding-top: 15px; border-top: 1px solid var(--line-soft);
  font-family: var(--mono); font-size: 9.5px; letter-spacing: 0.08em;
  text-transform: uppercase; color: var(--muted-2);
}

.home-section { padding: 44px 0 10px; }
.section-head {
  display: flex; align-items: flex-end; justify-content: space-between; gap: 28px;
  margin-bottom: 24px;
}
.section-head h2, .clients-card h2, .home-cta h2 { margin: 10px 0 0; font-size: clamp(23px, 3vw, 32px); letter-spacing: -0.04em; font-weight: 650; }
.section-head p { margin: 0; max-width: 44ch; color: var(--muted-2); font-size: 12px; line-height: 1.7; }

.feature-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 14px; }
.feature {
  padding: 18px;
  border: 1px solid var(--line); border-radius: var(--radius);
  background: var(--panel);
  transition: border-color 0.15s ease, transform 0.15s ease;
}
.feature:hover { border-color: var(--line-strong); transform: translateY(-2px); }
.feature-icon {
  width: 32px; height: 32px; display: grid; place-items: center;
  border: 1px solid var(--accent-line); border-radius: 8px;
  background: var(--accent-soft); color: var(--accent);
}
.feature h3 { margin: 15px 0 7px; font-size: 13px; font-weight: 650; }
.feature p { margin: 0; color: var(--muted-2); font-size: 11.5px; line-height: 1.65; }

.step-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 14px; }
.step {
  padding: 20px; border: 1px solid var(--line); border-radius: var(--radius);
  background: var(--panel);
}
.step-num {
  font-family: var(--mono); font-size: 10px; letter-spacing: 0.14em;
  color: var(--accent); margin-bottom: 14px;
}
.step h3 { margin: 0 0 7px; font-size: 14px; font-weight: 650; }
.step p { margin: 0; color: var(--muted-2); font-size: 11.5px; line-height: 1.7; }

.clients-card {
  display: grid; grid-template-columns: 1.1fr 0.9fr; gap: 30px; align-items: center;
  padding: 26px 28px;
  border: 1px solid var(--line); border-radius: 16px;
  background: linear-gradient(120deg, var(--panel-2), var(--panel));
}
.clients-card p { margin: 10px 0 0; max-width: 46ch; color: var(--muted-2); font-size: 12px; line-height: 1.7; }
.client-chips { display: flex; flex-wrap: wrap; gap: 8px; justify-content: flex-end; }
.client-chips .chip { padding: 7px 12px; font-size: 11.5px; }

.home-cta {
  display: flex; align-items: center; justify-content: space-between; gap: 24px;
  margin-top: 46px; padding: 28px 30px;
  border: 1px solid var(--accent-line); border-radius: 18px;
  background: radial-gradient(120% 140% at 0% 0%, var(--accent-soft), transparent 62%), var(--panel);
}
.home-cta p { margin: 9px 0 0; color: var(--muted); font-size: 12.5px; }
.home-cta-actions { display: flex; gap: 9px; flex-wrap: wrap; }

.home-foot {
  width: 100%; max-width: 1180px; margin: 0 auto;
  display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap;
  padding: 22px 26px 30px;
  border-top: 1px solid var(--line-soft);
  font-family: var(--mono); font-size: 9.5px; letter-spacing: 0.08em;
  text-transform: uppercase; color: var(--muted-2);
}
.home-foot-links { display: flex; gap: 16px; }
.home-foot-links a { color: var(--muted-2); }
.home-foot-links a:hover { color: var(--text); }

@media (max-width: 980px) {
  .home-hero { grid-template-columns: 1fr; gap: 30px; padding: 48px 0 40px; }
  .feature-grid { grid-template-columns: repeat(2, 1fr); }
  .clients-card { grid-template-columns: 1fr; }
  .client-chips { justify-content: flex-start; }
}
@media (max-width: 760px) {
  .home-nav { display: none; }
  .home-main { padding: 0 16px 44px; }
  .home-top { padding: 0 16px; gap: 8px; }
  .home-admin { display: none; }
  .section-head { flex-direction: column; align-items: flex-start; gap: 10px; }
  .step-grid { grid-template-columns: 1fr; }
  .home-cta { flex-direction: column; align-items: flex-start; }
  .home-foot { padding: 20px 16px 26px; }
}
@media (max-width: 520px) {
  .feature-grid { grid-template-columns: 1fr; }
  .hero-actions .btn { width: 100%; }
  .home-brand .brand-sub { display: none; }
}
</style>
