# livescore — Project Context

Backend feed ข้อมูล/สถิติ/วิเคราะห์ฟุตบอลสำหรับเว็บไซต์ของเราเอง (Go, single binary: REST API + sync worker + WebSocket hub)

## เอกสารที่ต้องอ่านก่อนแก้โค้ด

| ไฟล์ | เนื้อหา |
|---|---|
| `docs/architecture.md` | System design ทั้งหมด — data placement, live pipeline, WS design, failure modes, scaling path |
| `docs/thscore-api.md` | thscore API reference: ทุก endpoint, rate limits, field schemas, timezone ราย field |
| `docs/widgets-repo-analysis.md` | บทวิเคราะห์ repo ตัวอย่าง production — realtime loop, dictionary sync, กับดักต่าง ๆ |

## Business rules (ห้ามละเมิด)

1. **matchdate ตัดที่ 04:00 GMT+7 ไม่ใช่เที่ยงคืน** — แมตช์เตะ 08 Jul 03:00 เป็นโปรแกรมของวันที่ 07 Jul (บอลยุโรปเตะตี 1–3 ต้องอยู่ใน "เมื่อคืน") โค้ดอยู่ที่ `CurrentMatchDate`/`MatchDateFor` ใน `internal/service/catalog.go` — คำนวณจาก kickoff รายแมตช์แล้วเก็บเป็น field `match_date` ใน Mongo, query รายวันใช้ field นี้เท่านั้น
2. **ห้าม request จาก user ทะลุถึง thscore** — read path เสิร์ฟจาก Redis → Mongo เท่านั้น; `internal/thscore` (เรียกโดย sync worker) คือที่เดียวที่คุย thscore และมี rate limiter แยกต่อ endpoint (limit ต่างกันมาก: 1 วัน/call ถึง 1 วิ/call)
3. **Timezone ของ thscore อ้างอิง docs ราย field ห้ามเหมารวม/ห้ามเดา** — field ที่เป็น GMT+7: `matchTime`, `halfStartTime`, `startTime`, `oprTime`, `modifyTime`; "วันนี้" ของ `livescores.aspx` เป็น GMT+0; field อื่นต้องเช็คกับ payload จริง
4. **ห้าม hotlink โลโก้จาก thscore** — mirror ลง Cloudflare R2 ตอน dictionary sync

## thscore API (ยืนยันจาก production code แล้ว)

- Base URL `https://www.thscore.info`, auth = query param `api_key`
- **Rate limit คืน HTTP 200 + `code != 0`** — client เช็คให้แล้วใน `fetch[T]`
- `matchTime` ใน livescores/schedule = string `dd-MM-yyyy HH:mm:ss` GMT+7; ใน changes.aspx docs บอก Unix timestamp แต่ payload จริงเป็น string GMT+7 แบบเดียวกัน — ใช้ `ParseMatchTime`/`ParseTimeAny` (รับทั้ง unix และ datetime string)
- `homeId`/`awayId` เป็น string ใน schedule แต่เป็น number ใน livescores — struct ใช้ `FlexString`
- `country.aspx` ไม่อยู่ใน plan ที่ซื้อ (code=2) — dictionary sync ข้ามให้อัตโนมัติ
- มี smoke tool `cmd/thscore-smoke` ยิงเทียบ payload จริงกับ struct ทุก endpoint (ระวัง rate limit ฝั่ง dictionary 30 นาที–1 ชม./call ตอนรันซ้ำ — ใช้ flag `-only`)
- นาทีสดต้องคำนวณเอง (`model.ComputeLiveMinute` จาก half start, cap 45/90, ทดเจ็บ = `injury_time`) — feed ให้ minute เฉพาะตอนมีเหตุการณ์

## Tech stack & การตัดสินใจ

- MongoDB Atlas (dictionary, schedule, events) · Redis (live state `live:match:{id}`, read cache) · Cloudflare R2 (โลโก้)
- Realtime: **WebSocket self-hosted** (`internal/ws`) — channels: `live`, `match:{id}`, `matchlist:{date}`; snapshot-on-subscribe จาก `Catalog.Snapshot` แล้วตาม delta
- Deploy: VPS เครื่องเดียว docker compose — single binary ถูกต้องแล้ว (worker ตัวเดียว = ไม่เกิน quota) ตอน scale ค่อยแยก worker + เปลี่ยน `Publisher` เป็น Redis Pub/Sub (seam มีแล้วใน `internal/service/sync.go`)
- Sync cadences: dictionary 24 ชม. / schedule 1 ชม. (วันนี้+พรุ่งนี้ เพราะ matchdate คร่อม 2 วันปฏิทิน thscore) / **schedule-ahead 24 ชม.** (โปรแกรมล่วงหน้า +2..+7 วัน) / modify 30 นาที / live snapshot 1 นาที / **live changes 15 วิ** (delta window 20 วิ → gap-free) / events+stats 1 นาที / **standings 6 ชม.** (เฉพาะลีกที่มีแมตช์เมื่อวาน/วันนี้/พรุ่งนี้) / **analysis 30 นาที** (prefetch แมตช์ที่เตะใน 24 ชม. ข้างหน้า, ข้ามที่ fetch แล้ว <24 ชม.)

