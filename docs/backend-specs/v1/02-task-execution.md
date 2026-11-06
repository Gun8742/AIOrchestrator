# V1 — Codex Task Execution และ Cancellation

## Problem Statement

ผู้ใช้ต้องสั่ง Codex ทำงานในโปรเจกต์และรู้ผลจริง โดยไม่ให้งานหลายงานแก้ไฟล์โปรเจกต์เดียวกันพร้อมกัน

## Solution

Backend เก็บ task จัดคิวต่อโปรเจกต์ เริ่ม Codex ผ่าน programmatic integration และรายงานผลหรือข้อผิดพลาด รวมถึงรองรับการขอหยุดงาน

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการส่ง prompt และเลือกโปรเจกต์เพื่อเริ่มงาน
2. ในฐานะผู้ใช้ ฉันต้องการเห็น task ID และสถานะเริ่มต้นเพื่ออ้างอิงงาน
3. ในฐานะผู้ใช้ ฉันต้องการให้งานในโปรเจกต์เดียวกันรอคิวเพื่อหลีกเลี่ยงการแก้ไฟล์ชนกัน
4. ในฐานะผู้ใช้ ฉันต้องการดูผลลัพธ์และข้อผิดพลาดเพื่อประเมินงาน
5. ในฐานะผู้ใช้ ฉันต้องการยกเลิกงานในคิวเพื่อไม่ให้เริ่มทำงาน
6. ในฐานะผู้ใช้ ฉันต้องการขอหยุดงานที่กำลังทำเพื่อควบคุมการทำงาน
7. ในฐานะผู้ใช้ ฉันต้องการรู้ว่าการยกเลิกไม่ย้อนการแก้ไฟล์ เพื่อจะตรวจการเปลี่ยนแปลงเอง
8. ในฐานะผู้ใช้ ฉันต้องการได้รับ error เมื่อ Codex หรือ authentication ใช้ไม่ได้ เพื่อแก้ปัญหาก่อนลองใหม่
9. ในฐานะผู้ใช้ ฉันต้องการทราบว่า service หยุดระหว่างงาน เพื่อไม่เข้าใจว่างานยังทำอยู่
10. ในฐานะผู้ใช้ ฉันต้องการให้งานที่เริ่มแล้วไม่ถูกรันซ้ำหลัง restart และให้งานที่ยังไม่เริ่มทำงานตาม FIFO เมื่อ project ปลอดภัย

## Implementation Decisions

- `POST /api/tasks` รับ `{projectId, prompt}`; prompt ต้องไม่ว่างหลัง trim และต้องกำหนด size limit ใน implementation คืน 201 พร้อม task ID และ `queued` หลังบันทึกสำเร็จ; ไม่รับ arbitrary shell command
- `GET /api/tasks` คืนรายการเรียงตามเวลาสร้างล่าสุด; `GET /api/tasks/:id` คืน snapshot, result/error และ events ตามลำดับ
- Task มี ID, projectId, prompt, status, createdAt, updatedAt, result และ error; timestamp เป็น UTC แบบ ISO 8601. Status คือ `queued`, `running`, `waiting_for_user`, `completed`, `failed`, `cancelled`
- Transition: queued → running/cancelled; running → waiting_for_user/completed/failed/cancelled; waiting_for_user → running/failed/cancelled. Terminal state ไม่เปลี่ยน; บันทึก status กับ event อย่างเป็น atomic เดียวกัน
- FIFO ต่อโปรเจกต์ตามลำดับที่ Backend บันทึก task สำเร็จ โดยใช้ลำดับคิวถาวรที่ไม่ซ้ำแทน timestamp; มี active task ได้สูงสุดหนึ่ง task ต่อ project รวมสถานะ waiting_for_user; ต่างโปรเจกต์รันพร้อมกันได้ งานในคิวไม่ใช้ slot
- `POST /api/tasks/:id/cancel` คืน snapshot ล่าสุด: queued ยกเลิกทันที; active ส่งคำขอหยุดและคงสถานะเดิมจน runtime ยืนยันการหยุด จึงเป็น cancelled; หากเสร็จก่อนให้คืนสถานะจริง การเรียกซ้ำบน terminal state คืน snapshot เดิม
- เมื่อ runtime ปิดผิดปกติหรือหยุดไม่สำเร็จ ให้บันทึก `failed` พร้อมเหตุผล ไม่อ้างว่าหยุดสำเร็จ; หากยืนยันไม่ได้ว่า execution เดิมหยุดแล้ว ให้ project เป็น `blocked` และไม่เริ่ม task ถัดไป แม้ task เดิมเป็น terminal แล้ว
- เมื่อ restart: task ที่เคยเริ่มแล้วเป็น `failed` ด้วยเหตุ service interrupted และไม่ replay; กู้ queue ของ task ที่ยังไม่เริ่มและรันต่ออัตโนมัติตาม FIFO หลังยืนยันว่า execution เดิมหยุดแล้วเท่านั้น หากตรวจไม่ได้ให้ project เป็น `blocked` และแจ้งผู้ใช้; เมื่อ runtime ยืนยันได้ภายหลังว่าหยุดแล้วให้ปลด block และเดินคิวต่อ
- เลือก Codex SDK, App Server หรือ CLI หลังพิสูจน์ authentication, lifecycle, cancellation, event และ approval; ใช้ sandbox/approval ที่ runtime รองรับโดยไม่ bypass ข้อจำกัด
- 400 สำหรับ input ไม่ถูกต้อง, 404 สำหรับ resource ไม่พบ, 409 สำหรับ operation ที่ขัดกับสถานะ, 500 สำหรับ storage/internal failure; ไม่คืน stack trace หรือ credential

## Testing Decisions

- ทดสอบผ่าน HTTP API ด้วย Agent runtime จำลองที่ควบคุม completion, failure และ cancellation; ใช้ store จริงและ temporary project; ไม่มี prior art
- ตรวจ validation, FIFO รวม restart และ submission ระหว่าง recovery, concurrency ข้ามโปรเจกต์, task isolation, cancel queued/active/repeated, race ระหว่าง cancel กับ completion, failure และ blocked project ที่ปลดได้หลังยืนยัน process หยุด
- เพิ่ม integration smoke test แบบ opt-in กับ Codex จริงเพื่อตรวจ capability และผลลัพธ์ ไม่พึ่ง provider สำหรับ test suite ปกติ

## Out of Scope

แก้ไข prompt หลังเริ่ม, retries อัตโนมัติ, resume งาน, rollback ไฟล์, remote workers, และ Agent ค่ายอื่น

## Further Notes

การยืนยัน process หยุดต้องอาศัย identity และ lifecycle ที่ integration รองรับ ห้ามใช้ PID อย่างเดียวเป็นหลักฐานเพราะ PID อาจถูกนำกลับมาใช้ หากยืนยันไม่ได้ต้องคง block และอธิบายวิธีแก้; รายละเอียดการตรวจและการนำสถานะ blocked ออกเมื่อแก้แล้วต้องพิสูจน์ด้วย prototype
