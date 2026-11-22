# lnwthailive — API reference & storage design

ออกแบบ 19 พ.ย. 2026 · ยืนยันกับ API จริงด้วยคีย์ Pro 22 พ.ย. 2026
อ่านคู่กับ [architecture.md](architecture.md) · [thscore-api.md](thscore-api.md) · `CLAUDE.md` หัวข้อ lnwthailive

feed ที่ 3 ของ repo นี้ (ต่อจาก thscore และ lotto2news) เป้าหมาย **ไม่ใช่** เอามาแทน thscore ทันที
แต่เก็บเป็น **ชุดข้อมูลคู่ขนาน** เพื่อเทียบว่าเจ้าไหนเข้ากับเนื้อหาเว็บเรามากกว่า —
กติกาเดิมทุกข้อยังอยู่: **ห้าม request จาก user ทะลุถึงต้นทาง**, `internal/lnw` คือที่เดียวที่คุยกับมัน,
ไม่ตั้ง `LNW_API_KEY` = ปิดทั้งระบบ ไม่มี sync loop ไม่มี REST route

## 1. Auth & limit (ยืนยันจาก production แล้ว 2026-11-22)

| | |
|---|---|
| Base URL | `https://www.lnwthailive.online` |
| Auth | header `x-api-key:` (หรือ `?api_key=` สำหรับ video player) |
| แพ็กเกจ | Pro Multi-Sport All-Access รายเดือน · `tier: "pro"` |
| Rate limit | header `x-rate-limit: 3000` (ต่อนาที) · `enforceRateLimit: false` ยังไม่บังคับจริง |
| เช็กคีย์ | `GET|POST /api/portal/verify` คืน `expiresAt`, `daysRemaining`, `permissions` รายกีฬา |
| ⚠️ ความปลอดภัย | คีย์รายเดือน **ล็อกโดเมน/IP เองไม่ได้** (`canAccessPanel:false`, `allowedIps:[]`) ใครได้คีย์ไปยิงได้หมด — ต้องขอผู้ขาย whitelist IP ของ VPS ให้ |

## 2. กับดัก — อ่านก่อนเขียนโค้ดทุกครั้ง

ทั้ง 7 ข้อนี้**พิสูจน์จาก payload จริง** ไม่ใช่จากเอกสารของผู้ขาย

### 2.1 เจ้าเดียวแต่มี id คนละจักรวาล 4 ชุด (ข้อที่กำหนด design ทั้งหมด)

lnwthailive เป็น **aggregator** ไม่ใช่เจ้าของข้อมูล แต่ละ endpoint มาจากคนละต้นทาง
และ **id ไม่เชื่อมกันเอง**

| endpoint | id space | ตัวอย่าง id | CDN โลโก้ = ต้นทางจริง |
|---|---|---|---|
| `/api/public/matches` · `/{id}/details` · `/live-incidents` | `rb_` | `rb_4330326` | `logos1.tcpwe138stya.ru` |
| `/api/public/results` | ตัวเลขล้วน | `19722095` | `cdn.sportmonks.com` → **SportMonks** |
| `/api/public/standings` | **ไม่มี id เลย** | — | `365scores.com` (ผ่าน `/api/imgproxy`) |
| `/api/analyse/today` | ตัวเลขล้วน คนละชุดกับ results | `356477` | `fbstat-media-stg.b-cdn.net` |

คู่เดียวกัน (นิวคาสเซิล–อาร์เซนอล 22 พ.ย.) เป็น `19722095` ใน results แต่เป็น `356477` ใน analyse
**ห้ามสมมติว่า id จาก endpoint หนึ่งใช้กับอีก endpoint ได้**

### 2.2 `/api/public/matches` ไม่มีวันที่

มีแค่ `time: "22:00"` กับ `day: "today"|"tomorrow"` — ต้องประกอบวันเองจากนาฬิกา ICT
และ **ห้ามเชื่อ `day` ตรง ๆ**: แมตช์ `day:"today" time:"02:00"` ตามกติกา 04:00 ของเราคือ
โปรแกรม**เมื่อวาน** ให้คำนวณ `match_date` ด้วย `service.MatchDateFor` เหมือน thscore เสมอ

