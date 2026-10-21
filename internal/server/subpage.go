package server

const subPageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>{{.Title}} — {{.PanelName}}</title>
<style>
:root{color-scheme:dark;--bg:#070b14;--card:#0e1524;--card2:#121b2d;--line:#22304a;--text:#e6eefb;--muted:#8ea2c0;--accent:#38bdf8;--accent2:#818cf8;--ok:#34d399}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;background:radial-gradient(1200px 600px at 10% -10%,#123 0%,transparent 55%),radial-gradient(900px 500px at 100% 0%,#1b1440 0%,transparent 50%),var(--bg);color:var(--text);font:15px/1.55 ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,Arial,sans-serif}
a{color:var(--accent);text-decoration:none}
.wrap{max-width:1000px;margin:0 auto;padding:28px 18px 60px}
.hero{display:flex;align-items:center;gap:16px;margin-bottom:22px}
.logo{width:52px;height:52px;border-radius:16px;object-fit:cover;background:linear-gradient(135deg,var(--accent),var(--accent2));display:flex;align-items:center;justify-content:center;font-size:24px;font-weight:800;color:#03101d;flex:none;box-shadow:0 10px 30px #38bdf833}
h1{margin:0;font-size:22px;letter-spacing:-.3px}
.sub{color:var(--muted);font-size:13px;margin-top:3px}
.grid{display:grid;grid-template-columns:1.15fr .85fr;gap:18px}
@media(max-width:760px){.grid{grid-template-columns:1fr}}
.card{background:linear-gradient(180deg,#101827cc,#0b1220cc);border:1px solid var(--line);border-radius:20px;padding:20px;box-shadow:0 18px 50px #0006}
.card h2{margin:0 0 14px;font-size:12px;letter-spacing:1.6px;text-transform:uppercase;color:var(--muted);font-weight:700}
.qr{display:flex;justify-content:center;padding:10px}
.qr img{width:230px;height:230px;border-radius:14px;background:#fff;padding:8px}
.hint{text-align:center;color:var(--muted);font-size:12px;margin-top:8px}
.stats{display:grid;grid-template-columns:repeat(3,1fr);gap:10px;margin-bottom:14px}
.stat{background:var(--card2);border:1px solid var(--line);border-radius:14px;padding:12px}
.stat .k{font-size:10px;text-transform:uppercase;letter-spacing:1.2px;color:var(--muted)}
.stat .v{font-size:17px;font-weight:700;margin-top:4px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.linkbox{display:flex;gap:8px;margin-top:6px}
.linkbox input{flex:1;min-width:0;background:#070d18;border:1px solid var(--line);border-radius:10px;color:var(--text);padding:10px 12px;font:12px ui-monospace,SFMono-Regular,Menlo,monospace}
.btn{cursor:pointer;border:1px solid #2c4a6b;background:linear-gradient(180deg,#17324d,#10253a);color:#dff2ff;border-radius:10px;padding:10px 14px;font-weight:600;font-size:12px;white-space:nowrap}
.btn:hover{border-color:var(--accent)}
.btn.primary{background:linear-gradient(135deg,var(--accent),var(--accent2));color:#04101e;border:0}
.formats{display:flex;gap:8px;flex-wrap:wrap;margin-top:12px}
.pill{border:1px solid var(--line);border-radius:999px;padding:6px 11px;font-size:11px;color:var(--muted)}
.pill:hover{color:var(--text);border-color:var(--accent)}
.locations{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:12px;margin-top:14px}
.loc{background:var(--card2);border:1px solid var(--line);border-radius:15px;padding:14px}
.loc .name{font-weight:700;font-size:14px;margin-bottom:10px}
.cfg{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:7px 0;border-top:1px dashed #ffffff10;font-size:12px;color:var(--muted)}
.cfg:first-of-type{border-top:0}
.cfg .copy{font-size:11px;color:var(--accent);cursor:pointer;background:none;border:0;padding:4px}
.footer{text-align:center;color:#5f7292;font-size:11px;margin-top:26px}
.support{display:block;text-align:center;margin-top:18px}
.toast{position:fixed;left:50%;bottom:28px;transform:translate(-50%,20px);opacity:0;background:#0f2a22;border:1px solid #1f6f57;color:#b8f5dd;padding:9px 16px;border-radius:10px;font-size:12px;transition:.2s;pointer-events:none}
.toast.show{opacity:1;transform:translate(-50%,0)}
</style>
</head>
<body>
<div class="wrap">
  <div class="hero">
    {{if .LogoURL}}<img class="logo" src="{{.LogoURL}}" alt="logo" onerror="this.style.display='none'">{{else}}<div class="logo">M</div>{{end}}
    <div><h1>{{.Title}}</h1><div class="sub">{{.PageNote}}</div></div>
  </div>
  <div class="grid">
    <div class="card">
      <h2>连接信息</h2>
      <div class="stats">
        <div class="stat"><div class="k">账号</div><div class="v">{{.Username}}</div></div>
        <div class="stat"><div class="k">已用</div><div class="v">{{.UsedLabel}}</div></div>
        <div class="stat"><div class="k">到期</div><div class="v">{{.ExpireLabel}}</div></div>
      </div>
      <div class="linkbox">
        <input id="sublink" readonly value="{{.SubURL}}">
        <button class="btn primary" data-copy="{{.SubURL}}">复制</button>
      </div>
      <div class="formats">
        <a class="pill" href="{{.ClashURL}}">Clash / Mihomo</a>
        <a class="pill" href="{{.SingboxURL}}">sing-box</a>
        <a class="pill" href="{{.URIURL}}">URI list</a>
      </div>
      {{if .Buttons}}<div class="formats">{{range .Buttons}}<a class="pill" href="{{.URL}}" target="_blank" rel="noopener">{{.Label}}</a>{{end}}</div>{{end}}
      {{if .SupportURL}}<a class="support" href="{{.SupportURL}}" target="_blank" rel="noopener">联系支持</a>{{end}}
    </div>
    <div class="card">
      <h2>二维码</h2>
      <div class="qr">{{if .QRData}}<img src="{{.QRData}}" alt="Subscription QR code">{{end}}</div>
      <div class="hint">使用客户端扫码导入订阅。</div>
      <div class="hint">流量上限：{{if .Unlimited}}不限制{{else}}{{.LimitLabel}}{{end}}</div>
    </div>
  </div>
  {{if .Locations}}
  <div class="card" style="margin-top:18px">
    <h2>服务器</h2>
    <div class="locations">
      {{range .Locations}}
      <div class="loc">
        <div class="name">{{if .Flag}}{{.Flag}} {{end}}{{.Name}}</div>
        {{range .Configs}}<div class="cfg"><span>{{.Name}}</span><button class="copy" data-copy="{{.URI}}">复制链接</button></div>{{end}}
      </div>
      {{end}}
    </div>
  </div>
  {{end}}
  <div class="footer">{{.PanelName}} · 安全连接</div>
</div>
<div class="toast" id="toast">已复制</div>
<script>
(function(){
  var toast=document.getElementById('toast');
  function show(){toast.classList.add('show');setTimeout(function(){toast.classList.remove('show')},1200)}
  document.addEventListener('click',function(e){
    var b=e.target.closest('[data-copy]'); if(!b)return;
    var text=b.getAttribute('data-copy');
    if(navigator.clipboard&&window.isSecureContext){navigator.clipboard.writeText(text).then(show)}
    else{var t=document.createElement('textarea');t.value=text;document.body.appendChild(t);t.select();try{document.execCommand('copy');show()}catch(_){}t.remove()}
  });
})();
</script>
</body>
</html>`

const subPageErrorHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>订阅不可用</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:radial-gradient(800px 400px at 50% 0%,#1b1440,#070b14);color:#e6eefb;font:15px/1.6 system-ui,sans-serif;padding:20px}
.card{max-width:440px;text-align:center;background:#0e1524;border:1px solid #22304a;border-radius:20px;padding:32px;box-shadow:0 20px 60px #0008}
h1{font-size:20px;margin:0 0 8px}p{color:#8ea2c0;margin:0}.badge{display:inline-block;margin-bottom:16px;background:#3a1520;border:1px solid #7f2740;color:#ffb3c7;padding:5px 12px;border-radius:999px;font-size:11px;letter-spacing:1px;text-transform:uppercase}</style>
</head><body><div class="card"><div class="badge">不可用</div><h1>{{.Title}}</h1><p>{{.Error}}</p><p style="margin-top:14px;font-size:12px">如有疑问请联系管理员。</p></div></body></html>`
