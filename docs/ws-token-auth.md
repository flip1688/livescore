# WS Token Auth — ระบบตั๋วเข้า WebSocket

> ทำเสร็จ + enforce บน production 2026-07-10 · คู่กับ frontend repo `flip1688/livescore-web`
> (ฉบับเดียวกันเก็บใน Notion: "🔒 WS Token Auth — livescore realtime")

## ทำไมต้องมี

- ข้อมูล realtime (สกอร์สด/เหตุการณ์/สถิติ) คือของแพงที่ sync มาจาก thscore — ก่อนหน้านี้ใครก็ต่อ `wss://api.lsm-allsports.info/ws` มาดูดไปใช้ฟรีได้
- **TLS (`wss://`) เข้ารหัส transport อยู่แล้ว** — ปัญหาไม่ใช่การดักฟัง แต่คือการ freeload
- การเข้ารหัส payload ไม่ตอบโจทย์: เว็บ public ต้องให้ browser ถอดได้ = กุญแจอยู่ใน JS = แกะได้เสมอ
- มาตรการจริง: **ตั๋วอายุสั้น + จำกัดคิว** — คนดูดต้อง automate ขอตั๋วจากเว็บเรา (หลัง Cloudflare) ทุก 60 วิ

## Token contract

```
<exp>.<nonce>.<sig>
exp   = unix seconds (ฐาน 10) — 60 วิในอนาคต
nonce = 16 hex chars (สุ่มใหม่ทุกใบ)
sig   = lowercase hex ของ HMAC-SHA256(WS_TOKEN_SECRET, "<exp>.<nonce>")
```

- ส่งเป็น query param: `wss://.../ws?token=...`
- ยอมรับ clock skew 30 วิหลัง exp · เทียบลายเซ็น constant-time (`hmac.Equal`)
- Secret ค่าเดียวกันสองฝั่ง (สร้างด้วย `openssl rand -hex 32`)

## Flow

1. Browser → `GET /api/ws-token` บนเว็บ (Worker มินต์ด้วย Web Crypto, `Cache-Control: no-store`)
2. Browser ต่อ WS พร้อม `?token=` → hub ตรวจ**ก่อน upgrade**: Origin → token (401) → เพดาน IP (429)
3. ทุก reconnect ขอตั๋วใหม่ (fetch timeout 3 วิ; client fail-open — server เป็นคนตัดสิน)

## ตำแหน่งในโค้ด

### repo นี้ (backend)

| ไฟล์ | หน้าที่ |
|---|---|
| `internal/ws/token.go` | `verifyToken` — parse + HMAC + expiry(+30s) |
| `internal/ws/ip.go` | `extractClientIP` — `CF-Connecting-IP` → XFF → RemoteAddr |
| `internal/ws/handler.go` | `Handler(HandlerConfig{...})` — ลำดับตรวจก่อน upgrade |
| `internal/ws/hub.go` | นับ connection ต่อ IP (`tryReserveIP`/`releaseIP`) |
| `internal/ws/client.go` | ปล่อยสิทธิ์ IP ตอน disconnect (ผ่าน `closeOnce`) |
| `internal/config/config.go` | `WS_TOKEN_SECRET` (ว่าง = ไม่บังคับ), `WS_MAX_CONNS_PER_IP` (default 8, ≤0 ปิด) |

### frontend (`flip1688/livescore-web`)

- `src/app/api/ws-token/route.ts` — มินต์ตั๋ว (secret จาก `getCloudflareContext().env` — **ไม่ใช่** `process.env`; อันหลังเป็น fallback ของ dev)
- `src/lib/wsToken.ts` + hooks ทั้งสอง — ขอตั๋วก่อนทุก connect

## พฤติกรรม

| WS_TOKEN_SECRET | token | ผล |
|---|---|---|
| ไม่ตั้ง | อะไรก็ได้ | ต่อได้ (โหมด rollout) |
| ตั้งแล้ว | ถูกต้อง | 101 |
| ตั้งแล้ว | ไม่มี/ปลอม/หมดอายุ | 401 |
| — | เกิน 8 conns/IP | 429 |

## Runbook

- **ตั้ง/rotate secret** (ลำดับสำคัญ: Worker ก่อนเสมอ):
  1. `openssl rand -hex 32`
  2. Worker: `echo '<ค่า>' | npx wrangler secret put WS_TOKEN_SECRET` — ⚠️ **ต้อง pipe เข้า stdin** รันแบบ non-interactive เฉย ๆ มันเก็บ*ค่าว่าง*เงียบ ๆ (โดนมาแล้ว)
  3. Server: แก้ `WS_TOKEN_SECRET` ใน `/opt/livescore/.env` → `systemctl restart livescore`
- **Dev เครื่อง**: ต้องมีค่าเดียวกันใน `.env.local` ของ frontend ไม่งั้น WS ใน dev ต่อไม่ได้
- **ปิดฉุกเฉิน**: คอมเมนต์ `WS_TOKEN_SECRET` ใน `.env` server → restart → กลับสู่โหมดเปิดรับหมด
- **เทส**: ตั๋วจริงจาก `/api/ws-token` ต้อง 101, ตัด `?token=` ทิ้งต้อง 401 (curl ต้องใช้ `--http1.1` ไม่งั้น handshake พังด้วย 400 หลอกว่าระบบเสีย)