### 2.3 timezone ต่างกันรายendpoint (เหมือน thscore ห้ามเหมารวม)

| field | zone | หลักฐาน |
|---|---|---|
| `results[].matches[].startingAt` | **UTC** | `2026-11-21 17:30:00` = คู่เดียวกับ analyse ที่เป็น `2026-11-22T00:30:00+07:00` |
| `analyse.matches[].matchDatetime` | **+07:00 ระบุมาในสตริง** | ใช้ได้ตรง ๆ |
| `matches[].time` | ICT (เวลาแสดงผล) | แมตช์ `22:00` ขึ้น `HT` ตอน 23:05 ICT |
| `fetchedAt` | epoch ms | ทั้ง results และ standings |

### 2.4 ชื่อทีมสะกดไม่ตรงกันระหว่าง endpoint ของตัวเอง

`อาร์เซนอล` (results) vs `อาร์เซน่อล` (standings, analyse) ·
`นิวคาสเซิ่ล ยูไนเต็ด` vs `นิวคาสเซิล ยูไนเต็ด` ·
ใน `/matches` บางคู่ชื่อเหย้าเป็นไทยแต่ชื่อเยือนเป็นอังกฤษ (`ฮัวชิปาโต้` vs `D. Concepcion`)
→ จับคู่ข้าม endpoint ด้วยชื่อตรง ๆ ไม่ได้ ต้อง normalize + ใช้เวลาแข่งช่วยยืนยัน

### 2.5 เอกสารของผู้ขายไม่ตรงกับของจริง (เหมือน lotto2news)

| เอกสารบอก | ของจริง |
|---|---|
| `matches[].status`, `.minute`, `.hasStream` | ไม่มีสักตัว — ของจริงคือ `matchStatus` (`"20'"`, `"HT"`, `"FT"`, `"รอแข่ง"`), `time`, `day`, `channels`, `embed_url` |
| `incidents: { id: [ {type,minute,team,player,assist,score} ] }` | `incidents: { id: { "1": [ {Min,Nm,PosA,Aid,ID,Fn,Ln,Pnt,Pnum,Pn,IT,NoP,Sor} ] } }` — แบ่งตามครึ่ง + คีย์ย่อ |

**ยึด `internal/lnw/client.go` เป็นความจริง ไม่ใช่หน้า `/docs` ของเขา**

### 2.6 `matchStatus` เอานาทีมาปนกับสถานะ

ค่าที่เจอจริงในรอบเดียว: `รอแข่ง` `5'` `20'` `35'` `45+'` `50'` `80'` `HT` `FT`
→ ต้อง parse เป็น (status, minute) แยกกัน และ **`45+'` คือทดเจ็บครึ่งแรก ไม่ใช่นาทีที่ 45**

### 2.7 โลโก้มาจาก CDN ของคนอื่นทั้งหมด

ทั้ง `tcpwe138stya.ru`, `sportmonks.com`, `365scores.com`, `b-cdn.net`
กติกาข้อ 4 ของ repo (**ห้าม hotlink**) ใช้กับ feed นี้ด้วย — ถ้าจะโชว์จริงต้อง mirror ลง R2
ผ่าน pipeline เดิม (`internal/service/logos.go`) โดยเก็บ `logo_source_url` ไว้ใน dictionary

## 3. Coverage เทียบ thscore (วัดจริง 22 พ.ย. 2026 เวลา 23:05 ICT)

| | thscore (ของเราตอนนี้) | lnwthailive |
|---|---|---|
| แมตช์วันนี้ | **1,197** | 211 |
| ลีกวันนี้ | **318** | 76 |
| ช่วงเวลาที่ให้ | เมื่อวาน → +7 วัน | **วันนี้ + พรุ่งนี้เท่านั้น** |
| ผลย้อนหลัง | มี (`matches` + TTL 30 วัน) | `/api/public/results?date=` ย้อนได้ (เมื่อวาน 159 คู่ / 53 ลีก · 20 พ.ย. 32 คู่) |
| ตารางคะแนน | ทุกลีกที่มีแมตช์ | 12 ลีก |
| ของที่ thscore ไม่มี | — | **สตรีม HLS + embed, มวยไทย/พักยก, วัวชน, ไก่ชน, IPTV, บทวิเคราะห์ไทย** |

