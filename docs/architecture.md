# livescore — System Design

ออกแบบเมื่อ 2026-07-08 · อ่านคู่กับ [thscore-api.md](thscore-api.md)

## 1. Requirements & constraints

**Functional**
- เสิร์ฟข้อมูลฟุตบอล: ลีก ทีม โปรแกรม/ผลบอล สกอร์สด events ตารางคะแนน และสถิติวิเคราะห์ ให้เว็บไซต์ของเราเอง
- สกอร์เปลี่ยนแล้ว หน้าเว็บต้องเห็นแบบ realtime ผ่าน **WebSocket**

**Constraints (ตัวกำหนด design ทั้งหมด)**
- thscore เป็น source of truth แต่ rate limit เข้มมากและ**ต่างกันต่อ endpoint** (dictionary 1 call/วัน → live delta หลาย call/วิ)
- จึงห้ามมี request จาก user ทะลุไปถึง thscore เด็ดขาด — ระบบเราคือ **replica + fan-out layer**
- Deploy บน **VPS เครื่องเดียวด้วย docker compose** → ออกแบบเป็น single instance ก่อน แต่เขียน seam ไว้ให้แยกได้
- ภาษา Go, MongoDB Atlas (managed, อยู่นอกเครื่อง), Redis (local บน VPS)

## 2. High-level architecture

```text
                  ┌─────────────────────── VPS (docker compose) ───────────────────────┐
                  │                                                                     │
 thscore API ◀────┤  Sync Worker ──write──▶ MongoDB Atlas (dictionary, schedule, ผลย้อนหลัง)
 (rate-limited)   │      │                                                              │
                  │      ├──write──▶ Redis (live state, read cache)                     │
                  │      └──publish──▶ WS Hub (in-process event bus)                    │
                  │                        │                                            │
 เว็บของเรา ◀─────┤  HTTP API (REST) ◀── Redis ◀─(miss)─ Mongo                          │
      ▲           │                                                                     │
      └── WS ─────┤  WebSocket endpoint (/ws) ◀── WS Hub                                │
                  └─────────────────────────────────────────────────────────────────────┘
```

**หนึ่ง binary สามบทบาท** (API + Sync Worker + WS Hub ในโปรเซสเดียว):
- ถูกต้องบน single instance: worker มีตัวเดียว → ไม่มีทางยิง thscore ซ้ำเกิน limit
- WS hub in-process: worker เจอ diff แล้ว push ตรงเข้า hub ได้เลย ไม่ต้องผ่าน broker
- **Seam สำหรับอนาคต**: ถ้าต้อง scale API เป็นหลาย instance → แยก worker เป็น binary ที่สอง แล้วเปลี่ยน event bus in-process เป็น **Redis Pub/Sub** (interface `Publisher` ตัวเดียว สลับ implementation)

## 3. Data placement

| ข้อมูล | เก็บที่ | เหตุผล | refresh |
|---|---|---|---|
| League, Team, Country | Mongo (คงทน) + Redis cache TTL 6 ชม. | เปลี่ยนแทบไม่เคย, thscore ให้ดึงวันละครั้ง | cron 24 ชม. (`day` param ทำ incremental) |
| Schedule / ผลบอล | Mongo + Redis cache ต่อวัน (วันนี้ TTL 30 วิ, วันอื่น 10 นาที) | ต้อง query ย้อนหลัง/ล่วงหน้า | วันนี้+พรุ่งนี้ทุก 1 ชม. · ล่วงหน้า +2..+7 วันทุก 24 ชม. (`schedule-ahead`) · `modify.aspx` ทุก 30 นาที (จับแมตช์ถูกลบ/เลื่อน) |
| Live state (สกอร์/การ์ด/นาที) | Redis key ต่อแมตช์ (สำหรับ WS) + snapshot loop upsert สกอร์ลง Mongo ทุก 1 นาที (ให้ match list สด) | เปลี่ยนทุกวินาที | snapshot 1 นาที + delta ทุก ~5-10 วิ |
| Events (ประตู/ใบเหลือง) | Mongo + push ผ่าน WS | ต้องเก็บถาวรไว้แสดง timeline | `events.aspx?cmd=new` ทุก 1 นาที |
| Standings / Analysis | Mongo + Redis cache | thscore cache 24 ชม. อยู่แล้ว | standings หลังแมตช์จบ / analysis on-demand แบบ lazy + cache |

