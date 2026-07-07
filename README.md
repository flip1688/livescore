# livescore

Backend สำหรับ feed ข้อมูล สถิติ และการวิเคราะห์ฟุตบอล — source of truth คือ thscore API
(rate limit เข้มมาก จึงเก็บ dictionary ใน MongoDB และ live state ใน Redis;
รายละเอียด API และข้อจำกัดอยู่ที่ [docs/thscore-api.md](docs/thscore-api.md))

## Architecture

```text
thscore API ──(sync worker, per-endpoint rate limits)──▶ MongoDB Atlas (dictionary, schedule)
                                                    └──▶ Redis (live state, read cache)
client ──▶ HTTP API ──▶ Redis ──(miss)──▶ MongoDB     ※ read path ไม่แตะ thscore เด็ดขาด
```

- `cmd/api` — HTTP server + sync worker ในโปรเซสเดียว
- `internal/thscore` — client เดียวที่คุยกับ thscore, rate limiter แยกต่อ endpoint
- `internal/store` — MongoDB repositories
- `internal/cache` — Redis JSON helpers
- `internal/service` — read path (cache-first) และ sync loops
- `internal/handler` — HTTP handlers (stdlib mux)
- `internal/storage` — Cloudflare R2 (S3-compatible) client for logo mirroring

## Run

```sh
make redis                 # local Redis via docker compose
cp .env.example .env       # เติม MONGO_URI (Atlas) และ thscore credentials
set -a; source .env; set +a
make run
```

Endpoints: `GET /healthz`, `GET /v1/leagues`, `GET /v1/leagues/{id}/teams`, `GET /v1/matches?date=YYYY-MM-DD` (matchlist รายวัน, ตัดวันที่ 04:00 GMT+7), `GET /ws` (WebSocket — subscribe `live` / `match:{id}` / `matchlist:{date}`, ได้ snapshot ก่อนแล้วตาม delta)

## TODO

- [ ] ใส่ `THSCORE_API_KEY` จริงแล้วยิงเทียบ payload กับ struct (โครงถอดจาก repo ตัวอย่างที่ใช้งาน production อยู่ — ดู docs/widgets-repo-analysis.md)
- [x] Logo pipeline: ดาวน์โหลดโลโก้ทีม/ลีกแล้ว mirror ไป Cloudflare R2 ตอน sync dictionary + เสิร์ฟจาก `R2_PUBLIC_BASE_URL` (ห้าม hotlink) — รอแค่ credentials จริง (`.env.example`), ไม่งั้นตกไป dev mode เก็บ URL ต้นทาง
- [ ] REST endpoints เพิ่ม: match events/stats, standings, analysis
- [ ] Dockerfile + compose service ของ app สำหรับ deploy จริงบน VPS