> อ่านตัวเลขนี้ให้ถูก: lnw เป็น feed **สายสตรีม** ไม่ใช่ feed สายครอบคลุม
> ถ้าเป้าหมายคือ "ทุกลีกทั่วโลก" มันแพ้ขาด แต่ถ้าเป้าหมายคือ "ดูสดได้ในหน้าเดียว" thscore ทำไม่ได้เลย

## 4. Storage — ของที่ลงโค้ดแล้ว

หลักการ 4 ข้อ (ดูเหตุผลเต็มใน `CLAUDE.md`): แยก collection ตามต้นทาง · prefix `lnw_` ทุกตัว
ไม่แตะของ thscore แม้แต่ field เดียว · เก็บค่าดิบคู่กับค่าที่ parse แล้ว · `match_date`
คำนวณด้วยกติกา 04:00 ของเราเองเสมอ ไม่ใช้ `day` ของเขา

| collection | `_id` | มาจาก | สถานะ |
|---|---|---|---|
| `lnw_matches` | `rb_4330326` | `/api/public/matches` | ✅ |
| `lnw_results` | `19722095` (SportMonks) | `/api/public/results?date=` | ✅ |
| `lnw_standings` | anchor จาก `leagueUrl` (`epl`) | `/api/public/standings` | ✅ |
| `lnw_analysis` | `356477` (fbstat) | `/api/analyse/{today,tomorrow}` | ✅ |
| `lnw_match_links` | `day\|home\|away` (normalize แล้ว) | งาน `lnw-coverage` | ✅ |
| `lnw_coverage` | `YYYY-MM-DD` | งาน `lnw-coverage` | ✅ |
| `lnw_match_details` | rb id | `/{id}/details` (สถิติ + 11 ตัวจริง) | ยังไม่ทำ |
| `lnw_match_events` | rb id | `/live-incidents` | ยังไม่ทำ — ต้อง decode `IT` ก่อน |
| `lnw_boxing_programs` | program id (`"17343"`) | `/api/public/boxing` | ✅ (ตัว card ยังไม่ type — ดู §5) |
| `lnw_combat_bouts` | `<feed>:<id>` เช่น `wuachon:68539` | `/api/public/{wuachon,muay-pakyok}` | ✅ |

**ไม่มี REST route และไม่อยู่บน read path ใดๆ** ตั้งใจ: นี่คืองานเก็บหลักฐานเพื่อตัดสินใจ
เว็บหน้าบ้านยังกิน `/v1/*` ของ thscore เหมือนเดิม ถ้าวันหนึ่งตัดสินใจใช้ค่อยเพิ่ม `/v1/lnw/*`

### Cadence (`internal/service/lnw.go`)

`lnw-matches` 60s · `lnw-results` 2m · `lnw-standings` 10m · `lnw-analysis` 15m ·
`lnw-coverage` 1h (อ่าน DB อย่างเดียว ไม่ยิงต้นทาง) · `lnw-pakyok` 2m · `lnw-wuachon` 5m ·
`lnw-boxing` 15m — ทุกตัวมี `-once lnw-<job>`

### Config

```
LNW_API_KEY     ไม่ตั้ง = ปิดทั้ง feed (ค่า default คือปิด)
LNW_BASE_URL    default https://lnwthailive.online
LNW_RPS         default 2  (120/นาที จากเพดานจริง 3,000/นาที)
LNW_SPORTS      default football — CSV, ใช้กับ ?sport= ของ /api/public/matches
LNW_COMBAT      default boxing,muay_pakyok,wuachon — ตั้งว่าง = ไม่ยิง feed กีฬาต่อสู้
```

### การจับคู่กับ thscore — เพดานอยู่ที่ ~24%

`internal/service/lnw_link.go` ไล่ 4 ชั้น หยุดทันทีที่ผูกได้:

