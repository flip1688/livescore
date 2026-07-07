# thscore API — เอกสารภายใน

สรุปจาก official docs: <https://www.thscore.info/doc/docs_id=42.shtml> (ดึงเมื่อ 2026-07-08)

## ภาพรวม

- ทุก endpoint เป็น `GET` ภายใต้ path prefix `/football_th/...`
- **Host + auth (ยืนยันจาก repo ตัวอย่าง ChangPuakk/widgets — ดู [widgets-repo-analysis.md](widgets-repo-analysis.md))**:
  - Base URL: `https://www.thscore.info`
  - Auth: query param **`api_key`** ทุก request
  - Response เป็น JSON ห่อใน `{"data": [...]}`; บาง endpoint (league.aspx, team.aspx) มี envelope `{"code", "message", "data"}`
  - ⚠️ **rate limit คืน HTTP 200 + `code != 0`** — ต้องเช็ค `code` เสมอ
  - `matchTime` ใน livescores/schedule เป็น string `dd-MM-yyyy HH:mm:ss` (GMT+7); `matchTime`/`startTime` ใน changes.aspx docs บอกว่าเป็น **Unix timestamp** (number หรือ string) แต่ **payload จริง (2026-07-08) ส่งเป็น string `dd-MM-yyyy HH:mm:ss` GMT+7** — parser ฝั่งเรา (`ParseTimeAny`) รับได้ทั้งสามแบบ
  - endpoint ที่มีจริงเพิ่มเติม: `/football_th/schedule.aspx` (ตัวเต็ม), `/football_th/stats.aspx`, `/football_th/corner.aspx`, `/football_th/lineups.aspx`, `/football_th/standing/cup.aspx`

### ยืนยันจาก payload จริง (smoke test `cmd/thscore-smoke`, 2026-07-08)

- `homeId`/`awayId` **ชนิดไม่เท่ากันข้าม endpoint**: schedule/basic.aspx ส่งเป็น string แต่ livescores.aspx ส่งเป็น **number** — struct ใช้ `FlexString` รับทั้งคู่
- `matchTime`/`startTime` ใน changes.aspx เป็น string `dd-MM-yyyy HH:mm:ss` GMT+7 (ไม่ใช่ unix อย่างที่ docs บอก)
- `stats.aspx`: `oprTime` อยู่**ระดับ match** (คู่กับ `matchId`) ไม่ใช่ใน stat item แต่ละตัว
- `country.aspx`: **plan ปัจจุบันไม่ได้ซื้อ** — คืน code=2 "You haven't purchased this data" (sync ข้ามอย่างเดียว ไม่ล้ม)
- field เอกสารหลายตัว (corner/card/rank/weather ฯลฯ) ไม่โผล่ใน schedule/basic.aspx — โผล่เฉพาะ livescores.aspx ตามที่ comment ใน struct ระบุ
- ⚠️ **Timezone ระบุเป็นราย field ตาม docs — field ไหน docs ไม่ระบุ ห้ามเดา** ต้องตรวจกับ payload จริงก่อน
  - Field ที่ docs ระบุว่าเป็น **GMT+7 (Bangkok Time)**: `matchTime` + `halfStartTime` (schedule) · `matchTime` (livescores) · `matchTime` + `startTime` (livescores/changes) · `oprTime` (events) · `matchTime` + `modifyTime` (schedule/modify)
  - "วันนี้" ของ `livescores.aspx` นับตาม **GMT+0** (00:00–23:59)
  - Field ที่ระบุ GMT+7 แล้ว ห้ามตีความเป็น UTC แล้ว convert ซ้ำ
- ฝั่งเรา: "matchdate" ตัดที่ **04:00 GMT+7** ไม่ใช่เที่ยงคืน (ดู business rule ใน [architecture.md](architecture.md) ข้อ 5)
- ต้องมี paid plan: Stats / Live Data / Odds / Odds Pro / Betfair (แต่ละ endpoint ต้องการ plan ต่างกัน)
- รูป logo ทุกชนิด: **ให้ดาวน์โหลดมาเก็บเอง ห้าม hotlink** (docs ระบุตรง ๆ)

## ⚠️ Rate limits (หัวใจของ design)

