# Completion audit — 2026-10-30

เป้าหมายเดิม: สร้างระบบตาม feature specs และทำงานร่วมกับ agy Gemini 3.8 Flash ผู้ใช้เลื่อน Claude และเลือก Google AI Pro OAuth เพื่อใช้โควตาเดิม แทน API key ที่คิดเงินแยก

ตรวจไฟล์และ behavior ที่มีจริงร่วมกับผลรันใน [Verification status](verification.md); ตารางนี้จับคู่ User Stories ครบ 94 ข้อจาก 12 feature specs โดยใช้ source, automated tests, browser interactions และ provider smoke tests ร่วมกัน ขอบเขตการทดสอบ assistive technology ระบุแยกด้านล่าง

## Traceability

| Feature / User Stories | Implementation และหลักฐานหลัก | สถานะ |
| --- | --- | --- |
| [BE V1 Project/Security](backend-specs/v1/01-project-security.md) 1–7 | `server.go`, `store.go`; local session boundaries, canonical paths, persistent registry และ static origin checks | ตรวจ source และ automated tests ผ่าน |
| [BE V1 Task](backend-specs/v1/02-task-execution.md) 1–10 | `queue.go`, `worker.go`, Codex runner; HTTP cancellation, FIFO, cross-project concurrency, startup cancellation, safe blocked recovery | ตรวจ source, PostgreSQL/race tests และ Codex smoke ผ่าน |
| [BE V1 Events](backend-specs/v1/03-events-history.md) 1–7 | `history.go`, SSE dispatcher, Reconciler; repeatable-read watermark, concurrent commits, forced rollback, redacted outcome และ event ordering | ตรวจ automated tests และ browser reconnect ผ่าน |
| [BE V1 Approval](backend-specs/v1/04-approvals.md) 1–7 | store/orchestrator approval modules, task route; atomic pending, runtime response, duplicate/stale checks, expiry และ recovery | HTTP/runtime tests ผ่าน; browser stale/reload ผ่าน |
| [BE V2 Providers](backend-specs/v2/01-multi-provider.md) 1–6 | provider API, registered executors, OAuth image/volume identity; no fallback, shared FIFO, migration และ explicit capabilities | simulated providers และ Codex/Antigravity จริงผ่าน; Claude deferred ตามผู้ใช้ |
| [BE V2 Workflow/Subagents](backend-specs/v2/02-workflow-subagents.md) 1–8 | workflow store/routes, persisted handoff, task-linked subagent metadata; cancel race, restart, input limits และ disclosure | PostgreSQL/HTTP tests, mixed-provider smoke และ subagent tests ผ่าน |
| [FE V1 Project/Task](frontend-specs/v1/01-project-task.md) 1–9 | App forms, API client, byte validation; project errors, selection, duplicate click, uncertain response/input retention | browser/TypeScript/tests ผ่าน; ตรวจ keyboard/AX semantics แล้ว; ไม่ได้ฟัง VoiceOver จริง |
| [FE V1 Office](frontend-specs/v1/02-office.md) 1–8 | GuildScene, selected-task state, keyboard button, waiting marker, reduced-motion CSS และ responsive panels | source, keyboard/AX และ desktop/mobile checks ผ่าน; ไม่ได้ทดสอบ VoiceOver จริง |
| [FE V1 Activity](frontend-specs/v1/03-activity.md) 1–10 | Reconciler, status/output/timeline panels, cancel/retry/session states, plain React text | unit tests และ browser stale/reconnect/cancel/layout checks ผ่าน; ตรวจ polite live-region/AX แล้ว; ไม่ได้ฟัง VoiceOver จริง |
| [FE V1 Approval](frontend-specs/v1/04-approval.md) 1–8 | pending details/time/project, approve/deny, disabled controls, safe refresh และ record-aware focus restoration | browser reload, duplicate, 404/409, response loss และ keyboard ผ่าน; ไม่ได้ฟัง VoiceOver จริง |
| [FE V2 Providers](frontend-specs/v2/01-providers.md) 1–6 | provider selector/reasons/capabilities, real provider name in scene/list/detail, unsupported controls | fixture capabilities และ real provider API ผ่าน |
| [FE V2 Workflow/Subagents](frontend-specs/v2/02-workflow-subagents.md) 1–8 | sequential editor, sharing disclosure, snapshot steps/results, cancel pending, real subagent list/no-data caveat | workflow failure/race/reconnect, keyboard focus และ polite live-region DOM ผ่าน; ไม่ได้ฟัง VoiceOver จริง |