**Logo mirroring**: thscore ห้าม hotlink โลโก้ทีม/ลีก → dictionary sync เก็บแค่ `logo_source_url` ลง doc (ซ่อนจาก API JSON) แล้ว job `logos` (รันต่อท้าย dictionary ทุก 24 ชม. หรือสั่งมือ `cmd/api -once logos`) เทียบ `logo_url` ที่เก็บไว้กับ `ExpectedURL` (derive แบบ deterministic จาก hash ของ source URL) — doc ไหนไม่ตรงคือ "ค้าง" → ดาวน์โหลดแล้วอัปขึ้น **Cloudflare R2** (8 workers, พลาดรายรูปไม่ล้ม batch แค่ค้างไว้รอบหน้า) แล้ว stamp `logo_url` กลับลง doc ดีไซน์นี้ idempotent + self-heal: โหลดพลาด/source เปลี่ยน/ย้าย `R2_PUBLIC_BASE_URL` (เช่นผูก custom domain) ก็ converge เอง โดยไม่โหลดรูปที่มีใน bucket แล้วซ้ำ; ไม่ตั้ง R2 credentials = ข้าม job นี้ (`internal/storage`, `internal/service/logos.go`)

**DNS ของ CDN โลโก้**: `zq.titan007.com` โดน ISP ไทยบล็อกระดับ DNS (router คืน `192.168.1.0`/`fe80::`) — ตั้ง `LOGO_DNS_SERVER` (เช่น `1.1.1.1:53`) ให้ logo downloader resolve ผ่าน DNS สาธารณะเฉพาะ client ตัวนี้ ไม่แตะ DNS ของระบบ/ส่วนอื่น

**Redis key layout**

```text
live:index                 → set ของ matchId ที่กำลังแข่งวันนี้
live:match:{matchId}       → JSON สถานะล่าสุดของแมตช์ (TTL จนจบวัน)
leagues / teams:{leagueId} / matches:{date}   → read cache ของ REST
```

## 4. Live pipeline (หัวใจของระบบ)

1. **Snapshot loop (ทุก 1 นาที)** — `livescores.aspx` ดึงทุกแมตช์ของวัน เขียนทับ `live:match:*` ทั้งชุด → กัน state drift สะสมจาก delta ที่พลาด
2. **Delta loop (ทุก 5-10 วิ)** — `livescores/changes.aspx` คืนเฉพาะแมตช์ที่เปลี่ยนใน 20 วิล่าสุด:
   - เทียบกับ state เดิมใน Redis → คำนวณ diff (สกอร์เปลี่ยน, สถานะเปลี่ยน, ใบแดง ฯลฯ)
   - เขียน state ใหม่ลง Redis
   - publish diff เข้า WS hub → fan-out ให้ client ที่ subscribe
3. **Events loop (ทุก 1 นาที)** — `events.aspx?cmd=new` เก็บรายละเอียดเหตุการณ์ลง Mongo + push
4. **จบแมตช์** (status → -1): persist ผลสุดท้ายลง Mongo, ลบออกจาก `live:index`

## 5. REST API (สำหรับเว็บของเรา)

| Endpoint | ใช้ทำอะไร |
|---|---|
| `GET /v1/matches?date=YYYY-MM-DD` | **Match list รายวัน** (หน้าหลักของเว็บ) — default = วันนี้ |
| `GET /v1/leagues` | รายชื่อลีกทั้งหมด |
| `GET /v1/leagues/{id}/teams` | ทีมในลีก |
| `GET /healthz` | health check |

**กติกาของ match list รายวัน (business rule — ห้ามเปลี่ยนเป็นเที่ยงคืน):**
- **"matchdate" = 04:00 เวลาไทย → 04:00 ของวันถัดไป** เช่น แมตช์เตะ 08 Jul 2026 03:00 (GMT+7) นับเป็น matchdate **07 Jul 2026** — เพราะบอลยุโรปเตะตี 1–3 ต้องอยู่ในโปรแกรม "เมื่อคืน" ไม่ใช่วันใหม่
- ผลที่ตามมา: matchdate หนึ่งวันของเรา**คร่อมสองวันปฏิทินของ thscore** (date param ของ thscore เป็น GMT+7 ตัดเที่ยงคืน) → sync schedule ต้องดึงทั้ง "วันนี้" และ "พรุ่งนี้" เสมอ
- Implementation: sync คำนวณ matchdate จาก kickoff ของแต่ละแมตช์แล้ว**เก็บเป็น field `match_date`** ใน Mongo — query รายวันใช้ field นี้ตรง ๆ ไม่ใช้ time-range (ตามแบบ repo ตัวอย่าง ดู [widgets-repo-analysis.md](widgets-repo-analysis.md))
- ⚠️ **Timezone ของ thscore ต้องอ้างอิง docs เป็นราย field** — field ที่ docs ระบุ GMT+7 (`matchTime`, `halfStartTime`, `startTime`, `oprTime`, `modifyTime`) ห้าม convert ซ้ำเหมือนเป็น UTC; field ที่ docs ไม่ระบุ timezone ห้ามเดา ต้องเช็คกับ payload จริง; และ "วันนี้" ของ `livescores.aspx` นับตาม GMT+0 (รายละเอียดใน [thscore-api.md](thscore-api.md))
- ตอบเป็นลิสต์เรียงตามเวลาเตะ โดย**ฝังชื่อลีก/สีลีกไว้ในแต่ละแมตช์** (denormalize ตอน sync) ให้ frontend group ตามลีกได้เลยไม่ต้อง join
- **ความสดของสกอร์ในลิสต์**: snapshot loop เขียนสกอร์ล่าสุดลง Mongo ทุก 1 นาที + cache ของ "วันนี้" TTL แค่ 30 วิ (วันอื่น 10 นาที) → ลิสต์สดภายใน ~1 นาที ส่วนความสดระดับวินาทีเป็นหน้าที่ WS (ข้อ 6)
- วันย้อนหลังดูได้เท่าที่เรา sync เก็บไว้ (thscore ย้อนหลังให้แค่ 1 เดือน แต่ของเราสะสมใน Mongo ได้เรื่อย ๆ)