| Endpoint | Hard limit | แนะนำ | ใช้ทำอะไร |
|---|---|---|---|
| `/league/basic.aspx` | 1 ชม./call | 1 วัน/call | dictionary → Mongo |
| `/league.aspx` | 30 นาที/call | 1 วัน/call | dictionary → Mongo |
| `/team.aspx` | 30 นาที/call | 1 วัน/call | dictionary → Mongo |
| `/country.aspx` | 30 นาที/call | — | dictionary → Mongo |
| `/schedule/basic.aspx` | 60 วิ/call | 1 ชม./call | fixtures → Mongo |
| `/schedule/modify.aspx` | 60 วิ/call | 30 นาที/call | ตาม diff ตาราง 12 ชม.ล่าสุด |
| `/livescores.aspx` | 10 วิ/call | 1 นาที/call | full snapshot วันนี้ → Redis |
| `/livescores/changes.aspx` | 1 วิ/call | 2–10 วิ/call | **delta 20 วิล่าสุด** — ตัว poll หลักช่วง live |
| `/events.aspx` | 10 วิ/call (hard), แนะนำ 1 นาที/call | | goal/card/sub events |
| `/standing/league.aspx` | 5 วิ/call | 1 วัน/call | ตารางคะแนน |
| `/analysis.aspx` | 1 วิ/call | 6 ชม./call (cache ฝั่งเขา 24 ชม.) | H2H / form / สถิติ |

> Pattern ที่ docs ตั้งใจ: ดึง **snapshot เต็ม** ด้วย endpoint ช้า แล้ว poll **changes/modify** ถี่ ๆ เพื่อ sync ส่วนต่าง

## สถานะแมตช์ (ใช้ร่วมกันทุก endpoint)

| code | ความหมาย | | code | ความหมาย |
|---|---|---|---|---|
| `0` | ยังไม่เริ่ม | | `-1` | จบแล้ว |
| `1` | ครึ่งแรก | | `-10` | ยกเลิก (Cancelled) |
| `2` | พักครึ่ง | | `-11` | TBD |
| `3` | ครึ่งหลัง | | `-12` | Terminated |
| `4` | ต่อเวลา | | `-13` | Interrupted |
| `5` | จุดโทษ | | `-14` | เลื่อนแข่ง (Postponed) |

---

## Dictionary endpoints

### League & Cup Profile (Basic) — `docs_id=42`

`GET /football_th/league/basic.aspx`

| param | type | required | หมายเหตุ |
|---|---|---|---|
| `leagueId` | string | no | สูงสุด 50 ids |

Response: `leagueId` (int), `name` (เช่น Brazil Serie A), `shortName` (เช่น BRA D1), `type` (1=League, 2=Cup), `subLeagueName`

### League & Cup Profile (Full) — `docs_id=67`

`GET /football_th/league.aspx`

| param | type | required | หมายเหตุ |
|---|---|---|---|
| `leagueId` | string | no | สูงสุด 50 ids |
| `day` | int | no | เอาเฉพาะที่แก้ไขภายใน n วัน (ใช้ทำ incremental sync) |

