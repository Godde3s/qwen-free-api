// The dashboard — a small Persian/RTL control panel served at "/" (and it is
// the ONLY page in the binary: no CDN, no assets, no JS framework — a single
// self-contained HTML string compiled into the bridge, GhostBrain-style).
//
// What it shows (all data fetched live by the page itself):
//   - server status pill (live /health polling, 4s)
//   - multi-account cards: healthy / cooling-down (with countdown) / dead,
//     per-account requests, 429 hits, last error
//   - session pool state
//   - "connect" panel: base URL + auth key + endpoint list with copy buttons
//   - ready-made snippets (curl / Python / Node / agent tools) pre-filled
//     with the live base URL and key
//   - a mini streaming playground: pick a model, send a prompt, watch the
//     SSE stream — proves the bridge works end-to-end in one click
//
// The page embeds AUTH_TOKEN so the playground/models fetch work without
// typing anything. This is fine for the intended LOCAL use; if you expose
// the bridge publicly, you already changed AUTH_TOKEN (README security note).

package qbridge

import (
    "fmt"
    "net/http"
    "strings"
)

const dashboardHTML = `<!DOCTYPE html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Qwen-Free-API — داشبورد</title>
<style>
:root{--bg:#0b0f14;--card:#121826;--card2:#0e1420;--line:#1f2a3d;--txt:#e6edf3;--dim:#8b98a9;
--ok:#10b981;--warn:#f59e0b;--bad:#ef4444;--acc:#3b82f6;--acc2:#8b5cf6}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--txt);font-family:Vazirmatn,system-ui,'Segoe UI',Tahoma,sans-serif;
min-height:100vh;display:flex;flex-direction:column;align-items:center;padding:24px 16px 60px}
.wrap{width:100%;max-width:980px}
header{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:12px;margin-bottom:22px}
.logo{display:flex;align-items:center;gap:12px}
.logo .dot{width:44px;height:44px;border-radius:14px;display:grid;place-items:center;font-size:22px;
background:linear-gradient(135deg,var(--acc),var(--acc2))}
h1{font-size:1.25rem}
h1 small{display:block;font-weight:400;color:var(--dim);font-size:.75rem;margin-top:3px}
.pill{display:inline-flex;align-items:center;gap:8px;background:var(--card);border:1px solid var(--line);
padding:8px 14px;border-radius:999px;font-size:.85rem}
.pill b{font-weight:600}
.led{width:10px;height:10px;border-radius:50%;background:var(--bad);box-shadow:0 0 8px var(--bad);animation:pulse 1.6s infinite}
.led.ok{background:var(--ok);box-shadow:0 0 8px var(--ok)}
.led.warn{background:var(--warn);box-shadow:0 0 8px var(--warn)}
@keyframes pulse{50%{opacity:.45}}
.grid{display:grid;gap:14px}
.g2{grid-template-columns:repeat(auto-fit,minmax(300px,1fr))}
.card{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:18px}
.card h2{font-size:.95rem;margin-bottom:12px;display:flex;align-items:center;gap:8px}
.card h2 .n{color:var(--dim);font-weight:400;font-size:.75rem;margin-inline-start:auto}
.acct{background:var(--card2);border:1px solid var(--line);border-radius:12px;padding:12px 14px;margin-bottom:10px;
display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.acct:last-child{margin-bottom:0}
.av{width:38px;height:38px;border-radius:10px;display:grid;place-items:center;font-weight:700;font-size:.9rem;
background:var(--line);flex:none}
.acct .who{min-width:120px;flex:1}
.acct .who b{font-size:.88rem;display:block}
.acct .who span{color:var(--dim);font-size:.72rem}
.stat{font-size:.72rem;padding:4px 10px;border-radius:999px;border:1px solid}
.stat.ok{color:var(--ok);border-color:var(--ok);background:rgba(16,185,129,.08)}
.stat.warn{color:var(--warn);border-color:var(--warn);background:rgba(245,158,11,.08)}
.stat.bad{color:var(--bad);border-color:var(--bad);background:rgba(239,68,68,.08)}
.meter{color:var(--dim);font-size:.72rem;text-align:left;line-height:1.7;min-width:86px}
.meter b{color:var(--txt);font-weight:600}
.conn{display:flex;flex-direction:column;gap:10px}
.kv{display:flex;align-items:center;gap:10px;background:var(--card2);border:1px solid var(--line);
border-radius:10px;padding:10px 12px}
.kv label{color:var(--dim);font-size:.72rem;min-width:64px}
.kv code{flex:1;font-family:ui-monospace,Consolas,monospace;font-size:.8rem;color:var(--acc);
direction:ltr;text-align:left;overflow-x:auto;white-space:nowrap}
.copy{border:1px solid var(--line);background:var(--card);color:var(--dim);border-radius:8px;
padding:5px 12px;cursor:pointer;font-size:.72rem;transition:.2s}
.copy:hover{color:var(--txt);border-color:var(--acc)}
.tabs{display:flex;gap:6px;margin-bottom:10px;flex-wrap:wrap}
.tab{border:1px solid var(--line);background:transparent;color:var(--dim);border-radius:8px;
padding:6px 14px;cursor:pointer;font-size:.78rem;font-family:inherit}
.tab.on{background:var(--acc);border-color:var(--acc);color:#fff}
pre{background:#05080d;border:1px solid var(--line);border-radius:12px;padding:14px;overflow-x:auto;
font-family:ui-monospace,Consolas,monospace;font-size:.75rem;line-height:1.8;direction:ltr;text-align:left;color:#a5d6ff}
select,textarea,button.primary{font-family:inherit;font-size:.85rem;border-radius:10px;outline:none}
select,textarea{width:100%;background:var(--card2);border:1px solid var(--line);color:var(--txt);padding:10px 12px}
select{margin-bottom:10px}
textarea{min-height:88px;resize:vertical;line-height:1.8}
button.primary{width:100%;border:none;background:linear-gradient(135deg,var(--acc),var(--acc2));
color:#fff;font-weight:700;padding:12px;cursor:pointer;margin-top:10px;transition:.2s}
button.primary:hover{filter:brightness(1.15)}
button.primary:disabled{opacity:.5;cursor:wait}
.out{background:#05080d;border:1px solid var(--line);border-radius:12px;padding:14px;margin-top:12px;
min-height:70px;max-height:320px;overflow-y:auto;font-size:.85rem;line-height:2;white-space:pre-wrap;color:var(--txt)}
.out:empty::before{content:'پاسخ مدل اینجا استریم می‌شود…';color:var(--dim)}
footer{margin-top:26px;color:var(--dim);font-size:.72rem;text-align:center;line-height:2}
a{color:var(--acc);text-decoration:none}
.hidden{display:none}
.err{color:var(--bad);font-size:.75rem;margin-top:8px}
</style>
</head>
<body>
<div class="wrap">
<header>
  <div class="logo"><div class="dot">🦞</div>
    <h1>Qwen-Free-API<small>پل رایگان به Qwen3.8-Max و خانواده Qwen — بدون مرورگر، فقط HTTP</small></h1>
  </div>
  <div class="pill"><span class="led" id="led"></span><span id="statustxt">در حال اتصال…</span></div>
</header>

<div class="grid g2">

<section class="card">
  <h2>👤 اکانت‌ها <span class="n" id="poolinfo"></span></h2>
  <div id="accounts"></div>
</section>

<section class="card">
  <h2>🔌 اتصال برنامه‌ها</h2>
  <div class="conn">
    <div class="kv"><label>Base URL</label><code id="baseurl"></code><button class="copy" data-copy="baseurl">کپی</button></div>
    <div class="kv"><label>API Key</label><code id="apikey"></code><button class="copy" data-copy="apikey">کپی</button></div>
    <div class="kv"><label>OpenAI</label><code id="ep-oai"></code><button class="copy" data-copy="ep-oai">کپی</button></div>
    <div class="kv"><label>Anthropic</label><code id="ep-ant"></code><button class="copy" data-copy="ep-ant">کپی</button></div>
  </div>
</section>

<section class="card">
  <h2>🧪 تست زنده (استریم واقعی)</h2>
  <select id="model"></select>
  <textarea id="prompt" placeholder="هر چیزی بپرس… مثلا: خودت رو معرفی کن">سلام! خودت رو در دو جمله معرفی کن.</textarea>
  <button class="primary" id="send">▶ ارسال</button>
  <div class="err hidden" id="perr"></div>
  <div class="out" id="out"></div>
</section>

<section class="card">
  <h2>📋 کد آماده</h2>
  <div class="tabs" id="tabs">
    <button class="tab on" data-t="curl">curl</button>
    <button class="tab" data-t="py">Python</button>
    <button class="tab" data-t="js">Node.js</button>
    <button class="tab" data-t="agent">ایجنت (Cursor/Cline)</button>
  </div>
  <pre id="code"></pre>
</section>

</div>

<footer>
  وضعیت زنده: <a href="/status" target="_blank">/status</a> · سلامت: <a href="/health" target="_blank">/health</a>
  · مستندات کامل: <a href="https://github.com/Godde3s/qwen-free-api" target="_blank">GitHub</a><br>
  ساخت <b>Godde3s</b> — بر پایه GLM-Free-API (MIT) · تا کامیت آخر، نگه‌داری می‌شود 💜
</footer>
</div>

<script>
const KEY = "__AUTH_TOKEN__";
const ORIGIN = location.origin;
$("baseurl").textContent = ORIGIN + "/v1";
$("apikey").textContent  = KEY;
$("ep-oai").textContent  = ORIGIN + "/v1/chat/completions";
$("ep-ant").textContent  = ORIGIN + "/v1/messages";

function $(id){return document.getElementById(id)}
function esc(s){return String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}

// ── copy buttons ────────────────────────────────────────────────────────────
document.querySelectorAll(".copy").forEach(b=>b.onclick=()=>{
  navigator.clipboard.writeText($(b.dataset.copy).textContent).then(()=>{
    const old=b.textContent; b.textContent="✔ کپی شد";
    setTimeout(()=>b.textContent=old,1200);
  });
});

// ── live status ─────────────────────────────────────────────────────────────
async function refresh(){
  try{
    const h=await (await fetch("/health")).json();
    const led=$("led"), st=$("statustxt");
    led.className="led"+(h.healthy?" ok":"");
    st.innerHTML=h.healthy?"فعال و سالم <b>●</b>":"در حال راه‌اندازی…";
    const s=await (await fetch("/status")).json();
    renderAccounts(s);
  }catch(e){ $("statustxt").textContent="سرور پاسخ نمی‌دهد"; }
}
function renderAccounts(s){
  const pool=s.accountPool||{enabled:false};
  const box=$("accounts");
  $("poolinfo").textContent=pool.enabled?(pool.healthy+"/"+pool.size+" سالم"):"";
  if(!pool.enabled||!s.accounts||!s.accounts.length){
    box.innerHTML='<div class="acct"><div class="av">👻</div><div class="who"><b>حالت مهمان</b>'+
      '<span>بدون توکن — از IP خانگی معمولا کار می‌کند؛ برای قدرت کامل توکن اکانت را در QWEN_TOKENS بگذار</span></div>'+
      '<span class="stat warn">مهمان</span></div>';
    return;
  }
  box.innerHTML=s.accounts.map(a=>{
    const st=a.dead?'<span class="stat bad">مُرد</span>'
      :a.healthy?'<span class="stat ok">سالم</span>'
      :'<span class="stat warn">استراحت '+esc(a.cooldown||"")+'</span>';
    const tip=a.lastError?(' title="'+esc(a.lastError)+'"'):"";
    return '<div class="acct"'+tip+'><div class="av">'+esc((a.userName||"?").slice(0,2))+'</div>'+
      '<div class="who"><b>#'+a.id+" "+esc(a.userName||"")+'</b><span>اکانت شماره '+a.id+'</span></div>'+st+
      '<div class="meter"><b>'+a.requests+'</b> درخواست · <b>'+a.rateLimited+'</b> لیمیت</div></div>';
  }).join("");
}
refresh(); setInterval(refresh,4000);

// ── models + playground ─────────────────────────────────────────────────────
async function loadModels(){
  try{
    const r=await fetch(ORIGIN+"/v1/models",{headers:{Authorization:"Bearer "+KEY}});
    const j=await r.json();
    const ids=(j.data||[]).map(m=>m.id).filter(Boolean);
    const prefer=["qwen3.8-max","qwen3.7-plus","qwen3.7-max"];
    const first=prefer.find(p=>ids.includes(p))||ids[0]||"qwen3.8-max";
    const list=ids.length?ids:[first];
    $("model").innerHTML=list.map(id=>"<option>"+esc(id)+"</option>").join("");
    $("model").value=first;
  }catch(e){ $("model").innerHTML="<option>qwen3.8-max</option>"; }
}
loadModels();

$("send").onclick=async()=>{
  const btn=$("send"),out=$("out"),err=$("perr");
  btn.disabled=true; btn.textContent="… در حال دریافت";
  out.textContent=""; err.classList.add("hidden");
  try{
    const r=await fetch(ORIGIN+"/v1/chat/completions",{
      method:"POST",
      headers:{Authorization:"Bearer "+KEY,"Content-Type":"application/json"},
      body:JSON.stringify({model:$("model").value,messages:[{role:"user",content:$("prompt").value}],stream:true})
    });
    if(!r.ok){
      const t=await r.text();
      try{ err.textContent="خطا "+r.status+": "+(JSON.parse(t).error?.message||t).slice(0,300); }
      catch(_){ err.textContent="خطا "+r.status+": "+t.slice(0,300); }
      err.classList.remove("hidden"); return;
    }
    const reader=r.body.getReader(), dec=new TextDecoder();
    let buf="";
    while(true){
      const {done,value}=await reader.read();
      if(done) break;
      buf+=dec.decode(value,{stream:true});
      for(const line of buf.split("\n").slice(0,-1)){
        const m=line.trim();
        if(!m.startsWith("data:")) continue;
        const payload=m.slice(5).trim();
        if(payload==="[DONE]") continue;
        try{
          const j=JSON.parse(payload);
          const delta=j.choices?.[0]?.delta?.content;
          if(delta){ out.textContent+=delta; out.scrollTop=out.scrollHeight; }
        }catch(_){}
      }
      buf=buf.split("\n").pop();
    }
  }catch(e){ err.textContent="ارتباط برقرار نشد: "+e.message; err.classList.remove("hidden"); }
  finally{ btn.disabled=false; btn.textContent="▶ ارسال"; }
};

// ── snippet tabs ────────────────────────────────────────────────────────────
const SNIPPETS={
curl:'# OpenAI-compatible\n'+unescape("curl%20"+ORIGIN+"/v1/chat/completions%20%5C%0A%20%20-H%20%22Authorization%3A%20Bearer%20"+KEY+"%22%20%5C%0A%20%20-H%20%22Content-Type%3A%20application/json%22%20%5C%0A%20%20-d%20'%7B%0A%20%20%20%20%22model%22%3A%20%22qwen3.8-max%22%2C%0A%20%20%20%20%22messages%22%3A%20%5B%7B%22role%22%3A%22user%22%2C%22content%22%3A%22%D8%B3%D9%84%D8%A7%D9%85%22%7D%5D%2C%0A%20%20%20%20%22stream%22%3A%20true%0A%20%20%7D'"),
py:"from openai import OpenAI\n\nclient = OpenAI(\n    base_url = \""+ORIGIN+"/v1\",\n    api_key  = \""+KEY+"\",\n)\n\nresp = client.chat.completions.create(\n    model=\"qwen3.8-max\",\n    messages=[{\"role\":\"user\",\"content\":\"سلام\"}],\n)\nprint(resp.choices[0].message.content)",
js:"import OpenAI from \"openai\";\n\nconst client = new OpenAI({\n  baseURL: \""+ORIGIN+"/v1\",\n  apiKey:  \""+KEY+"\",\n});\n\nconst stream = await client.chat.completions.create({\n  model: \"qwen3.8-max\",\n  messages: [{ role: \"user\", content: \"سلام\" }],\n  stream: true,\n});\nfor await (const chunk of stream)\n  process.stdout.write(chunk.choices[0]?.delta?.content ?? \"\");",
agent:"// Cursor / Cline / any OpenAI-compatible agent\n// 1) Base URL : "+ORIGIN+"/v1\n// 2) API Key  : "+KEY+"\n// 3) Model    : qwen3.8-max  (or qwen3.7-plus for speed)\n//\n// For tool-calling agents set AGENT_MODE=1 in .env and restart,\n// then enable the OpenAI provider inside your agent.\n//\n// Anthropic-compatible agents:\n//   ANTHROPIC_BASE_URL="+ORIGIN+"\n//   ANTHROPIC_API_KEY="+KEY
};
document.querySelectorAll("#tabs .tab").forEach(t=>t.onclick=()=>{
  document.querySelectorAll("#tabs .tab").forEach(x=>x.classList.remove("on"));
  t.classList.add("on"); $("code").textContent=SNIPPETS[t.dataset.t];
});
$("code").textContent=SNIPPETS.curl;
</script>
</body>
</html>`

// serveDashboard renders the control panel. The only server-side injection
// is the client auth key, so the playground and model picker work with zero
// configuration (local-first: the same trade-off GhostBrain makes).
func serveDashboard(w http.ResponseWriter, r *http.Request) {
    page := strings.ReplaceAll(dashboardHTML, "__AUTH_TOKEN__", config.Auth.Token)
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Header().Set("Cache-Control", "no-store")
    _, _ = fmt.Fprint(w, page)
}
