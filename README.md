# Pixel Agent Office

เว็บ Local-first บน macOS สำหรับมอบหมายและติดตามงาน Codex และ Antigravity ผ่านโถงกิลด์ 2D แบบ Pixel Art ส่งงานเดี่ยวหรือให้ Agent ทำงานต่อกันผ่าน Workflow ได้

## เอกสาร

- [Architecture](docs/architecture.md)

- [Frontend specification](docs/frontend-specs/README.md)
- [Backend specification](docs/backend-specs/README.md)

## ดาวน์โหลดและติดตั้ง

### สิ่งที่ต้องมี

- Git, Go 1.27 ขึ้นไป, Python 3 และ Node.js 22.12 ขึ้นไปพร้อม npm
- Docker Desktop พร้อม `docker compose` ที่รองรับ `up --wait` สำหรับรัน PostgreSQL ให้เปิด Docker Desktop ก่อนเริ่ม
- Codex CLI ที่รองรับ `codex app-server` รัน `codex login` แล้วตรวจด้วย `codex login status`

### ติดตั้งครั้งแรก

1. Clone repository แล้วสร้างไฟล์ตั้งค่าบนเครื่อง

   ```bash
   git clone --branch main https://github.com/Gun8742/AIOrchestrator.git
   cd AIOrchestrator
   cp .env.example .env
   ```

2. แก้ `POSTGRES_PASSWORD` ใน `.env` เป็นรหัสเฉพาะเครื่องก่อนเริ่มฐานข้อมูลครั้งแรก เก็บไฟล์นี้ไว้บนเครื่อง
3. ติดตั้ง dependencies และ build Frontend จากโฟลเดอร์ repository

   ```bash
   npm ci --prefix frontend
   npm run build --prefix frontend
   ```

### เปิดเว็บ

รันจากโฟลเดอร์ repository

```bash
docker compose up -d --wait
python3 scripts/run-backend.py
```

