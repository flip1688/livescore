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
- **ต่อเวลา/จุดโทษ: `homeScore`/`awayScore` แช่แข็งที่ 90 นาที** — สกอร์สดช่วง ET อยู่ใน `extraExplain` (aggregate) — `applyExtra` ใน mapper โปรโมตขึ้น `home_score`/`away_score` + เก็บ detail ใน `Match.Extra` (จุดโทษไม่รวมในสกอร์หลัก) ดูหมายเหตุใน thscore-api.md
- นาทีสดต้องคำนวณเอง (`model.ComputeLiveMinute` จาก half start, cap 45/90, ทดเจ็บ = `injury_time`) — feed ให้ minute เฉพาะตอนมีเหตุการณ์

## Tech stack & การตัดสินใจ

- MongoDB Atlas (dictionary, schedule, events) · Redis (live state `live:match:{id}`, read cache) · Cloudflare R2 (โลโก้)
- Realtime: **WebSocket self-hosted** (`internal/ws`) — channels: `live`, `match:{id}`, `matchlist:{date}`; snapshot-on-subscribe จาก `Catalog.Snapshot` แล้วตาม delta
- Deploy: VPS เครื่องเดียว **systemd ไม่ใช้ Docker** (ตัดสินใจ 2026-07-09) — unit `deploy/livescore.service`, `make deploy` จากเครื่อง dev, ขั้นตอนใน `docs/deploy-systemd.md`; single binary ถูกต้องแล้ว (worker ตัวเดียว = ไม่เกิน quota) ตอน scale ค่อยแยก worker + เปลี่ยน `Publisher` เป็น Redis Pub/Sub (seam มีแล้วใน `internal/service/sync.go`)
- Sync cadences: dictionary 24 ชม. / schedule 1 ชม. (เมื่อวาน..พรุ่งนี้ — `date` ของ thscore นับแบบ GMT+0 แมตช์ตี 1–7 โมงบ้านเราอยู่ date เมื่อวาน ต้องดึงต่อเพื่อรับผลแก้ย้อนหลัง เช่น สกอร์ต่อเวลา) / **schedule-ahead 24 ชม.** (โปรแกรมล่วงหน้า +2..+7 วัน) / modify 30 นาที / live snapshot 2 นาที / **live changes 15 วิ** (delta window 20 วิ → gap-free) / events+stats 2 นาที / **standings 6 ชม.** (เฉพาะลีกที่มีแมตช์เมื่อวาน/วันนี้/พรุ่งนี้) / **analysis 30 นาที** (prefetch แมตช์ที่เตะใน 24 ชม. ข้างหน้า, ข้ามที่ fetch แล้ว <24 ชม.)
- ⚠️ **`THSCORE_API_KEY` ใช้ร่วมกับ repo widgets (production)** — rate limit นับต่อ key จึงต้องบวกโหลดของ widgets เสมอ (changes 30 วิ, livescores/events/stats 1 นาที, proxy ตาม user บน schedule/basic, events, stats, lineups, analysis) → endpoint ที่ hard limit เป็นวินาทีฝั่งเราวิ่งครึ่งความถี่ของ widgets, schedule รายชั่วโมง pin ที่ hh:30 (`loopAligned`), analysis limiter 3 วิ (2026-09-11) — ทางแก้ถาวรคือขอ key แยกต่อ product
- Data retention: MongoDB TTL index ลบอัตโนมัติ — `matches` (บน `kickoff_at`) และ `match_events`/`match_stats` (บน `updated_at`) หมดอายุตาม `MATCH_RETENTION_DAYS` (default 30 วัน) ส่วน `match_analysis` (บน `fetched_at`, blob ใหญ่สุด ~16KB/แมตช์ ใช้ไม่ได้แล้วหลังจบเกม) แยกอายุสั้นกว่าด้วย `ANALYSIS_RETENTION_DAYS` (default 7 วัน) — ค่า <= 0 = ปิด TTL ของ collection นั้น; `standings`/`leagues`/`teams`/`countries` ไม่หมดอายุ; สร้าง/อัปเดตใน `Store.EnsureIndexes` (`internal/store/mongo.go`)

## หวย (lotto2news) — เพิ่ม 19 พ.ย. 2026

feed ที่ 2 ของ repo นี้ คนละเจ้ากับ thscore `internal/lotto` คือที่เดียวที่คุยกับมัน
(กติกาเดียวกับ thscore: **ห้าม request จาก user ทะลุถึงต้นทาง**) เปิดใช้ด้วย `LOTTO_API_KEY`
ถ้าไม่ตั้งค่า = ปิดทั้งระบบ ไม่มี sync loop ไม่มี REST route

| | |
|---|---|
| collection | `lotto_markets` (_id = slug) · `lotto_draws` (_id = drawId) |
| sync | webhook (ทันที) + sweep ทุก 6 ชม. (162 ตลาด = 162 calls) |
| job เดี่ยว | `-once lotto-markets` / `-once lotto-results` |
| REST | `/v1/lotto/{markets,latest,day/{day},markets/{slug},markets/{slug}/history}` |
| webhook | `POST /v1/lotto/webhook` ตรวจ `X-Webhook-Secret` |

