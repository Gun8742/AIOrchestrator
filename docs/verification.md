# Verification status — 2026-10-30

บันทึกหลักฐานการตรวจตาม feature specs และขอบเขตการ validation ที่ใช้ส่งมอบ ใช้ร่วมกับ [Architecture](architecture.md) และ [Antigravity integration notes](backend-specs/v2/antigravity-integration-notes.md)

## หลักฐานปัจจุบัน

| ขอบเขต | หลักฐานที่ตรวจ |
| --- | --- |
| Local service, session, project registry | `TestLocalSessionBoundaries`, `TestProjectPersistence`, `TestCanonicalDirectory`, `TestStaticFrontendKeepsHostAndOriginChecks`; source อยู่ใน `backend/internal/server` และ `backend/internal/store` |
| Task lifecycle, FIFO และ recovery | `TestHTTPTaskCancellationWithDatabase`, `TestTaskSnapshotAndFIFO`, `TestWorkerRunsDifferentProjectsConcurrentlyAndKeepsProjectFIFO`, `TestProviderSchedulingPreservesFIFOWithoutFallback`, `TestCancellationDuringCodexInitialization` |
| Events, snapshots และ redaction | `TestEventStreamReadyBeforeCommittedEvents`, `TestConcurrentSnapshotsAndClaims`, `TestHTTPResultEventIsRedactedAndOrdered`, `TestTerminalOutcomeEventsAreStoredWithTask` และ Frontend Reconciler tests |
| Atomic rollback | `TestOutcomeFailureRollsBackTaskEventsAndWorkflow` บังคับให้ INSERT result event ล้มเหลวใน PostgreSQL schema ชั่วคราว; task ยัง running, ไม่มี result/event ที่หลุดออกมา, watermark ไม่เปลี่ยน, workflow ยังไม่สร้างขั้นถัดไป แล้ว retry persistence สำเร็จโดยใช้ sequence ต่อจาก commit เดิม |
| Approvals | `TestHTTPApprovalFlowWithDatabase`, `TestApprovalHTTPRequiresMatchingPendingRequest`, `TestApprovalResponseIsForwardedOnce`, `TestIncompleteApprovalCannotBeApproved`; pending approval snapshot มีเวลาที่ร้องขอ และ UI แสดง field นี้แล้ว |
| Workflow | `TestWorkflowHTTPCreateAndCancel`, `TestWorkflowHTTPHandoffEndToEnd`, `TestWorkflowCancelRacesCompletion`, `TestWorkflowRecoveryStopsInterruptedStepAndPreservesQueuedWork`, `TestWorkflowRejectsOversizedHandoffWithoutBlockingQueue` |
| Migration | `TestMigrationPreservesV1TasksAndQueueOrder`, `TestProviderMigrationPreservesExistingTask` และ container identity store tests |
| OAuth runtime จริง | ผู้ใช้ Login และยืนยัน persistence ผ่านแล้ว; `TestOAuthContainerTurn` ผ่านกับบัญชี AI Pro ทั้งผลสังเคราะห์และ cancellation ระหว่างเริ่ม container; ไม่มี API key และตั้งเครดิตเพิ่มปิดอยู่ |
| Mixed provider จริง | `TestWorkflowHTTPMixedOAuthHandoff` ผ่าน HTTP → PostgreSQL → Codex → Antigravity ด้วยคำสั่งสังเคราะห์ที่ไม่ใช้ tools |
| Application binary | `TestConfiguredOAuthProviderInRunningApp` ผ่านกับ schema ชั่วคราว: bootstrap session, Antigravity availability/capabilities และ static frontend พร้อม CSP โดยไม่ส่ง prompt |
| Container lifecycle | Docker fixtures ตรวจ handshake, model mismatch, subprocess cancellation, ownership, OAuth volume permissions/persistence และการปฏิเสธลบ container ที่ running; post-commit cleanup ไม่ทำงานเมื่อ DB commit ล้มเหลวหรือ execution ยังไม่ยืนยัน |
| Frontend regression | `npm test`: 12 tests ผ่าน; `npm run build`: TypeScript และ production build ผ่านหลังเพิ่ม Approval timestamp |

Backend suite ผ่านด้วย `go test -race ./... -count=1` พร้อม PostgreSQL และ `DOCKER_CONTAINMENT_TEST=1` หลังต่อ production OAuth provider จากนั้นเพิ่ม rollback test และรันเฉพาะกรณีใหม่ผ่านแล้ว ไม่ได้ส่ง source code หรือ credentials ให้ provider ใน smoke tests ข้างต้น

## Browser checks ในรอบนี้

ใช้ HTTP/SSE fixture ที่มีข้อมูลสังเคราะห์ทั้งหมด ไม่ใช่ข้อมูลบัญชีจริง:

- Project path ผิดแสดง error และคง path ที่กรอกไว้
- Project path ซ้ำแสดงข้อความเฉพาะ แล้วแก้ input เพื่อเพิ่ม project ใหม่ได้และเลือก project ที่สร้าง
- Double click ส่งงานขณะ request ค้าง: fixture นับได้หนึ่ง POST และหนึ่ง task ใหม่
- จำลอง commit task แล้วตัด connection ก่อนคืน response: UI คง prompt/project, แจ้งผลไม่แน่นอน, แสดง task ที่บันทึกผ่าน SSE และไม่ retry POST อัตโนมัติ
- Approval panel แสดงเวลาที่ Backend ส่ง (`createdAt`) ใน timezone ของ browser พร้อม task/project และปุ่มตอบคำขอ