เปิด [Agent Guild](http://127.0.0.1:8080/) แล้วกรอกรหัสครั้งเดียวจาก Terminal ภายใน 10 นาที หากรหัสหมดอายุให้หยุดแล้วเริ่ม Backend ใหม่

Backend ใช้ Codex config ของเครื่อง หาก model ใน config ใช้ไม่ได้ สามารถตั้ง environment variable `PIXEL_CODEX_MODEL` เป็น model ที่บัญชีรองรับก่อนรัน Backend โดยไม่แก้ config ส่วนกลาง เช่น `PIXEL_CODEX_MODEL=gpt-6-astra python3 scripts/run-backend.py` ตรวจ model ด้วย `codex app-server` ตาม environment ที่ใช้งานจริง

`Ctrl+C` หยุด Backend ใช้ `docker compose stop` เพื่อหยุด PostgreSQL โดยเก็บข้อมูลใน volume ไว้ เมื่อกลับมาใช้ให้รันสองคำสั่งในหัวข้อเปิดเว็บอีกครั้ง

## ใช้งาน Task และ Workflow

1. กด **+** ข้างช่อง **โปรเจกต์** แล้วใส่ absolute path ของโฟลเดอร์ที่มี README เช่น path ที่ได้จากการรัน `pwd` ในโฟลเดอร์ repository
2. เลือก **Codex** แล้วลองส่งคำสั่ง “อ่าน README.md แล้วสรุปวิธีเริ่มโปรเจกต์เป็นภาษาไทย อย่าแก้ไขไฟล์”
3. คลิกงานใน **กระดานภารกิจ** เพื่อดูสถานะ ผลลัพธ์ events และคำขอ approval การยกเลิกหยุดงานได้ ส่วนการแก้ไฟล์ที่เกิดขึ้นก่อนหน้านั้นจะยังอยู่

ในแผง Workflow กำหนด Agent และคำสั่งของแต่ละขั้นได้ 1–10 ขั้น แล้วกดเริ่ม Workflow ผลลัพธ์ที่บันทึกของขั้นก่อนจะส่งพร้อมคำสั่งให้ขั้นถัดไป จึงอาจส่ง source code หรือข้อมูลอ่อนไหวด้วย หากขั้นหนึ่ง failed/cancelled ระบบจะไม่เริ่มขั้นถัดไป งานทุกแบบใช้คิว FIFO ร่วมกันต่อ project

Backend เปิด Codex และ Antigravity เมื่อพบ Docker image OAuth ที่ตรงกับแอปและ volume session ที่ตั้งไว้; Claude ยังเลื่อนตามคำขอผู้ใช้ หาก Docker/image/volume ไม่พร้อม Antigravity จะแสดง unavailable โดยไม่มี automatic fallback ส่วนบัญชีหมดอายุหรือโควตาหมดอาจทำให้ task failed จึงต้องตรวจผลแต่ละงาน

## เชื่อม Antigravity ด้วย Google AI Pro (ตัวเลือกเสริม)

1. Build image จากโฟลเดอร์โปรเจกต์: `docker build -f backend/antigravity.Dockerfile -t pixel-agent-office-agy:1.4.9 backend`
2. รัน `bash scripts/login-agy.sh` แล้ว Login ด้วยบัญชี Personal ที่มี AI Pro ใน browser ของคุณ; หากมี authorization code ให้วางเฉพาะใน Terminal สคริปต์ทดสอบข้อความสังเคราะห์และตรวจว่า container ใหม่ใช้ session เดิมได้
3. เริ่ม Backend ใหม่เพื่อโหลด provider แล้วเลือก Antigravity ใน Task หรือแต่ละขั้นของ Workflow

ใช้โควตาจากบัญชี Google ผ่าน OAuth ไม่มีการส่ง Gemini API key และตั้ง `useG1Credits:false` เพื่อไม่ให้ใช้เครดิตเพิ่มเมื่อโควตาหมด Session เก็บใน Docker volume `pixel-agent-office-agy-auth` แยกจาก project; ผู้มีสิทธิ์ Docker เข้าถึง volume นี้ได้ อย่าลบ volume หากยังต้องการเก็บ Login

Antigravity แบบ headless ยังไม่รองรับ interactive approval ผ่านเว็บ หาก runtime ปฏิเสธ action ที่ต้องขอ permission งานจะแสดง failed; ระบบไม่อนุมัติแทนผู้ใช้ หลังยืนยันว่าหยุดแล้วและบันทึก terminal state สำเร็จ จึงลบเฉพาะ execution container โดยเก็บ OAuth volume ไว้ หาก cleanup ล้มเหลวจะมีข้อความแจ้งใน Backend log ดูหลักฐานและข้อจำกัดที่ [integration notes](docs/backend-specs/v2/antigravity-integration-notes.md)

## เมื่อคิวโปรเจกต์ถูกระงับ

Backend ระงับคิวเมื่อยืนยันไม่ได้ว่า execution ก่อนหน้าหยุดแล้ว เพื่อไม่ให้งานใหม่แก้ไฟล์พร้อมกัน งานที่เริ่มไปแล้วจะไม่ถูกรันซ้ำ และงานที่ยังรออยู่จะคงอยู่ในคิว

สำหรับงาน Codex หากต้องการให้ระบบตรวจอีกครั้ง ให้รีสตาร์ตเครื่อง macOS เดิม แล้วเริ่ม Backend ตามขั้นตอนข้างต้น ระบบจะปลดคิวเมื่อยืนยันได้ว่าเป็นเครื่องเดิมและ boot session เปลี่ยนไป จากนั้นจึงดำเนินงานที่รอตาม FIFO การ restart Backend เพียงอย่างเดียวไม่ยืนยันว่า process เดิมหยุดแล้ว

หากระบบอ่านหรือจับคู่ machine identity ไม่ได้ คิวจะยังถูกระงับ แม้รีสตาร์ตเครื่องแล้ว ปัจจุบันยังไม่มีวิธีปลดคิวผ่าน UI หรือวิธียืนยันการหยุด execution ด้วยตนเองในระบบ

## ทดสอบ

ผลตรวจและขอบเขตการ validation อยู่ใน [Verification status](docs/verification.md)

- Backend: รัน `go test -race ./...` ใน `backend`; ตั้ง `TEST_DATABASE_URL` ไปยัง PostgreSQL **สำหรับทดสอบโดยเฉพาะ** เพื่อรัน integration tests ซึ่งสร้าง schema ชั่วคราวและลบทิ้งหลังทดสอบ
- Frontend: รัน `npm test` และ `npm run build` ใน `frontend`
- Codex smoke test เป็น opt-in: `CODEX_SMOKE_TEST=1 go test ./internal/codex -run TestAppServerTurn` ใน `backend`; ตั้ง `PIXEL_CODEX_MODEL` เมื่อจำเป็น การทดสอบใช้ account และอาจใช้ quota
- Codex subagent smoke test เป็น opt-in: `CODEX_SMOKE_SUBAGENT=1 go test ./internal/codex -run TestAppServerSubagent` ใน `backend`; ทดสอบด้วย prompt สังเคราะห์ใน directory ชั่วคราวและอาจใช้ quota
- ทดสอบ Subagent ผ่าน HTTP และ PostgreSQL: ตั้ง `TEST_DATABASE_URL`, `CODEX_SMOKE_TEST=1` และ `CODEX_SMOKE_SUBAGENT=1` แล้วรัน `go test ./internal/server -run TestHTTPCodexTaskEndToEnd` ใน `backend`; ใช้ schema และ project ชั่วคราวและอาจใช้ quota
- ทดสอบ Workflow สองขั้นกับ Codex จริง: ตั้ง `TEST_DATABASE_URL` และ `CODEX_WORKFLOW_SMOKE=1` แล้วรัน `go test ./internal/server -run TestWorkflowHTTPCodexHandoff` ใน `backend`; ใช้ schema และ project ชั่วคราวและอาจใช้ quota
- OAuth runtime smoke: `AGY_OAUTH_SMOKE=1 go test ./internal/antigravity -run TestOAuthContainerTurn` ใช้ AI Pro quota ใน project ชั่วคราว และตรวจ result กับ cancellation ระหว่าง container เริ่มทำงาน
- Mixed workflow smoke: ตั้ง `TEST_DATABASE_URL`, `PIXEL_CODEX_MODEL` เมื่อจำเป็น และ `AGY_OAUTH_WORKFLOW_SMOKE=1` แล้วรัน `go test ./internal/server -run TestWorkflowHTTPMixedOAuthHandoff`; ตรวจ HTTP → PostgreSQL → Codex → Antigravity จริงด้วยข้อความสังเคราะห์
- App startup smoke: ตั้ง `TEST_DATABASE_URL` และ `APP_RUNTIME_SMOKE=1` แล้วรัน `go test ./cmd/server -run TestConfiguredOAuthProviderInRunningApp`; ใช้ port 8080 และ schema ชั่วคราว ตรวจ provider API กับ static frontend โดยไม่ส่ง prompt
- Antigravity stream smoke test เป็น opt-in: `AGY_SMOKE_TEST=1 go test ./internal/antigravity -run TestRealStream` ใน `backend`; ใช้ prompt สังเคราะห์และอาจใช้ quota

ทดสอบ HTTP → PostgreSQL → worker → Codex จริงสำหรับงานที่ไม่ใช้เครื่องมือแล้ว รวมถึง Codex App Server smoke tests สำหรับ interrupt และปฏิเสธ approval; ทดสอบ browser-to-Codex flow ด้วย prompt ที่ไม่ใช้เครื่องมือบน project และ schema ชั่วคราวแล้ว; ทดสอบ browser flow สำหรับ cancellation และปฏิเสธ approval กับ Codex จริงแล้ว โดย approval test ใช้ policy แบบ untrusted เพื่อบังคับให้เกิดคำขอ การกู้คิวหลังเปลี่ยน boot session ทดสอบด้วย identity จำลอง ยังไม่ได้ทดสอบโดยรีสตาร์ตเครื่องจริง
