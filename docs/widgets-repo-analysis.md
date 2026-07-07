# วิเคราะห์ repo ตัวอย่าง (ChangPuakk/widgets + oneallsports-widgets)

วิเคราะห์เมื่อ 2026-07-08 เพื่อตอบ 2 คำถาม: **realtime ใช้ API function ไหนบ้าง** และ **เก็บ dictionary ยังไง**
ตัวอย่างทำงานกับหลาย provider (OneAllSports + THScore) แต่ของเราใช้ thscore เจ้าเดียว + เพิ่ม daily matchlist

- backend: `~/go/src/github.com/ChangPuakk/widgets` (Go + Mongo + Redis + Ably)
- frontend: `~/Works/oneallsports/widgets/oneallsports-widgets` (Next.js)

## 1. ข้อเท็จจริงของ thscore API ที่ยืนยันได้จากโค้ดจริง (docs สาธารณะไม่บอก)

| เรื่อง | ค่า |
|---|---|
| Base URL | `https://www.thscore.info` |
| Auth | query param **`api_key`** ทุก request |
| Format | JSON — ส่วนใหญ่ห่อใน `{"data": [...]}`, บาง endpoint (league.aspx, team.aspx) มี `{"code": n, "message": "..."}` |
| ⚠️ Error แบบเนียน | **rate limit คืน HTTP 200 + `code != 0`** — ต้องเช็ค `code` เสมอ ไม่ใช่แค่ HTTP status |
| `matchTime` (livescores/schedule) | string `dd-MM-yyyy HH:mm:ss` GMT+7 → parse ด้วย `"02-01-2006 15:04:05"` ใน zone GMT+7 |
| `matchTime`/`startTime` (changes.aspx) | **Unix timestamp (วินาที)** — มาเป็น number หรือ string ก็ได้ (ตัวอย่างรับเป็น `any` แล้ว parse เอง) |
| Endpoint เพิ่มที่ docs หน้า index ไม่ได้สรุป | `/football_th/schedule.aspx` (ตัวเต็ม), `/football_th/stats.aspx`, `/football_th/corner.aspx`, `/football_th/lineups.aspx`, `/football_th/standing/cup.aspx` |
| ทีม id ใน schedule | `homeId`/`awayId` เป็น **string** ใน payload (ตัวอย่างต้อง `strconv.Atoi`) |

## 2. Realtime — API functions ที่ใช้จริง (จาก `LiveDataCollector` ใน scheduler)

| Function → endpoint | cadence ของตัวอย่าง | ทำอะไร |
|---|---|---|
| `GetLivescoresToday` → `livescores.aspx` | ตอน start + **ทุก 1 นาที** (hydrate) | snapshot เต็มของวัน — สำคัญ: เป็นตัวเดียวที่มี `halfStartTime` ครบทุกแมตช์ ใช้กัน state drift และทำให้นาทีเดินได้ |
| `GetLivescoreChanges` → `livescores/changes.aspx` | **ทุก 30 วิ** | delta 20 วิล่าสุด → apply ลง `match_live_state` + publish Ably |
| `GetLiveMatchEventsLast3Minutes` → `events.aspx?cmd=new&date=<UTC วันนี้>` | **ทุก 1 นาที** | timeline events (ประตู/ใบ/เปลี่ยนตัว) → upsert + publish + ลบ Redis cache ราย match |
| `GetLiveMatchStats` → `stats.aspx?date=<UTC วันนี้>` | **ทุก 1 นาที** (รอบเดียวกับ events) | technical stats (possession/shots) → upsert + publish |

ที่มีใน client แต่**ไม่อยู่ใน loop realtime**: `analysis.aspx` (collector แยก, ยิงก่อนแมตช์เริ่ม), `lineups.aspx`, `corner.aspx` (ยังไม่ใช้)

**บทเรียนสำคัญจาก loop นี้:**
- **นาทีสดไม่ได้มาจาก feed** — thscore ให้ `minute` เฉพาะตอนมีเหตุการณ์ ตัวอย่างคำนวณเอง (`ComputeLiveMinute`): `status=1 → min(elapsed จาก halfStart+1, 45)`, `status=3 → min(45+elapsed+1, 90)`, ทดเจ็บใช้ `injuryTime` แยก → เราต้องเก็บ `half_start` ใน live state เสมอ
- Delta 30 วิ + snapshot 1 นาที เพียงพอ (ไม่ต้อง 5 วิแบบที่เราเขียนไว้ตอนแรก — ประหยัด quota กว่า)
- Publish แบบ **on-change only**: hash digest ต่อแมตช์ ไม่ push ซ้ำถ้าข้อมูลไม่เปลี่ยน
- Collector กรองด้วย allowlist ลีกที่เปิดใช้ก่อนเก็บ/push (ของเราถ้าเอาทุกลีกก็ตัดส่วนนี้ได้)