**เอกสารของผู้ให้บริการเชื่อไม่ได้ ให้ยึด `internal/lotto/client.go`** — เขียนไว้ว่า history คืน
array ที่ `data` (จริงคือ `data.results`), ตัวอย่าง webhook ใช้ `slug:"gov"` `category:"thai"`
`tier:"main"` ซึ่ง**ไม่มีสักค่าในระบบจริง** (ของจริงคือ `gov-th`/`gov`/`normal`) ·
`limit` เพดาน 100 · `to` เป็น exclusive · `/api/v1/result` คืนแค่ตลาดที่เพิ่ง publish ไม่กี่ตัว
ไม่ใช่ทุกตลาด · `/api/v1/result/gov-th` คืน `latest:null` ทั้งที่ history มีงวดอยู่

กับดักข้อมูล 3 ข้อที่โค้ดจัดการไว้แล้ว **อย่ารื้อ**:

1. **ต้นทางส่งงวดซ้ำ** คนละ `drawId` (งวด 16 ก.ค. 2026 ของ `gov-th` เป็นทั้ง 525 และ 689)
   → **ห้ามทำ unique index บน `(slug, draw_day)`** ใช้ flag `superseded` เลือกตัวที่
   `published_at` ใหม่กว่า (`model.MarkSuperseded`)
2. **งวดเก่าของหวยรัฐบาลมีแต่รางวัลที่ 1** (ครบจริง 11 จาก 26 งวด) → เก็บลง DB แต่ซ่อนจาก
   read path ด้วย flag `complete`
3. **14 ตลาดไม่เคยมีงวดเลย + หุ้นไทยเที่ยงตายตั้งแต่ มิ.ย. 2026** ต้นทางไม่มี flag บอก →
   `active` คำนวณจากความสดของงวด (`LOTTO_STALE_AFTER` default 30 วัน)

> `gov-gsb`/`gov-baac` อยู่ category `gov` เหมือน `gov-th` แต่ใช้ชนิดรางวัลคนละชุด
> (`ticket/3up/2down` vs `first/front3/last3/last2`) — **ตัดสินใจจาก slug ห้ามใช้ category**

⚠️ **โควตานับรวมทั้ง account ไม่ใช่ต่อ key** (error บอกเอง "รวมทุก key") — รัน `-once` ตอน
service วิ่งอยู่ = ยิงซ้อนกันทะลุ 60/นาที client retry ให้แล้วแต่อย่าทำโดยไม่จำเป็น

`lotto_draws` **ไม่มี TTL** ต่างจาก `matches` เพราะต้นทางเก็บย้อนหลังแค่ ~100 งวดต่อตลาด
อะไรที่ลบไปคือหายถาวร

## lnwthailive — feed ที่กำลังประเมินแข่งกับ thscore (เพิ่ม 19 พ.ย. 2026)

feed ที่ 3 ของ repo **ยังไม่ได้ใช้งานจริง** ใส่ไว้เพื่อเก็บข้อมูลมาเทียบว่าควรย้ายจาก thscore ไหม
`internal/lnw` คือที่เดียวที่คุยกับมัน เปิดด้วย `LNW_API_KEY` ไม่ตั้ง = ปิดสนิท (default คือปิด)

| | |
|---|---|
| collection | `lnw_matches` · `lnw_results` · `lnw_standings` · `lnw_analysis` · `lnw_match_links` · `lnw_coverage` |
| job เดี่ยว | `-once lnw-verify\|lnw-matches\|lnw-results\|lnw-standings\|lnw-analysis\|lnw-coverage` |
| REST | **ไม่มี** — เป็นงานเก็บข้อมูลเปรียบเทียบล้วนๆ ไม่มี route สาธารณะ |
| cadence | matches 60s · results 2m · standings 10m · analysis 15m · coverage 1h |

**ห้ามให้มันเขียนทับ collection ของ thscore เด็ดขาด** (`matches`, `teams`, `leagues`,
`standings`, `match_analysis`) ทุกอย่าง prefix `lnw_` — ถ้าสองเจ้าทับกันได้ การเปรียบเทียบก็
ไม่มีความหมาย และการเลิกใช้ทำได้ด้วยการ drop 6 collection นี้ทิ้งเฉยๆ

### เรื่องใหญ่ที่สุด: มันคือ aggregator ของ 4 ต้นน้ำที่ id ไม่ตรงกันเลย

```
/api/public/matches    id "rb_4344928"   โลโก้ logos1.tcpwe138stya.ru
/api/public/results    id 19893334 int   โลโก้ cdn.sportmonks.com
/api/public/standings  ไม่มี id เลย       โลโก้ 365scores (base64 ห่อใน /api/imgproxy)
/api/analyse/*         id 342830 int     มี league/team/season id ชุดของตัวเอง
```