Response เพิ่มจาก basic: `color` (#RGB), `logo` (URL — เก็บ local), `totalRound`, `currentRound`, `currentSeason` (เช่น 2018-2019), `countryId`, `country`, `countryLogo`, `areaId` (0=International, 1=Europe, 2=America, 3=Asia, 4=Oceania, 5=Africa)

Sub-endpoint: `?cmd=rule` → field `rule` (กติกาการแข่งขัน)

### Team Profile — `docs_id=22`

`GET /football_th/team.aspx`

| param | type | required | หมายเหตุ |
|---|---|---|---|
| `teamId` | string | no | สูงสุด 50 ids |
| `day` | int | no | เอาเฉพาะที่แก้ไขภายใน n วัน |
| `page` | int | no | 1–5 แบ่งหน้า (สำหรับเน็ตช้า) |

Response: `teamId`, `leagueId`, `name`, `logo` (เก็บ local), `foundingDate` ("1984" หรือ "1890-9-6"), `address`, `area`, `venue`, `capacity`, `coach`, `website`, `isNational` (bool)

Sub-endpoint: `?cmd=more` → เพิ่ม `match` (ถ้วยที่เคยได้), `season`

### List of Countries — `docs_id=222`

`GET /football_th/country.aspx` — ไม่มี param

Response: `countryId` (int), `country` (อังกฤษ), `countryTh` (**มีชื่อไทยให้เลย**)

---

## Schedule endpoints

### Schedule & Results (Basic) — `docs_id=41`

`GET /football_th/schedule/basic.aspx`

| param | type | required | หมายเหตุ |
|---|---|---|---|
| `date` | string | no | `yyyy-MM-dd`; ย้อนหลังได้ 1 เดือน |
| `leagueId` | int | no | คู่กับ `season` ได้ (default = ฤดูกาลปัจจุบัน) |
| `season` | string | no | เช่น `2018-2019` ใช้คู่ `leagueId` |
| `matchId` | string | no | สูงสุด 50 ids |

**ต้องส่งอย่างน้อย 1 ใน (`date`, `leagueId`, `matchId`) และห้ามส่งพร้อมกัน**

Response (ต่อแมตช์): `matchId`, `leagueId`, `leagueType`, `leagueName`, `leagueShortName`, `leagueColor`, `matchTime` (GMT+7), `halfStartTime`, `homeId`/`homeName`, `awayId`/`awayName`, `homeScore`/`awayScore`, `homeHalfScore`/`awayHalfScore`, `kickOff` (1=home เขี่ยก่อน, 2=away), `minute`, `neutral` (สนามกลาง), `status` (ตารางด้านบน), `explain`/`extraExplain`, ต่อเวลา/จุดโทษ: `extraTimeStatus` (1=จบปกติ, 2=จบแบบพิเศษ, 3=กำลังต่อเวลา), `extraHomeScore`/`extraAwayScore`, `penHomeScore`/`penAwayScore`, `winner` (1=home, 2=away), สองนัด: `twoRoundsHomeScore`/`twoRoundsAwayScore`

### Match Modify Record — `docs_id=33`

`GET /football_th/schedule/modify.aspx` — บันทึกการ **ลบแมตช์/เปลี่ยนเวลาแข่ง** ใน 12 ชม.ล่าสุด (ใช้คู่กับ Schedule เพื่อ sync diff)

Response: `matchId`, `type` (`modify` | `delete`), `matchTime` (เวลาเดิม), `modifyTime` (เวลาที่แก้)

---

## Live endpoints (Live Data plan)

### Livescores for Today — `docs_id=20`

`GET /football_th/livescores.aspx` — ไม่มี param, คืนทุกแมตช์ของ "วันนี้" (GMT+0 00:00–23:59)

Response: fields เหมือน schedule + `homeYellow`/`awayYellow`, `homeRed`/`awayRed`, `homeCorner`/`awayCorner`, `season`, `minute`, `hasLineup`

### Livescores Changes — `docs_id=14` ⭐

`GET /football_th/livescores/changes.aspx` — **เฉพาะแมตช์ที่มีการเปลี่ยนแปลงใน 20 วินาทีล่าสุด** ออกแบบมาให้ poll ถี่คู่กับ snapshot ของ `livescores.aspx`

Response เพิ่ม: `startTime` (เวลาเริ่มครึ่ง), `var` (เหตุการณ์ VAR), `injuryTime`, `winner`, `extraExplain`

### Events — `docs_id=15`

`GET /football_th/events.aspx`

| param | หมายเหตุ |
|---|---|
| `date` | `yyyy-mm-dd` ย้อนหลัง 1 เดือน |
| `cmd=new` | เอาเฉพาะที่อัปเดตใน 3 นาทีล่าสุด (สำหรับ poll) |

Response: `matchId` + `events[]`: `eventId`, `minute` (45/90 = ทดเจ็บ), `type`, `homeEvent` (bool), `playerId`/`playerName` (เปลี่ยนตัวจะมี 2 คน), `assistPlayerId`, `overtime`, `oprTime` (GMT+7)

**Event type codes**: 1=Goal, 2=Red card, 3=Yellow card, 7=Penalty scored, 8=Own goal, 9=Second yellow, 11=Substitution, 13=Penalty missed, 14=VAR

Sub-endpoint: `?cmd=shot` → เพิ่ม `outcome`, `situation`, `shotType`, `goalZone`, `isBlocked`

---

## Stats endpoints (Stats plan)

### League Standing — `docs_id=43`

`GET /football_th/standing/league.aspx`

| param | type | required | หมายเหตุ |
|---|---|---|---|
| `leagueId` | int | **yes** | ถ้าลีกมี division จะคืน set ของ `subLeagueId` |
| `subLeagueId` | int | no | ระบุ stage/division (default = อันแรก) |

Response: `leagueInfo` (`leagueId`, `name`, `currentSeason`, `color`, `shortName`, `totalRound`, `currentRound`), `subLeagueInfos[]` (`subLeagueId`, `name`, `totalRound`, `currentRound`, `hasScore`, `hasTwoLegs`, `currentSubLeague`), `teamInfos[]` แยก 6 มุมมอง: `totalStandings`, `halfStandings`, `homeStandings`, `awayStandings`, `homeHalfStandings`, `awayHalfStandings` — แต่ละอัน: `rank`, `teamId`, `winRate`/`drawRate`/`loseRate`, `winAverage`/`loseAverage`, `totalCount`, `winCount`/`drawCount`/`loseCount`, `getScore`/`loseScore`, `goalDifference`, `integral` (แต้ม)

ผลนัดล่าสุด: `0`=ชนะ, `1`=เสมอ, `2`=แพ้, `3`=ว่าง · `leagueColorInfos` = โซนเลื่อนชั้น/ตกชั้น + สี

### Matches Analysis — `docs_id=109`

`GET /football_th/analysis.aspx?matchId=...` (`matchId` required)

คืน: H2H ย้อนหลัง ≤20 นัด, ฟอร์ม 20 นัดล่าสุดของทั้งสองทีม, โปรแกรม 5 นัดถัดไป, สถิติ odds (win/loss, over/under, handicap), การกระจายประตูตามช่วงเวลา (ทุก 10 นาที), HT/FT combinations

---

## หน้า docs อื่นที่ยังไม่ได้สรุป (ไว้ตามอ่านเพิ่ม)

| docs_id | เรื่อง |
|---|---|
| 21 | Schedule & Results (ตัวเต็ม, ใต้ Live Data) |
| 16 | Live Stats (possession, shots ฯลฯ) |
| 63 | Corner |
| 17 | Lineups |
| 64 | Injury |
| 65, 66 | Live Text |
| 69 | Transfer |
| 272 | Match Insights |
| 68 | Subleague Profile |
| 112 | Cup Stage Profile |
| 23 | Player Profile |
| 108 | Referee Profile |
| 37, 40, 49, 51 | Player Stats (match / league) |
| 38, 45 | Standing (subleague / cup) |
| 39 | Top Scorer |
| 47 | FIFA Ranking |
| 242 | Live animation |
| 24, 44, 138, 46 | Odds ทั้งหมด / Betfair |
| 223 | List of Bookmakers |
| 243 | Basketball |

URL pattern: `https://www.thscore.info/doc/docs_id=<id>.shtml`

## นัยต่อ architecture ของเรา

1. **Dictionary (league/team/country) → Mongo** sync วันละครั้งด้วย cron, ใช้ param `day` ทำ incremental
2. **Schedule → Mongo** sync รายชั่วโมง + poll `schedule/modify.aspx` ทุก 30 นาทีเพื่อจับแมตช์ที่ถูกลบ/เลื่อน
3. **Live → Redis**: snapshot จาก `livescores.aspx` ทุก 1 นาที + delta จาก `livescores/changes.aspx` ทุก ~5–10 วิ, events ด้วย `events.aspx?cmd=new` ทุก 1 นาที
4. Client ฝั่งเราต้องมี **rate limiter ต่อ endpoint** (ไม่ใช่ limiter รวมตัวเดียว) เพราะ limit ต่างกันมาก (1 วัน/call จนถึง 1 วิ/call)
5. ยังต้องหา: **API host จริง + วิธี auth** จากหน้า account หลังสมัคร plan