1. **ชื่อตรงกัน** (`exact_name`, conf 1.0) + เช็กว่า kickoff ห่างกันไม่เกิน 90 นาที
2. **เวลา ±5 นาที มีผู้สมัครเดียว** (`time_window`, conf 0.5)
3. **เวลา ±5 นาที หลายคู่ แต่สกอร์ตรงกันคู่เดียว** (`time_score`, conf 0.6)
4. ถ้ายังกำกวม = **ไม่ผูก** แล้วบันทึกเหตุผลลง `lnw_coverage`

ผลจริง 22 พ.ย.: `linked=60/251` · `unlinked_ambiguous=190` · `unlinked_script=111`
**อ่าน `linked` เดี่ยวๆ ไม่ได้** ต้องอ่านคู่กับ `unlinked_*` ไม่งั้นจะสรุปผิดว่าสองเจ้าไม่ซ้ำกัน

## 5. หน้ามวย (`/boxing`) — feed ที่จะมารองรับ

prototype: `lsmbet-burgundy-landing.naychang-marketing.chatgpt.site/boxing/`
หน้านี้ต้องการ: โปรแกรมรายวัน + ตัวกรองชนิด (**ทั้งหมด / มวยไทย / คิกบ็อกซิ่ง / มวยสากล / MMA**)
+ ตัวกรองสถานะ + คู่เด่น (แดง–น้ำเงิน, ชื่อ, ค่าย, พิกัดน้ำหนัก, "คู่ที่ N")
+ กระดานติดตาม + **บทความ 3 ชิ้น (RINGSIDE JOURNAL)**

### 5.1 ของที่ lnw มีให้จริง (วัดเมื่อ 2026-11-22 23:10 ICT)

| endpoint | ได้อะไร | สภาพตอนวัด |
|---|---|---|
| `/api/public/boxing` | **รายการ "ศึก" 26 รายการ** (ศึกมวยไทย 7 สี, ศึกมวยดีวิถีไทย, เกียรติเพชร …) พร้อมโลโก้/สแปลช | `totalEvents: 0` **ทั้ง 26 รายการ — ยังไม่มีคู่ชกสักคู่** |
| `/api/public/muay-pakyok` | มวยพักยก | `data: []`, `live: 0` ว่างเปล่า |
| `/api/public/wuachon` | วัวชน/ไก่ชน | **212 รายการ มีข้อมูลเต็ม** |

⚠️ **`/api/public/boxing` ไม่รับ parameter ใด ๆ** — ลอง `?date=`, `?day=`, `?programId=`
ได้ payload เดิมเป๊ะทั้ง `scrapedAt` เดียวกัน มันคือ snapshot ของ "รายการศึก" ไม่ใช่โปรแกรมคู่ชก
**ยังพิสูจน์ไม่ได้ว่า `events[]` หน้าตาเป็นยังไง** เพราะตอนวัดไม่มีคู่ชกเลย
→ ต้องยิงซ้ำช่วงที่มีมวยจริง (ศึกมวยไทย 7 สี อาทิตย์บ่าย, ศึกอื่นเย็น–ค่ำ) ก่อนเขียน struct

### 5.2 `wuachon` คือตัวอย่างของ shape ตระกูลเดียวกันที่มีข้อมูลจริง

และบังเอิญมี field ตรงกับที่ prototype ต้องการเกือบครบ:

```json
{"id":68539, "roundName":"22/11/69 สนามชนไก่ดาวอังคาร คู่ที่ 33",
 "type":"cock", "typeLabel":"ไก่ชน", "stadium":"…", "stadiumId":…,
 "dateTime":"2026-11-22T11:33:00+07:00", "status":"ended", "statusLabel":"จบแล้ว",
 "result":"tie", "resultLabel":"เสมอ / ถอน", "stakes":380000,
 "redCorner":{"name":"ไก่แดง"}, "blueCorner":{…}, "isLive":true,
 "stats":{"redWinPercent":…,"redFights":…,"redWins":…,"redWeight":…,"redForm":…,"opinion":…},
 "playerUrl":…, "iframeUrl":…, "stream":{…}}
```

ถ้า `boxing.events[]` ใช้ shape ใกล้เคียงกัน หน้ามวยจะได้ครบเกือบทุกช่องที่ prototype วางไว้
(สถิติแพ้–ชนะ, น้ำหนัก, ฟอร์ม, คู่ที่เท่าไหร่, สถานะ, ผล, สตรีม)