รอบก่อนหน้าตรวจ provider unavailable/capabilities, keyboard เลือกตัวละคร, waiting marker, session expiry และข้อความยาวที่ 375px แล้ว รายละเอียดขอบเขตอยู่ใน README และ integration notes ไม่ได้รันซ้ำทั้งหมดในรอบแก้ timestamp

## Browser lifecycle checks เพิ่มเติม — 2026-10-30

ใช้ fixture HTTP/SSE ที่ควบคุม state ได้ ไม่มีคำขอไป provider:

- Workflow failed แสดง error ของขั้นแรก และขั้นถัดไปเป็น “ยังไม่เริ่ม” โดยไม่มี task ที่คาดเดาขึ้น
- Cancel แข่งกับ completion: ปุ่มแสดง pending ระหว่างรอ แล้วใช้ snapshot ที่รายงาน completed พร้อมผลขั้นสุดท้าย แทนการอ้างว่า cancelled; counter ยืนยันหนึ่ง cancel POST ต่อการกระทำ
- SSE/API หลุดขณะ workflow running: UI แสดงข้อมูลอาจเก่าและปิด controls; หลังเชื่อมต่อกลับ โหลด snapshot ที่เปลี่ยนเป็น completed พร้อมผลที่เกิดระหว่าง offline ได้
- Pending Approval ยังปรากฏหลัง reload แล้วเลือก task เดิม รายละเอียดและเวลายังตรงกับ snapshot; double click อนุมัติส่งเพียงหนึ่ง POST
- Approval 409, 404 และ connection ขาดหลัง commit: โหลด snapshot ใหม่ ลบปุ่มของคำขอเก่า แสดง error ที่เหมาะสม และไม่ตอบซ้ำเอง Counter หลังสี่การกระทำเท่ากับสี่ POST
- พบและแก้ focus ที่หลุดไปต้นหน้าเมื่อปุ่ม cancel/approval ถูกถอดออก: ส่ง focus กลับรายละเอียดเฉพาะ record เดิม; keyboard Enter ทดสอบผ่าน และเมื่อผู้ใช้ย้ายไป textarea ระหว่างรอ response focus ยังคงอยู่ใน textarea
- Workflow status และ connection status ประกาศผ่าน `role=status`/`aria-live=polite`; workflow region มี accessible name และ focus target แยกจาก controls ตรวจ DOM และ browser AX tree แล้ว

Frontend tests 12 รายการและ TypeScript/production build ผ่านหลังแก้ focus ไม่มีการเปลี่ยน Backend หรือเรียก model quota ในรอบนี้

## ขอบเขต accessibility และการส่งมอบ

- ตรวจ keyboard, accessible names/roles, focus และ polite live-region content ผ่าน DOM/AX tree จริงของ browser แล้ว ไม่ได้ฟังหรือทดสอบ VoiceOver engine จึงไม่อ้างว่าได้ทำการตรวจเสียง
- VoiceOver ไม่ได้เป็น dependency ของเว็บหรือ release gate ที่ระบุใน feature specs; การตั้งเป็น blocker ก่อนหน้านี้เป็น gate ที่ agent เพิ่มเอง ข้อกำหนด screen-reader support ยังคงเดิม
- จับคู่ User Stories ทั้ง 94 ข้อและหลักฐานไว้ใน [Completion audit](completion-audit.md)

## Directory permission และ session boundary — final audit

เพิ่ม `TestCanonicalDirectoryRequiresReadAndSearchPermission` ซึ่งล้มก่อนแก้สำหรับ permission 0000/0400/0100 แล้วผ่านหลังแก้ helper ให้ตรวจ read/search access จริง ใช้ helper เดียวกันใน project registration และก่อน worker execution; `TestProjectHTTPRejectsInaccessibleDirectory` ยืนยัน HTTP 400 สำหรับ path ที่เข้าไม่ได้

เพิ่มตรวจ bootstrap secret หมดอายุ, HttpOnly/SameSite=Strict cookie และการปฏิเสธ API/SSE เมื่อไม่มี session หรือใช้ session ไม่รู้จัก โดยไม่เปลี่ยน authentication contract

Claude ถูกเลื่อนตามคำขอผู้ใช้ ไม่รวมใน provider ที่เปิดใช้ Antigravity ไม่มี interactive approval ผ่านเว็บตาม capability ที่ประกาศ และไม่รับประกันว่า cancellation จะย้อนการแก้ไฟล์หรือ side effects ภายนอก container

Final Backend run ผ่านทั้ง 6 packages ด้วย race detector, PostgreSQL, Docker fixtures และ app startup smoke หลังแก้ permission; ไม่มี failure และ permission tests/startup smoke ไม่ถูก skip ไม่ได้เปิด opt-in model smoke ซ้ำในรอบนี้เพราะไม่มีการเปลี่ยน provider protocol
