# Deploy บน VPS ด้วย systemd (ไม่ใช้ Docker)

App เป็น static binary ตัวเดียว (REST + sync worker + WS) — บน VPS ต้องมีแค่ Redis (apt) กับ reverse proxy; Mongo อยู่ Atlas

## ติดตั้งครั้งแรก (บน server)

```bash
# 1. Redis local (เป็น systemd service ในตัว)
sudo apt install redis-server

# 2. user + directory ของ app
sudo useradd -r -s /usr/sbin/nologin livescore
sudo mkdir -p /opt/livescore/.incoming
sudo chown -R livescore:livescore /opt/livescore

# 3. วาง .env ใน /opt/livescore (binary โหลดเองจาก working directory)
#    ต้องมีอย่างน้อย: MONGO_URI, THSCORE_API_KEY (+ R2_* 5 ตัวถ้าจะรัน logo job)
#    REDIS_ADDR ไม่ต้องตั้ง (default localhost:6379)

# 4. unit file (แก้ค่าใน deploy/livescore.service ก่อน — user/path)
sudo cp livescore.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now livescore
```

## Deploy รอบถัดไป (จากเครื่อง dev)

```bash
make deploy DEPLOY_HOST=me@1.2.3.4   # build linux → scp → systemctl restart
```

cross-compile ด้วย `CGO_ENABLED=0` เสมอ (static, ไม่ติด glibc) — server เป็น ARM ให้เติม `GOARCH=arm64`
อัปโหลดเข้า `.incoming/` แล้ว rename ทับ เพราะเขียนทับ binary ที่กำลังรันตรง ๆ จะเจอ `ETXTBSY`; rename ปลอดภัยเสมอ

## รัน sync job เดี่ยว / backfill บน server

```bash
cd /opt/livescore && sudo -u livescore ./livescore-api -once schedule
# job: dictionary|logos|schedule|schedule-ahead|schedule-modify|live-snapshot|live-changes|events-stats|standings|analysis
# logo backfill ใหญ่ ๆ ใช้ ./livescore-logo-sync (Mongo+R2 เท่านั้น ไม่แตะ Redis)
```

ลำดับ backfill ครั้งแรก: `dictionary` → `schedule` + `schedule-ahead` → ค่อย `standings` / `analysis` (สองตัวหลัง scope จากตาราง `matches` — ว่าง = ไม่มีอะไรให้ sync)

## Logs & ดูสถานะ

```bash
journalctl -u livescore -f          # slog JSON ไหลลง journald
systemctl status livescore
```

## Reverse proxy

ต้องมี caddy/nginx ทำ TLS + upgrade `/ws` (เหมือนแผนเดิม) — app ฟัง `:8080` (env `PORT`)
อย่าลืมตั้ง `WS_ALLOWED_ORIGINS` ใน `.env` เมื่อ frontend domain นิ่งแล้ว