## 6. WebSocket design

- Endpoint เดียว `GET /ws` (upgrade), hub เป็น goroutine กลางถือ registry ของ client
- **Subscription model**: client ส่ง `{"subscribe": "live"}` (ทุกแมตช์วันนี้) หรือ `{"subscribe": "match:{id}"}` — hub ส่งเฉพาะ channel ที่ขอ
- Message เป็น JSON: `{"channel": "match:123", "type": "score", "data": {...}}` — type: `score | status | card | event | snapshot`
- Client เพิ่งต่อ → ส่ง snapshot ปัจจุบันจาก Redis ให้ก่อน แล้วค่อยตาม delta (กัน gap)
- Slow client: buffered channel ต่อ client, เต็มแล้ว drop connection (client reconnect เอง) — ห้าม block hub
- Auth: ยังไม่ต้อง (เว็บเราเอง) — กันด้วย CORS/origin check พอ

## 7. Deployment (VPS, docker compose)

```text
services: app (Go binary เดียว), redis (local, appendonly)
Mongo = Atlas (นอกเครื่อง) · reverse proxy = caddy/nginx ทำ TLS + /ws upgrade
```

- Config ผ่าน env (`.env`) — มี `THSCORE_BASE_URL`/`THSCORE_API_KEY` (รอ credentials)
- Log เป็น JSON ลง stdout → `docker logs` พอสำหรับตอนนี้
- Backup: Mongo ฝั่ง Atlas จัดการ, Redis เป็น state ที่สร้างใหม่ได้ (แค่รอ snapshot รอบถัดไป) → ไม่ต้อง backup

## 8. Failure modes

| เหตุการณ์ | ผลกระทบ | การรับมือ |
|---|---|---|
| thscore ล่ม/ตอบช้า | ข้อมูลค้าง | API ยังเสิร์ฟจาก Redis/Mongo ได้ปกติ; worker log error แล้ว retry รอบถัดไป (limiter คุมไม่ให้ถล่มซ้ำ) |
| Redis ล่ม | cache miss + live หาย | REST fallback ไป Mongo (ทำแล้วใน read path); live กลับมาเองใน 1 นาทีหลัง Redis ฟื้น (snapshot loop) |
| app restart | WS หลุดทั้งหมด | client reconnect + ได้ snapshot ใหม่ทันที; Redis/Mongo อยู่นอกโปรเซส ไม่เสียอะไร |
| Delta พลาด (poll gap > 20 วิ) | สกอร์ค้างชั่วคราว | snapshot loop 1 นาทีเป็น self-healing |

## 9. Scaling path (เมื่อถึงเวลา ไม่ใช่ตอนนี้)

1. แยก `cmd/worker` ออกจาก `cmd/api` (โค้ดพร้อมอยู่แล้ว แค่ main คนละตัว)
2. เปลี่ยน in-process event bus → Redis Pub/Sub ให้ API หลาย instance รับ diff ได้
3. API + WS scale แนวนอนหลัง load balancer (sticky ไม่จำเป็นถ้า subscribe ใหม่หลัง reconnect)

## 10. สิ่งที่ยังเปิดอยู่

- [x] ~~thscore host + auth~~ — ยืนยันจาก repo ตัวอย่างแล้ว (`https://www.thscore.info`, `api_key` query param) และ implement payload parsing แล้ว
- [ ] ยิง API จริงด้วย key จริงเพื่อ verify struct กับ payload (โครงถอดจาก production code จึงเสี่ยงต่ำ)
- [ ] เว็บ frontend อยู่ repo ไหน / host ที่เดียวกันไหม (มีผลกับ CORS + `WS_ALLOWED_ORIGINS`)
- [ ] ต้องการข้อมูล Odds ไหม (plan แพงขึ้น)
