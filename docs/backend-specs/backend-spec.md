# Backend Specification

## เป้าหมาย

จัดการงาน Coding Agent บนเครื่องผู้ใช้ ส่งสถานะจริงให้ Frontend และเก็บประวัติที่เพียงพอสำหรับตรวจผลและกู้สถานะหลัง restart

## ขอบเขต MVP

- รันเป็น local service สำหรับผู้ใช้คนเดียวบน macOS และ bind เฉพาะ loopback
- รับรายการโปรเจกต์ที่ผู้ใช้ระบุและตรวจสอบ path ก่อนใช้งาน
- สร้างงานให้ Codex ผ่าน integration ที่รองรับแบบ programmatic โดยตรวจสอบ version, authentication, event และ approval ที่รองรับก่อนเลือกใช้ SDK, App Server หรือ CLI
- บันทึกคำสั่ง สถานะ เหตุการณ์ ผลลัพธ์ และข้อผิดพลาดลง local storage แบบ persistent
- ส่ง snapshot ผ่าน HTTP และ stream เหตุการณ์ผ่าน SSE
- ให้หยุดงานที่กำลังทำงาน และจัดการ process/resource เมื่อเสร็จหรือยกเลิก
- หาก integration รองรับ interactive approval ให้ส่งคำขอและคำตอบผ่าน UI; หากไม่รองรับ ต้องรายงานข้อจำกัดและ failure อย่างชัดเจน ห้ามอนุมัติแทนผู้ใช้

## Workflow

1. ผู้ใช้เลือกโปรเจกต์ที่อนุญาตและส่งคำสั่ง
2. Backend ตรวจ input สร้าง task และบันทึก `queued`
3. เริ่ม Codex ใน project directory ที่เลือก จากนั้นบันทึก `running` และเหตุการณ์ที่ได้รับ
4. เมื่อมีคำขออนุมัติที่ส่งต่อได้ เปลี่ยนเป็น `waiting_for_user` จนกว่าจะได้รับคำตอบหรือเกิด timeout/error
5. บันทึกผลสุดท้ายเป็น `completed`, `failed` หรือ `cancelled` พร้อมรายละเอียดที่มี

MVP อนุญาตงานที่กำลัง execute เพียงหนึ่งงานต่อโปรเจกต์เพื่อหลีกเลี่ยงการแก้ไฟล์ชนกัน งานถัดไปรอใน queue การยกเลิกไม่ย้อนการแก้ไฟล์ที่เกิดขึ้นแล้ว ต้องแสดงข้อเท็จจริงนี้ในผลลัพธ์

## Task และ lifecycle

Task มี `id`, `projectId`, `prompt`, `status`, `createdAt`, `updatedAt`, ผลลัพธ์หรือข้อผิดพลาด และรายการ events ที่เรียงตามลำดับ แต่ละ event มี `id`, `taskId`, `type`, `timestamp` และ payload ตามประเภท

- `queued` → `running` หรือ `cancelled`
- `running` → `waiting_for_user`, `completed`, `failed` หรือ `cancelled`
- `waiting_for_user` → `running`, `failed` หรือ `cancelled`
- สถานะปลายทางเปลี่ยนไม่ได้; การยกเลิกที่แข่งกับการเสร็จต้องรายงานสถานะสุดท้ายที่บันทึกจริง

เมื่อ service restart ให้ทำเครื่องหมายงานที่เริ่มแล้วเป็น `failed` และไม่รันซ้ำ; งาน queued ที่ยังไม่เริ่มเดินต่อแบบ FIFO เมื่อยืนยันได้ว่า execution เดิมหยุดแล้ว หากยืนยันไม่ได้ให้ block project ตาม feature spec

## API contract

ตารางนี้เป็นภาพรวม; contract และ invariants ฉบับละเอียดอยู่ใน [Project Security](v1/01-project-security.md), [Task Execution](v1/02-task-execution.md), [Events และ History](v1/03-events-history.md), [Approvals](v1/04-approvals.md) และ [Multi-provider V2](v2/01-multi-provider.md)

