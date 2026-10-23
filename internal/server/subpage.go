package server

const subPageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<meta name="color-scheme" content="dark light">
<script>try{var t=localStorage.getItem('mikvace_theme');if(t==='light'||t==='dark')document.documentElement.dataset.theme=t}catch(e){}</script>
<title>{{.Title}} — {{.PanelName}}</title>
<style>
:root{
  color-scheme:dark;
  --bg:#0d0d0c;--panel:#161615;--panel2:#1b1b19;--panel3:#242421;--input:#0a0a09;
  --line:#2d2d28;--line-soft:#232320;--line-strong:#3d3d36;
  --text:#f2efe7;--muted:#a8a49a;--muted2:#77736a;
  --accent:#ff6b3d;--accent-hover:#ff8259;--accent-soft:rgba(255,107,61,.11);--accent-line:rgba(255,107,61,.34);
  --on-accent:#1a0b06;--ok:#86c99b;--danger:#ff6f6f;
  --dot:rgba(242,239,231,.055);--shadow:0 24px 64px rgba(0,0,0,.42);
  --font:"Inter","SF Pro Text",ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,"PingFang SC","Noto Sans SC",sans-serif;
  --mono:ui-monospace,"SF Mono","Cascadia Mono","Roboto Mono",Menlo,Consolas,monospace;
}
:root[data-theme="light"]{
  color-scheme:light;
  --bg:#f3efe6;--panel:#fbf9f4;--panel2:#f5f1e8;--panel3:#ebe5d8;--input:#fffdf8;
  --line:#d8d0c1;--line-soft:#e6dfd2;--line-strong:#bfb5a2;
  --text:#1b1915;--muted:#5d584e;--muted2:#8b8477;
  --accent:#d9481f;--accent-hover:#bf3d17;--accent-soft:rgba(217,72,31,.09);--accent-line:rgba(217,72,31,.3);
  --on-accent:#fff9f4;--ok:#2f7c4f;--danger:#bd352b;
  --dot:rgba(27,25,21,.075);--shadow:0 22px 54px rgba(46,39,27,.12);
}
*{box-sizing:border-box}
body{
  margin:0;min-height:100vh;
  background-color:var(--bg);
  background-image:radial-gradient(circle at 1px 1px,var(--dot) 1px,transparent 0);
  background-size:24px 24px;
  color:var(--text);font:14px/1.55 var(--font);
  -webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility;
}
a{color:var(--accent);text-decoration:none}
a:hover{color:var(--accent-hover)}
button{font:inherit}
::selection{background:var(--accent-soft)}
.wrap{max-width:1080px;margin:0 auto;padding:30px 20px 60px}
.top{display:flex;align-items:center;justify-content:space-between;gap:16px;padding-bottom:22px;border-bottom:1px solid var(--line);margin-bottom:34px}
.brand{display:flex;align-items:center;gap:11px;min-width:0}
.logo{width:36px;height:36px;border-radius:10px;border:1px solid var(--line-strong);background:var(--panel2);display:flex;align-items:center;justify-content:center;font-family:var(--mono);font-size:16px;font-weight:700;color:var(--text);object-fit:cover;flex:none}
.logo-mark{position:relative}
.logo-mark::after{content:"";position:absolute;top:-3px;right:-3px;width:8px;height:8px;border-radius:2px;background:var(--accent)}
.brand-name{font-weight:650;font-size:14px;letter-spacing:-.02em;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.brand-sub{font-family:var(--mono);font-size:9px;letter-spacing:.16em;text-transform:uppercase;color:var(--muted2);margin-top:2px}
.top-meta{display:flex;align-items:center;gap:7px;padding:6px 10px;border:1px solid var(--line);border-radius:6px;background:var(--panel);font-family:var(--mono);font-size:9.5px;letter-spacing:.12em;text-transform:uppercase;color:var(--muted);white-space:nowrap}
.top-meta .dot{width:6px;height:6px;border-radius:50%;background:var(--ok);box-shadow:0 0 0 3px rgba(134,201,155,.12)}
.hero{display:grid;grid-template-columns:1fr auto;gap:24px;align-items:end;margin-bottom:22px}
.eyebrow{font-family:var(--mono);font-size:10px;letter-spacing:.18em;text-transform:uppercase;color:var(--accent);margin-bottom:12px}
.hero h1{margin:0 0 10px;font-size:clamp(30px,4.5vw,46px);line-height:1.05;letter-spacing:-.045em;font-weight:650}
.hero p{margin:0;color:var(--muted);font-size:13px;max-width:52ch}
.hero-side{text-align:right}
.hero-label{font-family:var(--mono);font-size:9px;letter-spacing:.14em;text-transform:uppercase;color:var(--muted2)}
.hero-account{font-family:var(--mono);font-size:15px;margin-top:4px;color:var(--text);word-break:break-all}
.grid{display:grid;grid-template-columns:1.15fr .85fr;gap:16px}
.card{background:var(--panel);border:1px solid var(--line);border-radius:14px;overflow:hidden}
.card-head{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px;border-bottom:1px solid var(--line-soft)}
.card h2{display:flex;align-items:center;gap:8px;margin:0;font-size:12.5px;font-weight:650;letter-spacing:.01em}
.card h2::before{content:"";width:5px;height:5px;border-radius:1px;background:var(--accent);flex:none}
.card-note{font-family:var(--mono);font-size:9px;letter-spacing:.12em;text-transform:uppercase;color:var(--muted2);white-space:nowrap}
.card-body{padding:16px}
.stats{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin-bottom:16px}
.stat{background:var(--panel2);border:1px solid var(--line-soft);border-radius:8px;padding:10px 11px;min-width:0}
.stat .k{font-family:var(--mono);font-size:9px;letter-spacing:.12em;text-transform:uppercase;color:var(--muted2)}
.stat .v{font-size:16px;font-weight:650;margin-top:3px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;font-variant-numeric:tabular-nums}
.usage{margin:0 0 18px}
.usage-top{display:flex;justify-content:space-between;gap:8px;font-family:var(--mono);font-size:9.5px;letter-spacing:.04em;color:var(--muted2);margin-bottom:6px}
.bar{height:4px;background:var(--panel3);border-radius:99px;overflow:hidden}
.bar i{display:block;height:100%;background:var(--accent);border-radius:99px}
.field-label{font-family:var(--mono);font-size:9px;letter-spacing:.12em;text-transform:uppercase;color:var(--muted2);margin-bottom:6px}
.subrow{display:flex;gap:7px}
.subrow input{flex:1;min-width:0;background:var(--input);border:1px solid var(--line);border-radius:8px;color:var(--text);padding:9px 11px;font:11px var(--mono);outline:none;transition:border-color .15s}
.subrow input:focus{border-color:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)}
.btn{cursor:pointer;border:1px solid var(--line);background:var(--panel2);color:var(--text);border-radius:8px;padding:9px 13px;font-weight:600;font-size:12px;white-space:nowrap;transition:background .15s,border-color .15s,color .15s}
.btn:hover{border-color:var(--line-strong);background:var(--panel3);color:var(--text)}
.btn.primary{background:var(--accent);border-color:var(--accent);color:var(--on-accent)}
.btn.primary:hover{background:var(--accent-hover);border-color:var(--accent-hover);color:var(--on-accent)}
.formats{display:flex;gap:7px;flex-wrap:wrap;margin-top:14px}
.pill{border:1px solid var(--line);border-radius:6px;padding:6px 10px;font-family:var(--mono);font-size:10px;letter-spacing:.02em;color:var(--muted);background:transparent;transition:background .15s,border-color .15s,color .15s}
.pill:hover{color:var(--accent);border-color:var(--accent-line);background:var(--accent-soft)}
.note{margin:16px 0 0;padding-top:14px;border-top:1px solid var(--line-soft);font-size:11px;line-height:1.65;color:var(--muted2)}
.support{display:inline-flex;align-items:center;gap:7px;margin-top:14px;font-size:12px;color:var(--muted)}
.support:hover{color:var(--accent)}
.qr-card .card-body{display:flex;flex-direction:column;align-items:center;text-align:center}
.qr{width:100%;display:flex;justify-content:center;padding:8px 0 4px}
.qr img{width:min(230px,72vw);height:auto;aspect-ratio:1;border-radius:8px;background:#fff;padding:8px;border:1px solid var(--line)}
.qr-fallback{width:230px;height:230px;max-width:72vw;max-height:72vw;display:grid;place-items:center;border:1px solid var(--line);border-radius:8px;background:var(--panel2);color:var(--muted2);font-family:var(--mono);font-size:11px;letter-spacing:.12em}
.hint{color:var(--muted2);font-size:11.5px;margin-top:10px}
.limit{font-family:var(--mono);font-size:10px;letter-spacing:.06em;color:var(--muted);margin-top:8px;padding:6px 10px;border:1px solid var(--line);border-radius:6px}
.servers{margin-top:16px}
.locations{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:10px;padding:16px}
.loc{border:1px solid var(--line-soft);border-radius:10px;background:var(--panel2);overflow:hidden}
.loc-head{padding:11px 13px;border-bottom:1px solid var(--line-soft)}
.loc-name{font-weight:650;font-size:13px;display:flex;align-items:center;gap:8px;min-width:0}
.flag{font-size:16px;line-height:1}
.cfg{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:8px 13px;border-top:1px solid var(--line-soft);font-size:11.5px;color:var(--muted)}
.cfg:first-of-type{border-top:0}
.cfg-name{font-family:var(--mono);font-size:10px;letter-spacing:.06em;color:var(--muted2)}
.copy{cursor:pointer;border:0;background:none;color:var(--accent);font-size:10.5px;padding:3px 0;white-space:nowrap}
.copy:hover{color:var(--accent-hover)}
.empty{padding:28px 16px;text-align:center;color:var(--muted2);font-size:12px}
.footer{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap;padding-top:22px;margin-top:24px;border-top:1px solid var(--line-soft);font-family:var(--mono);font-size:9.5px;letter-spacing:.08em;text-transform:uppercase;color:var(--muted2)}
.toast{position:fixed;left:50%;bottom:28px;transform:translate(-50%,14px);opacity:0;background:var(--panel);border:1px solid var(--accent-line);border-left:2px solid var(--accent);color:var(--text);padding:10px 16px;border-radius:8px;font-size:12px;transition:transform .18s,opacity .18s;pointer-events:none;box-shadow:var(--shadow);z-index:10}
.toast.show{opacity:1;transform:translate(-50%,0)}
@media (max-width:820px){
  .grid{grid-template-columns:1fr}
  .hero{grid-template-columns:1fr;align-items:start}
  .hero-side{text-align:left}
  .locations{grid-template-columns:1fr}
}
@media (max-width:520px){
  .wrap{padding:22px 14px 48px}
  .top{padding-bottom:18px;margin-bottom:26px}
  .stats{gap:6px}
  .stat{padding:8px}
  .stat .v{font-size:13px}
  .top-meta{display:none}
  .formats{gap:6px}
  .pill{font-size:9.5px;padding:5px 8px}
  .footer{font-size:9px}
  .card-body{padding:13px}
  .locations{padding:12px}
}
@media (prefers-reduced-motion: reduce){
  *{transition:none!important}
}
</style>
</head>
<body>
<div class="wrap">
  <header class="top">
    <div class="brand">
      {{if .LogoURL}}<img class="logo" src="{{.LogoURL}}" alt="" onerror="this.style.display='none'">{{else}}<div class="logo logo-mark">M</div>{{end}}
      <div>
        <div class="brand-name">{{.PanelName}}</div>
        <div class="brand-sub">Subscription</div>
      </div>
    </div>
    <div class="top-meta"><span class="dot"></span> 有效订阅</div>
  </header>

  <section class="hero">
    <div>
      <div class="eyebrow">Personal subscription</div>
      <h1>{{.Title}}</h1>
      <p>{{.PageNote}}</p>
    </div>
    <div class="hero-side">
      <div class="hero-label">Account</div>
      <div class="hero-account">{{.Username}}</div>
    </div>
  </section>

  <div class="grid">
    <section class="card">
      <div class="card-head"><h2>连接</h2><span class="card-note">Copy · Import</span></div>
      <div class="card-body">
        <div class="stats">
          <div class="stat"><div class="k">账号</div><div class="v">{{.Username}}</div></div>
          <div class="stat"><div class="k">已用</div><div class="v">{{.UsedLabel}}</div></div>
          <div class="stat"><div class="k">到期</div><div class="v">{{.ExpireLabel}}</div></div>
        </div>
        {{if not .Unlimited}}
        <div class="usage">
          <div class="usage-top"><span>流量进度</span><span>{{.UsedLabel}} / {{.LimitLabel}}</span></div>
          <div class="bar"><i style="width:{{.UsedPercent}}%"></i></div>
        </div>
        {{end}}
        <div class="field-label">订阅地址</div>
        <div class="subrow">
          <input id="sublink" readonly value="{{.SubURL}}">
          <button class="btn primary" data-copy="{{.SubURL}}">复制</button>
        </div>
        <div class="formats">
          <a class="pill" href="{{.ClashURL}}">Clash / Mihomo</a>
          <a class="pill" href="{{.SingboxURL}}">sing-box</a>
          <a class="pill" href="{{.URIURL}}">URI list</a>
        </div>
        {{if .Buttons}}<div class="formats">{{range .Buttons}}<a class="pill" href="{{.URL}}" target="_blank" rel="noopener">{{.Label}}</a>{{end}}</div>{{end}}
        <p class="note">订阅链接会自动同步节点与账号状态。请妥善保管，不要分享给他人。</p>
        {{if .SupportURL}}<a class="support" href="{{.SupportURL}}" target="_blank" rel="noopener">联系支持 <span>→</span></a>{{end}}
      </div>
    </section>

    <section class="card qr-card">
      <div class="card-head"><h2>扫码导入</h2><span class="card-note">QR</span></div>
      <div class="card-body">
        <div class="qr">{{if .QRData}}<img src="{{.QRData}}" alt="Subscription QR code">{{else}}<div class="qr-fallback">QR</div>{{end}}</div>
        <div class="hint">使用客户端扫码导入订阅。</div>
        <div class="limit">{{if .Unlimited}}流量不限制{{else}}流量上限 {{.LimitLabel}}{{end}}</div>
      </div>
    </section>
  </div>

  <section class="card servers">
    <div class="card-head"><h2>节点</h2><span class="card-note">{{len .Locations}} 个节点</span></div>
    {{if .Locations}}
    <div class="locations">
      {{range .Locations}}
      <article class="loc">
        <div class="loc-head"><div class="loc-name">{{if .Flag}}<span class="flag">{{.Flag}}</span>{{end}}{{.Name}}</div></div>
        {{if .Configs}}{{range .Configs}}<div class="cfg"><span class="cfg-name">{{.Name}}</span><button class="copy" data-copy="{{.URI}}">复制链接</button></div>{{end}}{{else}}<div class="cfg"><span class="cfg-name">暂无可用配置</span></div>{{end}}
      </article>
      {{end}}
    </div>
    {{else}}<div class="empty">暂无可用节点。</div>{{end}}
  </section>

  <footer class="footer">
    <span>© {{.Year}} {{.PanelName}}</span>
    <span>安全连接 · 自动更新</span>
  </footer>
</div>
<div class="toast" id="toast">已复制到剪贴板</div>
<script>
(function(){
  var toast=document.getElementById('toast');
  var timer=null;
  function show(){
    toast.classList.add('show');
    clearTimeout(timer);
    timer=setTimeout(function(){toast.classList.remove('show')},1400);
  }
  document.addEventListener('click',function(e){
    var b=e.target.closest('[data-copy]');
    if(!b)return;
    var text=b.getAttribute('data-copy');
    if(navigator.clipboard&&window.isSecureContext){
      navigator.clipboard.writeText(text).then(show);
      return;
    }
    var t=document.createElement('textarea');
    t.value=text;
    t.setAttribute('readonly','');
    t.style.position='fixed';
    t.style.opacity='0';
    document.body.appendChild(t);
    t.select();
    try{document.execCommand('copy');show()}catch(_){}
    t.remove();
  });
})();
</script>
</body>
</html>`

const subPageErrorHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<meta name="color-scheme" content="dark light">
<script>try{var t=localStorage.getItem('mikvace_theme');if(t==='light'||t==='dark')document.documentElement.dataset.theme=t}catch(e){}</script>
<title>{{.Title}} — {{.PanelName}}</title>
<style>
:root{
  color-scheme:dark;
  --bg:#0d0d0c;--panel:#161615;--panel2:#1b1b19;--line:#2d2d28;--line-soft:#232320;--line-strong:#3d3d36;
  --text:#f2efe7;--muted:#a8a49a;--muted2:#77736a;--accent:#ff6b3d;--danger:#ff6f6f;
  --dot:rgba(242,239,231,.055);--shadow:0 24px 64px rgba(0,0,0,.42);
  --font:"Inter","SF Pro Text",ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,"PingFang SC","Noto Sans SC",sans-serif;
  --mono:ui-monospace,"SF Mono","Cascadia Mono","Roboto Mono",Menlo,Consolas,monospace;
}
:root[data-theme="light"]{
  color-scheme:light;
  --bg:#f3efe6;--panel:#fbf9f4;--panel2:#f5f1e8;--line:#d8d0c1;--line-soft:#e6dfd2;--line-strong:#bfb5a2;
  --text:#1b1915;--muted:#5d584e;--muted2:#8b8477;--accent:#d9481f;--danger:#bd352b;
  --dot:rgba(27,25,21,.075);--shadow:0 22px 54px rgba(46,39,27,.12);
}
*{box-sizing:border-box}
body{
  margin:0;min-height:100vh;display:grid;place-items:center;padding:22px;
  background-color:var(--bg);
  background-image:radial-gradient(circle at 1px 1px,var(--dot) 1px,transparent 0);
  background-size:24px 24px;
  color:var(--text);font:14px/1.6 var(--font);-webkit-font-smoothing:antialiased;
}
.card{width:100%;max-width:440px;background:var(--panel);border:1px solid var(--line);border-radius:16px;padding:30px;box-shadow:var(--shadow);text-align:center}
.mark{position:relative;width:44px;height:44px;margin:0 auto 20px;border-radius:12px;border:1px solid var(--line-strong);background:var(--panel2);display:grid;place-items:center;font-family:var(--mono);font-size:18px;font-weight:700;color:var(--accent)}
.mark::after{content:"";position:absolute;top:-3px;right:-3px;width:8px;height:8px;border-radius:2px;background:var(--accent)}
.badge{display:inline-block;margin-bottom:14px;padding:4px 8px;border:1px solid rgba(255,111,111,.28);border-radius:4px;background:rgba(255,111,111,.1);color:var(--danger);font-family:var(--mono);font-size:9.5px;letter-spacing:.12em;text-transform:uppercase}
h1{font-size:22px;line-height:1.2;letter-spacing:-.03em;margin:0 0 8px;font-weight:650}
p{color:var(--muted);margin:0;font-size:12.5px}
.hint{margin-top:16px;padding-top:16px;border-top:1px solid var(--line-soft);font-size:11.5px;color:var(--muted2)}
.support{display:inline-block;margin-top:14px;font-size:12px;color:var(--accent)}
.foot{margin-top:24px;font-family:var(--mono);font-size:9px;letter-spacing:.1em;text-transform:uppercase;color:var(--muted2)}
</style>
</head>
<body>
  <div class="card">
    <div class="mark">!</div>
    <div class="badge">Unavailable</div>
    <h1>{{.Title}}</h1>
    <p>{{.Error}}</p>
    <div class="hint">如有疑问，请联系面板管理员。</div>
    {{if .SupportURL}}<a class="support" href="{{.SupportURL}}" target="_blank" rel="noopener">联系支持 →</a>{{end}}
    <div class="foot">{{.PanelName}}{{if .Year}} · {{.Year}}{{end}}</div>
  </div>
</body>
</html>`