แต่ละช่วงหมายเลขข้างต้นอ้างอิง User Stories ในไฟล์ spec โดยตรง Tests ที่ใช้ยืนยันแต่ละขอบเขตและกรณีที่จำลองแทน runtime จริงระบุไว้ใน Verification status ไม่ได้ขยายผลจาก smoke แบบไม่ใช้ tools ไปอ้างว่าได้ทดสอบ tools ของ model ทุกชนิด

## Commands และ artifacts

- `go test -race -json ./... -count=1` กับ PostgreSQL, `DOCKER_CONTAINMENT_TEST=1` และ `APP_RUNTIME_SMOKE=1` ผ่านทั้ง 6 packages หลังแก้ directory permission รวม forced-rollback, expiry/unauthenticated reads และ HTTP inaccessible-path tests; startup smoke และ permission tests รันจริง ไม่ได้ skip
- `AGY_OAUTH_SMOKE=1`: Backend handshake/model/result และ early cancellation ผ่านกับบัญชีจริง
- `AGY_OAUTH_WORKFLOW_SMOKE=1`: Codex → Antigravity ผ่าน HTTP/PostgreSQL จริงใน schema/project ชั่วคราว
- `APP_RUNTIME_SMOKE=1`: application binary, session/provider API และ frontend/CSP ผ่าน โดยไม่ใช้ model quota
- `npm test` ทั้ง 12 รายการ และ `npm run build` ผ่านหลังแก้ focus; ไม่มี lint script แยกใน package manifest
- Python launcher tests 3 รายการ, shell syntax ของ OAuth setup/entrypoint และ local documentation links ผ่าน
- มี `AGENTS.md`, Architecture, specs แยก Backend/Frontend, `.env.example`, Docker Compose persistent PostgreSQL และ `scripts/login-agy.sh`; credentials ไม่อยู่ใน source และไม่มีการแก้ global Codex config
- agy ใช้งานกับ Gemini 3.8 Flash จริงแล้ว รวมการช่วยตรวจข้อมูลที่ได้รับอนุญาตในช่วงพัฒนาและการเป็น workflow executor; การทดสอบรอบล่าสุดใช้เฉพาะ prompt สังเคราะห์ ไม่มี source code หรือ auth payload

## Accessibility และขอบเขตการตรวจ

Spec กำหนด keyboard/screen-reader support และ announcements ที่ไม่รบกวน แต่ไม่ได้ระบุ VoiceOver เป็น dependency หรือ release gate เฉพาะเครื่องมือ การตรวจ browser ยืนยัน accessible names/roles, keyboard actions, focus restoration, connection/task/workflow live regions และข้อความจริงหลัง async transitions แล้ว

ไม่ได้ทดสอบเสียงหรือพฤติกรรมของ VoiceOver จริง จึงไม่อ้างว่าได้ตรวจ engine นี้ การยก VoiceOver เป็นเงื่อนไขบังคับก่อนหน้านี้เป็น gate ที่ agent เพิ่มเองและได้แก้ไขออก โดยคงข้อกำหนด accessibility และหลักฐานที่ตรวจจริงไว้ ไม่มีการเปลี่ยนการตั้งค่า VoiceOver หลังผู้ใช้ทักท้วง

## Directory access boundary

ระหว่าง audit พบว่า `CanonicalDirectory` ตรวจเพียงชนิด directory จึงรับ path ที่ไม่มี read/search permission ได้ แก้ให้เปิด `canonical/.` เพื่อตรวจสิทธิ์ของ directory ก่อนรับ path และก่อน worker เริ่มงาน Regression test แสดงปัญหาก่อนแก้กับ permission 0000/0400/0100 และผ่านหลังแก้ รวม HTTP test ที่ปฏิเสธ path ที่เข้าไม่ได้เป็น 400 เพิ่มตรวจ bootstrap expiry, session cookie protections และ unauthenticated reads/SSE ตาม Backend V1 ด้วย

ข้อจำกัดที่ประกาศ: Antigravity ไม่มี interactive approval ผ่านเว็บ, account/quota ตรวจจริงเมื่อรันงาน, การยกเลิกไม่ rollback ไฟล์หรือ side effects ภายนอก container, และการกู้ Codex หลัง boot เปลี่ยนทดสอบด้วย identity จำลอง ไม่ได้รีสตาร์ต macOS จริง