## Repo ตัวอย่างสำหรับอ้างอิง (อยู่นอก repo นี้ ในเครื่อง dev)

- Backend: `~/go/src/github.com/ChangPuakk/widgets` — production ที่ใช้ thscore จริง (หลาย provider; ของเรา thscore เจ้าเดียว ตัดชั้น mapping ทิ้ง)
- Frontend: `~/Works/oneallsports/widgets/oneallsports-widgets` — Next.js, `useLiveScore` hook, UI ไทย

## Conventions

- stdlib `net/http` mux (Go 1.22+ patterns), `log/slog` JSON, ไม่ใช้ framework
- ก่อนส่งงาน: `gofmt -l .` สะอาด, `go build ./...`, `go vet ./...`, `go test -race ./...`
- Docs สรุปเป็นภาษาไทย, โค้ด/comment ภาษาอังกฤษ

## สถานะ & สิ่งที่ค้าง (2026-07-09)

- โครงเสร็จ: typed thscore client + parsing, sync pipeline ครบ 6 loops, WS hub + tests, matchlist รายวัน, R2 logo pipeline
- ✅ ได้ `THSCORE_API_KEY` จริงแล้ว + ยิงเทียบ payload กับ struct ครบทุก typed endpoint แล้ว (`cmd/thscore-smoke`) — แก้ diff ที่เจอครบ: `FlexString` ids, `ParseTimeAny`, stats `oprTime`, ข้าม country sync
- ✅ R2 เชื่อมแล้ว: bucket `livescore-logos`, public URL `pub-*.r2.dev` (dev — production ค่อยผูก custom domain แล้วเปลี่ยน `R2_PUBLIC_BASE_URL`), creds ใน `.env` ครบ 5 ตัว, ทดสอบ end-to-end ด้วย `cmd/r2-smoke` ผ่าน
- ✅ Logo pipeline แยกจาก dictionary sync แล้ว: dictionary เก็บ `logo_source_url` (ซ่อนจาก JSON), job `logos` (รันต่อท้าย dictionary + `-once logos` + binary เดี่ยว `cmd/logo-sync`/`make logo-sync` ใช้แค่ Mongo+R2) เทียบ `logo_url` กับ `ExpectedURL` แบบ deterministic → mirror เฉพาะที่ขาด/เปลี่ยน — idempotent, self-heal, เปลี่ยน `R2_PUBLIC_BASE_URL` แล้ว migrate URL ให้เองไม่ต้องโหลดรูปใหม่
- ⚠️ **ISP ไทยบล็อก DNS ของ `titan007.com`** (CDN โลโก้ thscore คืน `192.168.1.0`/`fe80::`) — ตั้ง `LOGO_DNS_SERVER=1.1.1.1:53` ใน `.env` เพื่อให้ logo downloader ใช้ resolver ตัวเอง (VPS ที่ DNS ปกติไม่ต้องตั้ง)
- `cmd/api -once <job>` รัน sync job เดี่ยวแล้วออก (dictionary|logos|schedule|schedule-modify|live-snapshot|live-changes|events-stats|standings|analysis) — ใช้ backfill/รีทราย
- ✅ REST ครบชุดแล้ว (2026-07-09): `/v1/matches/{id}` + `/events` + `/stats` (เสิร์ฟ shape เดียวกับ WS, evict cache ตอน sync), `/v1/leagues/{id}/standings` (sync job ใหม่ 6 ชม., payload จริงไม่มี envelope — ดูหมายเหตุใน thscore-api.md), `/v1/matches/{id}/analysis` (blob ดิบ `json.RawMessage`, **prefetch** ก่อน kickoff ไม่ lazy — กัน user ทะลุ thscore) — error convention: 400/404/500
- ค้าง: Dockerfile + compose ของ app, ตัดสินใจเรื่อง frontend repo (มีผล CORS/`WS_ALLOWED_ORIGINS`), Odds เอาไหม (plan แพงขึ้น)