**กับดักที่เจอแล้วใน `wuachon`:**
- `status:"ended"` + `resultLabel:"เสมอ / ถอน"` แต่ `isLive:true` — **ขัดกันเอง**
  อย่าใช้ `isLive` ตัดสินสถานะ ให้ยึด `status`
- `roundName` เอาวันที่ (พ.ศ. ย่อ `22/11/69`) + สนาม + "คู่ที่ N" มายัดรวมเป็นสตริงเดียว
  ต้อง parse แยกถ้าจะใช้ "คู่ที่ N" ตามดีไซน์
- `dateTime` มี `+07:00` ระบุมาให้ ใช้ได้ตรง ๆ (ต่างจาก `results.startingAt` ที่เป็น UTC)

### 5.3 Collections ของหน้ามวย — ลงโค้ดแล้ว 22 พ.ย. 2026

| collection | `_id` | มาจาก |
|---|---|---|
| `lnw_boxing_programs` | `"17343"` | `/api/public/boxing` → `data.programs[]` |
| `lnw_combat_bouts` | `wuachon:68539` | `muay-pakyok` + `wuachon` (shape ตระกูลเดียวกัน แยกด้วย `sport`) |

รันจริงแล้ว: boxing 26 programs (`with_card: 0`) · pakyok 0 แถว · wuachon **212 แถว**
(ไก่ชน 150 / วัวชน 62) · `unknown_result: 10` · `live_contradicts_status: 200`

**`events[]` ของ boxing ยังไม่ type จงใจ** เก็บเป็น `raw_events` ดิบ และเก็บเฉพาะตอนไม่ว่าง
`SyncBoxing` จะ log ระดับ **WARN** ทันทีที่เจอ card ครั้งแรก — นั่นคือตอนที่จะเขียน struct ได้
จากหลักฐานจริง ไม่ใช่จากการเดา

⚠️ **`stream.url` ของ wuachon มี API key ฝังอยู่** (`stk=` decode base64 ได้ `tgl-pro-<key>`)
→ เก็บแค่ `has_stream` ยืนยันแล้วว่า document จริงไม่มีทั้ง `stk=` และ `http`
กับดักที่เหลืออีก 6 ข้อดู `CLAUDE.md` หัวข้อ "กีฬาต่อสู้"

### 5.4 บทความในหน้ามวย — ไม่ใช่งานของ feed นี้

`RINGSIDE JOURNAL` 3 ชิ้นเป็น **เนื้อหาเราเอง** (prototype เขียนไว้ว่า "บทความต้นฉบับ LSM99")
ใช้ WordPress headless ตัวเดิมที่ทำหน้าหวยไปแล้ว — เพิ่ม term ใหม่ในระบบเดิม ไม่ต้องสร้างของใหม่
ดู `lsm99-match-center/AGENTS.md` หัวข้อ "บทความหวย"

## 6. สิ่งที่ยืนยันแล้ว / ยังไม่รู้

**ยืนยันแล้ว 2026-11-22:**
- `/api/public/matches?sport=basketball` ใช้ได้จริง (136 แมตช์) — multi-sport ไม่ได้มีแต่ชื่อ
- `/api/analyse/tomorrow` และ `/api/analyse/{id}` ใช้ได้ (รายคู่ 26KB ลึกกว่า list)
- **`/api/public/results?date=2026-11-20` ใช้ได้** — ย้อนหลังได้ ไม่ได้มีแค่วันเดียว
  → backfill ผลย้อนหลังตอนเริ่มระบบได้

**ยังไม่รู้ ต้องยิงจริงก่อนเขียนโค้ด:**
1. `boxing.events[]` หน้าตาเป็นยังไง (§5.1)
2. `IT` ใน incidents แปลว่าอะไรบ้าง — ต้องทำตาราง decode เอง เอกสารไม่มี
3. ตอนโดน rate limit คืนอะไร (HTTP 429 หรือ 200 + `ok:false` แบบ thscore)
4. `results?date=` ย้อนได้ลึกสุดกี่วัน