## 3. Realtime — ฝั่ง push + frontend

- Ably channel **ต่อแมตช์**: `widget:match:{matchId}` มี 3 event: `live` / `events` / `stats`
- Frontend (`useLiveScore`): SSR initial → reconcile กับ localStorage (เชื่อ state ที่ไปไกลกว่า) → subscribe `live` → **tick นาทีเองใน browser ระหว่าง delta** → ปิด socket เมื่อ `status < 0` (จบ/ยกเลิก)
- ไม่มี "snapshot on join" ใน Ably — SSR ต้องสดพอ ตัวอย่างเจอบั๊ก prefetch RSC เป็น snapshot ก่อนประตูมาแล้ว
- Header สดเสิร์ฟจาก Mongo `match_live_state` (**ไม่มี rate limit**) ไม่ proxy ไป thscore ตรง ๆ — เคยทำแล้วล่มเพราะ `schedule/basic` จำกัด 60 วิ/call

**ของเรา (WS self-hosted) map ตรง ๆ ได้เลย** — channel ต่อแมตช์ + event type เหมือนกัน แต่เราได้เปรียบ: ทำ snapshot-on-subscribe จาก Redis ได้ (Ably ทำไม่ได้) และต้องเพิ่ม **channel ระดับ matchlist** (`live` รวมทั้งวัน) ที่ตัวอย่างไม่มีเพราะหน้าเขาเป็นราย match

## 4. Dictionary — เก็บยังไง

| ข้อมูล | function → endpoint | วิธี run |
|---|---|---|
| ลีก | `GetLeagues` → `league/basic.aspx`, `GetLeagueProfile` → `league.aspx` | **script ครั้งคราว** (`scripts/thscoreleagues`, `refreshprofiles`) → `thscore_leagues` |
| ทีม | `GetTeamProfile` (สูงสุด 50 ids), `GetTeamProfilesByDay(day, page)` → `team.aspx` | script `thscoreteams` — ใช้ `day` ทำ bulk delta เลี่ยง per-id rate limit → `thscore_teams` |
| โลโก้ | ดาวน์โหลดจาก URL ใน profile → **อัปโหลด GCS + CDN** (ห้าม hotlink ตาม docs; CDN จีนอาจต้อง proxy) | script `fetchteamlogos`/`fetchleaguelogos` |
| Schedule/matchlist | `GetScheduleByDate` → `schedule/basic.aspx` | scheduler ทุกวัน **04:05 GMT+7** generate ล่วงหน้า 7 วัน |

**Matchdate ของตัวอย่าง = กติกาเดียวกับเรา:** `SportsDateFor` ตัดที่ 04:00 GMT+7 และ **คำนวณ match_date จาก kickoff ของแต่ละแมตช์** (`hour < 4 → วันก่อนหน้า`) แล้ว**เก็บเป็น field `match_date` ใน Mongo** — ไม่ใช่ query ด้วย time range แบบที่เราเขียนไว้ ข้อดี: query ง่าย (`match_date = "2026-07-07"`), index ตรง, ไม่มี edge case ขอบเวลา → **ควรเปลี่ยนของเราไปใช้แบบนี้**

ตัวอย่างมีตาราง mapping (`widget_leagues`, `widget_teams` — map id ระหว่าง OneAllSports ↔ THScore + ชื่อไทย) เพราะทำหลาย provider — **ของเราตัดทิ้งได้ทั้งชั้น** ใช้ thscore id ตรง ๆ

## 5. สิ่งที่ต้องแก้/ทำต่อในโปรเจกต์เรา

1. **Client**: base URL default `https://www.thscore.info`, auth param ชื่อ `api_key`, เช็ค `code != 0` ใน envelope, parser เวลา 2 แบบ (string `dd-MM-yyyy HH:mm:ss` / unix `any`)
2. **Model**: เพิ่ม `match_date` (string) ใน Match คำนวณตอน sync จาก kickoff — เลิก query แบบ time-range
3. **Live state**: เก็บ `half_start` + `injury_time` และ implement `ComputeLiveMinute` แบบเดียวกับตัวอย่าง
4. **Sync cadence**: changes 30 วิ / snapshot+events+stats 1 นาที / schedule 7 วันล่วงหน้าทุก 04:05
5. **เพิ่ม endpoint ใน client**: `stats.aspx` (ใช้ใน realtime), เผื่อ `lineups.aspx`, `corner.aspx`, `standing/cup.aspx`
6. **โลโก้**: ต้องมี pipeline โหลดเก็บเอง (เราใช้ VPS — เก็บ local disk + เสิร์ฟผ่าน nginx ก็พอ ไม่ต้อง GCS)
7. **WS**: channel `match:{id}` (event: live/events/stats) + channel `matchlist:{date}` สำหรับหน้า daily list + snapshot-on-subscribe จาก Redis
