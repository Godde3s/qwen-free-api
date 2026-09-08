<div dir="rtl">

# 🦞 Qwen-Free-API

**پل رایگان و مستقیم به Qwen3.8-Max** — فلگ‌شیپ علی‌بابا — بدون کلید API رسمی، بدون مرورگر، فقط HTTP.
هر کلاینتی که با OpenAI یا Anthropic حرف می‌زند (Hermes، Cursor، Cline، Chatbox، LobeChat، …) مستقیم به این بریج وصل می‌شود.

> 🏗️ معماریِ برنده‌ی [GLM-Free-API](https://github.com/Godde3s/glm-free-api) — حالا برای chat.qwen.ai

---

## ✨ چرا این پروژه

| قابلیت | توضیح |
|---|---|
| 🚀 **تک‌فایل، تک‌دستور** | یک باینری، `./start.sh` — تمام. داشبورد فارسی داخل همان باینری embed شده |
| 🔌 **دو پروتکل کامل** | `/v1/chat/completions` (OpenAI) + `/v1/messages` (Anthropic) با استریم SSE |
| 🧠 **تفکر زنده** | `reasoning_content` مدل‌های thinking مستقیم استریم می‌شود (قابل خاموش‌کردن) |
| 🛠️ **Tool Calling واقعی** | با `AGENT_MODE=true` ابزارهای OpenAI/Anthropic به prompt ترجمه و جواب‌ها به `tool_calls`/`tool_use` برمی‌گردند — برای Hermes و هر ایجنت دیگر |
| 👥 **چند-اکانت واقعی** | `QWEN_TOKENS=tok1,tok2,tok3` → round-robin + cooldown نمایی روی RateLimit + failover شفاف قبل از اولین بایت |
| 🎛️ **داشبورد فارسی RTL** | وضعیت زنده اکانت‌ها، کپی اندپوینت‌ها، اسنیپت آماده، پلی‌گراند استریم — روی `/` |
| 🪶 **سبک** | یک باینری ~12MB، بدون CDN، بدون دیتابیس، بدون وابستگی runtime |
| 🐳 **Docker + CI** | `docker compose up` یا باینری آماده ۵ پلتفرم از Releases |

## 📦 مدل‌ها

| مدل | توانایی‌ها |
|---|---|
| **`qwen3.8-max`** ⭐ | فلگ‌شیپ — کدنویسی/ایجنت/استدلال، vision، thinking، ۱M کانتکست |
| `qwen3.7-plus` | سریع و چندوجهی (vision/thinking/search) |
| `qwen3.7-max` | نسل قبل فلگ‌شیپ |
| `qwen3.6-plus`, `qwen3.5-plus` | مدل‌های بالانس قدیمی‌تر |

لیست مدل‌ها **زنده** از `/api/models` آپستریم می‌آید (`GET /v1/models`).

---

## 🚀 راه‌اندازی در ۶۰ ثانیه

### ۱. توکن بگیر (۱ بار، ۳۰ ثانیه)

1. وارد [chat.qwen.ai](https://chat.qwen.ai) شو (ایمیل/پسورد یا Google/GitHub)
2. `F12` → تب **Application** → **Cookies** → `https://chat.qwen.ai` → مقدار کوکی **`token`** را کپی کن
   (یا در Console: `document.cookie.split('; ').find(c=>c.startsWith('token=')).slice(6)`)
3. یا اصلاً از ابزار خودمان استفاده کن: `go run ./cmd/qwen-login` — مرورگر باز می‌شود، خودت لاگین می‌کنی، توکن خودش ذخیره می‌شود

### ۲. اجرا

```bash
cp .env.example .env
nano .env                # QWEN_TOKENS=توکن_تو را بگذار
chmod +x start.sh
./start.sh
```

همین. داشبورد: **http://localhost:8080**

<details dir="ltr">
<summary><b>English — Quick Start</b></summary>

```bash
git clone https://github.com/Godde3s/qwen-free-api && cd qwen-free-api
cp .env.example .env && nano .env          # paste your chat.qwen.ai cookie token
./start.sh                                  # or: docker compose up -d
```

Get the token: log in at chat.qwen.ai → F12 → Application → Cookies → copy `token`.
Multi-account: `QWEN_TOKENS=token1,token2,token3` — the pool round-robins and fails over automatically.

</details>

---

## 🎡 حالت مهمان (بدون توکن)

بریک بدون توکن هم بالا می‌آید (هدرهای Baxia سنتزشده مثل کلاینت وب).
ناپایدار است: روی IP خانگی معمولاً کار می‌کند، روی سرور/دیتاسنتر ممکن است کپچا یا ۴۰۱ ببینید — آپستریم هر وقت سخت‌گیرتر می‌شود همین‌جا خودش را نشان می‌دهد.
برای قدرت کامل و بدون دردسر: **یک توکن بگذار.** اکانت فری = همه مدل‌ها + سقف روزانه‌ی سخاوتمندانه.

---

## 🔌 اتصال برنامه‌ها

**Base URL:** `http://localhost:8080/v1` — **API Key:** مقدار `AUTH_TOKEN` (پیش‌فرض `qwen`)

<details dir="ltr">
<summary>Python (OpenAI SDK)</summary>

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="qwen",
)

resp = client.chat.completions.create(
    model="qwen3.8-max",
    messages=[{"role": "user", "content": "سلام! خودت رو معرفی کن"}],
    stream=True,
)
for chunk in resp:
    delta = chunk.choices[0].delta
    if delta.content:
        print(delta.content, end="", flush=True)
```

</details>

<details dir="ltr">
<summary>Anthropic-compatible (Claude SDK / Claude Code)</summary>

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_API_KEY=qwen
```

Endpoint: `POST /v1/messages` — با `thinking` blocks و `tool_use` کامل.

</details>

<details dir="ltr">
<summary>Hermes / Cursor / Cline (Tool Calling)</summary>

```env
OPENAI_BASE_URL=http://localhost:8080/v1
OPENAI_API_KEY=qwen
MODEL=qwen3.8-max
```

سپس در `.env` بریج:

```env
AGENT_MODE=true
```

ابزارها به یک پرامپت XML-بخش‌بندی‌شده ترجمه می‌شوند، مدل بلوک‌های `<<<TOOL_CALL>>>` می‌سازد و بریج آن‌ها را به `tool_calls` استاندارد (استریم دلتا + `finish_reason=tool_calls`) برمی‌گرداند — دقیقاً همان چیزی که ایجنت‌ها انتظار دارند.

</details>

### cURL

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer qwen" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.8-max","messages":[{"role":"user","content":"سلام"}],"stream":true}'
```

---

## ⚙️ تنظیمات (.env)

| متغیر | پیش‌فرض | توضیح |
|---|---|---|
| `QWEN_TOKENS` | — | توکن‌ها با `,` جدا شوند؛ pool خودکار می‌چرخد |
| `PORT` / `HOST` | `8080` / `0.0.0.0` | آدرس سرور |
| `AUTH_TOKEN` | `qwen` | کلاینت‌ها با `Bearer` یا `x-api-key` |
| `AGENT_MODE` | `false` | ترجمه ابزارها برای ایجنت‌ها |
| `ACCOUNT_COOLDOWN_BASE` | `120` | اولین cooldown روی RateLimit (ثانیه، نمایی ×۲ تا سقف ۳۰ دقیقه) |
| `ACCOUNT_QUEUE_TIMEOUT` | `120` | صف انتظار وقتی همه اکانت‌ها لیمیت‌اند |

### اندپوینت‌ها

| مسیر | توضیح |
|---|---|
| `GET /` | داشبورد فارسی |
| `GET /health`, `/status` | سلامت + وضعیت اکانت‌ها |
| `GET /v1/models` | لیست زنده مدل‌ها |
| `POST /v1/chat/completions` | OpenAI (stream + tools) |
| `POST /v1/messages` | Anthropic (thinking + tool_use) |

---

## 🛠️ عیب‌یابی

| خطا | معنی و راه‌حل |
|---|---|
| `RateLimited` | سقف روزانه اکانت پر شده → توکن دوم اضافه کن یا فردا تلاش کن |
| `unauthorized / session has expired` | توکن منقضی → توکن جدید از کوکی بگیر |
| `RGV587 / risk-control` | کپچای علی‌بابا (مهمان روی IP دیتاسنتر) → توکن بگذار یا از شبکه خانگی اجرا کن |
| `403 forbidden` | مدل برای سطح اکانت باز نیست → `qwen3.8-max` یا `qwen3.7-plus` امتحان کن |

لاگ‌ها فارسی و قابل‌فهم‌اند؛ هر خطا راه‌حل خودش را هم می‌گوید.

---

## 🔒 امنیت و نکات

- این پروژه **برای استفاده شخصی و محلی** است؛ اشتراک‌گذاری عمومی توکن/سرور ریسک بن اکانت دارد.
- توکن‌ها را commit نکن (`.env` و `qwen-tokens.json` در `.gitignore` هستند).
- این یک پروژه‌ی غیررسمی است و وابسته به علی‌بابا نیست؛ مسئولیت استفاده با کاربر است.

## 📜 لایسنس

MIT — بر پایه‌ی معماری [GLM-Free-API](https://github.com/Godde3s/glm-free-api)

<div dir="ltr">

## 🇬🇧 English Summary

Unofficial OpenAI & Anthropic-compatible bridge for **chat.qwen.ai** (Qwen3.8-Max & family).
Single Go binary with an embedded RTL dashboard, live model discovery, streaming (incl. `reasoning_content`),
prompt-shim **tool calling** (`AGENT_MODE=true`), and a resilient **multi-account token pool**
(round-robin + exponential 429 cooldown + pre-stream failover). Guest mode is best-effort (upstream risk-control varies); a free-account cookie token is the reliable path. Local/personal use intended.

- **Quick start:** `cp .env.example .env` → paste your `token` cookie from chat.qwen.ai → `./start.sh`
- **Endpoints:** `/v1/chat/completions`, `/v1/messages`, `/v1/models`, `/health`
- **Login helper:** `go run ./cmd/qwen-login`

</div>
</div>