ต่างจาก thscore ที่ทุกอย่างแขวนบน match id เดียว **ที่นี่ join ด้วย key ไม่ได้ ต้องเดาจากชื่อทีม
+ เวลาแข่ง** → `internal/service/lnw_link.go` ทำ crosswalk เก็บลง `lnw_match_links` พร้อม
`method` + `confidence` เสมอ **ห้ามเอา id ที่เดาได้ไปเขียนกลับลง document ของ feed**

แถมชื่อทีมคนละภาษา: matches/standings เป็นไทย แต่ **results เป็นอังกฤษล้วน** → สองอันนี้
จับคู่ด้วยชื่อไม่ได้เลย เหลือแค่เวลา และผูกให้เฉพาะตอนมีผู้สมัครเดียวในหน้าต่าง 90 นาที
(`linkResultsByTime`) ถ้ากำกวม = ปล่อยไม่ผูก แล้วรายงานเป็นตัวเลขใน `lnw_coverage`

### กับดักของ feed นี้ (โค้ดจัดการแล้ว อย่ารื้อ)

1. **`/api/public/matches` ไม่มีวันที่** มีแค่ `day:"today"|"tomorrow"` + `time:"20:00"`
   → ต้อง resolve ด้วยนาฬิกา ณ ตอน fetch (`lnw.ResolveKickoff`, `SyncMatches` จับเวลาไว้
   ครั้งเดียวแล้วส่งต่อ) **เก็บดิบไว้แปลงทีหลัง = ผิดวัน** เก็บ `raw_day`/`raw_time` ไว้ตรวจย้อน
2. **feed นี้ไม่มีประวัติย้อนหลัง** คืนแค่วันนี้+พรุ่งนี้ ย้อนหลังต้องไป `/api/public/results?date=`
   ซึ่งเป็น id space คนละชุด
3. **URL สตรีมมี token อายุสั้น** เอกสารเขาเขียนเอง "ห้ามแคชเกิน 15 วิ" → **ไม่เก็บลง Mongo**
   เก็บแค่รายชื่อช่อง (`model.LnwChannel` ไม่มีฟิลด์ URL)
4. **สกอร์ก่อนแข่งเป็นสตริง `"vs"`** → `ParseScore` คืน `ok=false` ใช้ `HasScore` แยก 0-0 จริง
   ออกจาก "ยังไม่เริ่ม" **ห้ามอ่านค่า 0 เปล่าๆ**
5. **standings ไม่มี league id** ใช้ anchor จาก `leagueUrl` (`/standings#epl` → `epl`)
   เป็น `_id` ถ้าไม่มี anchor = ข้ามทิ้ง ไม่ key ด้วยชื่อที่เขาเปลี่ยนคำได้
6. **status คนละชุดกันทั้ง 3 feed** (`"30'"/"HT"/"รอแข่ง"` · `NS/INPLAY_1ST_HALF/FT_PEN` ·
   `FULL_TIME`) → normalize แยกกัน 3 ฟังก์ชัน และเก็บค่าดิบไว้ทุกครั้ง

> `match_date` ใช้เส้นตัด 04:00 ICT เดียวกับฝั่ง thscore (`MatchDateFor`) **จงใจ** เพื่อให้
> นับเทียบกันวันต่อวันได้ตรงๆ

### เอกสารของเขาขัดกันเอง — อย่าเชื่อ ให้ยึด `internal/lnw/client.go`

ราคา: หน้า demo บอก Starter ฿500/สัปดาห์, Pro ฿3,500/เดือน · `llms.txt` บอก Starter
฿4,500/เดือน, Pro ฿6,500/เดือน · rate limit: 10/min vs 60/min vs 1,000/min สำหรับ tier เดียวกัน
→ `LNW_RPS` default **0.13 (~8/นาที)** คือสมมติว่าได้น้อยที่สุด ยิง `-once lnw-verify`
(`/api/portal/verify`) เพื่อดูค่าจริงจาก account ก่อนปรับ cadence

⚠️ token ในช่อง playground หน้าเว็บ (ฟิลด์ `initialCsrf`) **ไม่ใช่ API key** — เป็น session token
เซ็น HMAC มีวันหมดอายุ ใช้ทำ sync ไม่ได้ · ตอนประเมิน sandbox ของเขาเองก็ใช้งานไม่ได้

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
- ✅ Deploy เป็น systemd แทน Docker แล้ว (2026-07-09): `deploy/livescore.service` (user ต้องเติมค่า user/path เอง) + `make build-linux` / `make deploy` + `docs/deploy-systemd.md` — docker-compose.yml เหลือไว้แค่ dev redis (`make redis`)
- ค้าง: ติดตั้งจริงบน VPS (เติมค่า .service + .env), ตัดสินใจเรื่อง frontend repo (มีผล CORS/`WS_ALLOWED_ORIGINS`), Odds เอาไหม (plan แพงขึ้น)