| Method | Path | หน้าที่ |
| --- | --- | --- |
| POST | `/api/bootstrap` | แลก one-time secret เป็น session และ CSRF token |
| GET | `/api/session` | คืน CSRF token สำหรับ session ที่มีอยู่ |
| GET | `/api/providers` | รายชื่อ provider, availability และ capabilities |
| GET | `/api/projects` | รายการโปรเจกต์ที่อนุญาต |
| POST | `/api/projects` | เพิ่ม local project path ที่ตรวจสอบแล้ว |
| GET | `/api/tasks` | รายการงานและสถานะล่าสุด |
| POST | `/api/tasks` | สร้างงานจาก `projectId`, `prompt` และ `provider` (ค่าเริ่มต้น `codex`) |
| GET | `/api/tasks/:id` | รายละเอียดงานและ events ที่บันทึกไว้ |
| POST | `/api/tasks/:id/cancel` | ขอหยุดงาน; คืนสถานะล่าสุด |
| POST | `/api/tasks/:id/approvals/:approvalId` | ส่ง `{decision: "approve" \| "deny"}` เมื่อ integration รองรับ |
| GET | `/api/events` | SSE สำหรับ task events |

Project แสดง canonical `path`, `queueState` และ `blockedReason` เมื่อมี; task list คืน `{tasks, sequence}` ส่วน task detail คืน `{task, events, sequence}` และ `pendingApproval` เมื่อมีคำขอรออยู่ Event มี `sequence` ที่เรียงตาม commit order; SSE เริ่มด้วย `event: ready` และ payload `{}` ก่อนส่ง task events

การสร้าง task คืน ID และสถานะเริ่มต้น; input ไม่ถูกต้องคืน 400, ไม่พบ resource คืน 404, action ขัดกับสถานะปัจจุบันคืน 409 และ internal failure คืน 500 พร้อมข้อความที่ปลอดภัย การตรวจ Host, Origin, session และ CSRF ใช้ตาม Project Security spec; client ต้องรอ SSE ready ก่อนโหลด snapshot และ reconcile เหตุการณ์ตาม Events spec

## ขอบเขตความปลอดภัย

- จำกัด project path ให้เป็น directory ที่ผู้ใช้เพิ่มและตรวจสอบ canonical path; ไม่รับ shell command จาก input โดยตรง
- ป้องกันเว็บอื่นสั่ง local service โดยตรวจ Origin และใช้ token สำหรับคำขอที่เปลี่ยนแปลงข้อมูล รวมถึงพิจารณาการป้องกัน DNS rebinding ใน implementation
- ใช้สิทธิ์และ sandbox/approval ของ integration อย่างชัดเจน; แสดง action ที่ต้องการอนุมัติและไม่ปิดการป้องกันโดยปริยาย
- ไม่เก็บ credential หรือ token ของ provider ใน task logs และไม่ส่งไป Frontend
- งานหลายตัวอาจแก้ไขไฟล์จริง: แสดง project path และขอบเขตสิทธิ์ก่อนเริ่ม และไม่ทำ destructive Git action หรือ publish อัตโนมัติ

## แนวทางเทคนิคที่เสนอ

ใช้ PostgreSQL สำหรับ persistent store และ Docker Compose สำหรับ local development โดยเก็บข้อมูลใน persistent volume และไม่ commit credentials; ใช้ Go สำหรับ Backend โดยพิสูจน์ Codex integration ก่อน implementation แยกส่วนรับคำสั่ง จัดการ lifecycle และ Codex integration ให้ทดสอบได้ โดยยังไม่สร้าง plugin framework

เริ่มจาก PostgreSQL ใน container และ Backend บน host เป็นแนวทางเสนอ เพื่อให้เข้าถึง Codex และ project บนเครื่องได้ตรงไปตรงมา; การ containerize Backend ต้องพิสูจน์ filesystem, process และ authentication boundary ก่อน

## เกณฑ์ยอมรับ

- ส่งงานไปยัง Codex ที่ตั้งค่าแล้วได้จริงและแสดงผลหรือ error ที่ตรวจสอบได้
- มี event และสถานะตรงกับ lifecycle; reconnect และ restart ไม่ทำให้แสดงงานค้างว่า running โดยไม่มีหลักฐาน
- หยุดงานได้และแสดงผลตามจริง รวมถึงการแก้ไฟล์ที่อาจเกิดก่อนหยุด
- ตรวจ input, project path และการเข้าถึง local service; ไม่เผย credential ใน API หรือ logs
- ทดสอบ lifecycle, cancellation, restart recovery และ approval ตามความสามารถของ integration ที่เลือก

## ระยะถัดไปและข้อที่ต้องพิสูจน์

เพิ่ม Claude Code และ Antigravity โดยให้ Orchestrator ส่งผลลัพธ์จากงานหนึ่งเป็น input ของงานถัดไป; ตรวจ authentication, event, cancellation, approval และเงื่อนไขการใช้งานของแต่ละ integration ก่อนกำหนด contract จริง ตรวจด้วยว่า Codex สามารถส่ง event ของ Subagents ให้ client ในรายละเอียดระดับใด ก่อนสร้างตัวละครตามจำนวน Subagents
