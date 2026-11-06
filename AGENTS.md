# AGENTS.md

## Project

Pixel Agent Office เป็นเว็บ Local-first สำหรับมอบหมายและติดตาม Coding Agents ผ่านออฟฟิศ 2D แบบ Pixel Art โดย MVP เริ่มจาก Codex บนเครื่องผู้ใช้

- ตอบผู้ใช้เป็นภาษาไทย กระชับ และคง Technical Terms ที่จำเป็น
- ก่อนแก้ไข ตรวจโครงสร้าง โค้ดและรูปแบบที่มีอยู่ รวมถึง callers และ contracts ที่ได้รับผลกระทบ
- รักษา unrelated changes และรายงานสิ่งที่ตรวจจริง ข้อจำกัด และสมมติฐานที่มีผลต่อความถูกต้อง

## Sources of Truth

- ก่อนเปลี่ยนโครงสร้างระบบ, deployment หรือ integration ให้อ่าน [Architecture](docs/architecture.md)

- ก่อนทำ Backend ให้อ่าน [Backend Feature Specifications](docs/backend-specs/README.md) และ spec ของ feature/version ที่เกี่ยวข้อง
- ก่อนทำ Frontend ให้อ่าน [Frontend Feature Specifications](docs/frontend-specs/README.md) และ spec ของ feature/version ที่เกี่ยวข้อง
- Feature spec มีรายละเอียดมากกว่าเอกสารภาพรวม; V1 เป็น MVP ส่วน V2 เป็น roadmap ที่ต้องตรวจ provider capabilities และ API ก่อน implementation
- ตรวจข้อที่ยังเปิดและ contract ระหว่าง Frontend กับ Backend ก่อนเริ่มงาน หากความไม่ชัดเจนมีผลต่อ correctness ให้ระบุและแก้ข้อตกลงใน spec ร่วมกัน
- ตรวจ versions, dependencies และคำสั่งจาก `backend/go.mod`, `frontend/package.json`, lockfiles และ scripts ที่มีจริง; PostgreSQL และ Docker เป็นตัวเลือกที่ผู้ใช้กำหนด

## Domain and Safety

- ใช้ PostgreSQL สำหรับ persistence และ Docker Compose สำหรับ local development; จัดเก็บข้อมูล database ใน persistent volume และห้าม commit credentials
- Codex ต้องเข้าถึง project และ runtime บนเครื่องผู้ใช้; ตรวจ boundary ก่อนตัดสินใจ containerize Backend ไม่สมมติว่า container เข้าถึง host ได้เอง

- Backend เป็นแหล่งข้อมูลของ task status, events, results และ approval; UI และ animation แสดงข้อมูลที่ได้รับจริง ไม่สร้าง progress หรือ Subagent ขึ้นเอง
- ตรวจ task lifecycle, cancellation, restart recovery และ event ordering ตาม feature spec; การ cancel ไม่ย้อนการแก้ไฟล์ที่เกิดขึ้นแล้ว
- จำกัด active task ต่อ project ตาม V1 spec และตรวจ race ระหว่าง execution, cancellation และ approval
- Local service ต้องตรวจการเข้าถึงตาม Backend spec; project path เป็น working directory ไม่ใช่ filesystem sandbox
- ตรวจ input ที่ trust boundary ใช้ sandbox และ approval ของ runtime ตาม capability จริง และไม่อนุมัติแทนผู้ใช้
- เก็บ credentials นอก source control และไม่ส่งหรือบันทึก token, auth payload หรือ secrets ที่ทราบลงใน client หรือ logs; prompt และ output อาจมีข้อมูลอ่อนไหว
- ตรวจ capability และเงื่อนไขของ Codex หรือ provider จากเอกสาร/เวอร์ชันที่ใช้งานจริงก่อนพึ่งพา API, event, approval หรือ cancellation

## Implementation and UI

- ใช้โครงสร้างและ utilities ที่มีอยู่ก่อน เพิ่ม abstraction หรือ dependency เมื่อจำเป็นต่อ behavior ที่ต้องส่งมอบ
- แยก orchestration จาก validation, business rules และ persistence; ใช้ named functions สำหรับพฤติกรรมที่มีความหมาย และรักษา Type Safety
- เมื่อเปลี่ยน API, event หรือ task state ให้ตรวจ callers, tests และ specs ทั้งสองฝั่ง; ไม่เปลี่ยน public contract โดยไม่จัดการ compatibility
- แสดง loading, empty, error, connection และ approval states ตามผลจริง; เมื่อผล request ไม่แน่นอน ให้ตรวจสถานะล่าสุดก่อน retry action ที่อาจสร้างผลซ้ำ
- UI หลักต้องใช้ keyboard และ screen reader ได้ มีข้อความระบุสถานะนอกเหนือจากสี รองรับจอแคบและ reduced motion

## Verification and Completion

- ตรวจคำสั่งจาก config และ scripts ที่มีจริง แล้วรัน formatter, lint, type check, tests และ build ที่เกี่ยวข้อง; หากยังไม่มีเครื่องมือ ให้ระบุสิ่งที่ยังไม่ได้ตรวจ
- Backend: ทดสอบ behavior ผ่าน HTTP API เป็นหลัก โดยใช้ Agent runtime จำลองและ persistent store ตามความเหมาะสม; smoke test กับ provider จริงเป็น opt-in
- Frontend: ทดสอบ user flows ผ่าน UI ด้วย Backend responses จำลอง และตรวจ integration กับ Backend จริงเมื่อพร้อม; ตรวจ keyboard, error states และ layout ที่เปลี่ยน
- เพิ่ม regression test สำหรับ behavior ที่มีความเสี่ยง โดยเฉพาะ task transitions, cancellation races, recovery, access control และ event reconciliation
- ส่งมอบเมื่อ callers และเอกสารที่ได้รับผลกระทบสอดคล้องกัน และรายงานผลตรวจตามจริง
