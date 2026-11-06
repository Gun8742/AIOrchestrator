# Workflow API และ persistence — ร่างสำหรับ implementation

เอกสารนี้อธิบาย Workflow API แบบ sequential ที่รองรับ Codex แล้ว; การเปิด provider อื่นต้องผ่าน [Multi-provider V2](01-multi-provider.md) ก่อน

## API

- `POST /api/workflows` รับ `{projectId, steps: [{provider, prompt}, ...]}` ตรวจ project, prompt, จำนวนและขนาด input, provider และ capability ที่ใช้ได้ทุก step ก่อน commit; คืน `201` พร้อม `{id, firstTaskId}` เมื่อบันทึก workflow และสร้าง task ของ step แรกสำเร็จ; โหลด snapshot ด้วย `GET /api/workflows/:id`
- `GET /api/workflows` คืนรายการพร้อม `sequence`; `GET /api/workflows/:id` คืน workflow, ordered steps, task IDs, results/errors ที่บันทึกแล้ว และ `sequence` จาก database snapshot เดียวกัน
- `POST /api/workflows/:id/cancel` บันทึกคำขอยกเลิกและคืน snapshot ล่าสุด; หากมี active task ให้ส่งคำขอหยุดตาม capability ของ provider และรอ terminal state จริง ห้ามอ้างว่ายุติแล้วเพียงเพราะส่งคำขอสำเร็จ; หากบันทึกคำขอแล้วแต่ runtime รับคำสั่งหยุดไม่ได้ ให้คืน `409` และตรวจ snapshot ก่อนลองใหม่
- Workflow snapshot ใช้ status `running`, `completed`, `failed`, `cancelled` และ `cancelRequested` แยกจาก terminal state; step ที่ยังไม่เริ่มไม่มี `taskId` และ `status` ส่วน step ที่เริ่มแล้วอ้าง `taskId` และ task lifecycle จริง (`queued`, `running`, `waiting_for_user`, `completed`, `failed`, `cancelled`) ไม่สร้าง task สำหรับ step ที่ยังไม่ถึง
- Mutation ใช้ session, Origin, Host และ CSRF เดียวกับ V1; ใช้ `400` สำหรับ input ผิด, `404` เมื่อไม่พบ resource, `409` สำหรับ provider ไม่พร้อมหรือ state ขัดแย้ง และ `500` สำหรับ internal error โดยไม่ส่ง secret หรือ stack trace

## Invariants และ transaction

- Step ระบุลำดับคงที่, provider และ prompt; task ของ step ถูกสร้างได้ครั้งเดียวโดย unique constraint `(workflow_id, step_index)` และใช้ `queue_order` จาก transactional counter เดียวกับ task ปกติ งาน step ถัดไปจึงเข้าคิวหลังงานที่ commit ก่อนหน้า ไม่แซง FIFO
- เมื่อ task completed: ใน transaction เดียวกันบันทึกผล, event, ปรับ workflow/step และสร้าง task ถัดไปเฉพาะเมื่อไม่มีคำขอยกเลิก; handoff ใช้เฉพาะ result ที่ persist และผ่าน redaction แล้ว รวมกับ prompt ของ step ถัดไป ไม่ดึง history หรือ credentials เพิ่ม; redaction ครอบคลุมค่า `OPENAI_API_KEY`, `CODEX_API_KEY` และ `GEMINI_API_KEY` จาก environment เท่านั้น ไม่รับประกันว่าผลลัพธ์ไม่มีความลับอื่น
- หาก output รวมกับ prompt เกินขนาดที่ runtime รับได้ ให้ fail workflow พร้อมเหตุผล แทนการตัดข้อมูลเงียบ ๆ หรือเริ่ม step ที่ input ไม่ครบ
- Cancellation และ completion ต้อง lock แถว workflow ตามลำดับ lock เดียวกันเพื่อ serialize การตัดสินใจ: cancellation ที่ commit ก่อนป้องกันการสร้าง step ถัดไป; completion ที่ commit ก่อนอาจสร้าง task ถัดไปแล้ว และ cancellation ต้องยกเลิก task นั้นตามสถานะจริง
- Step failed/cancelled หรือ execution ที่ยืนยันหยุดไม่ได้ทำให้ workflow ไม่สร้าง step ถัดไป; project ยังคง blocked ตาม V1 เมื่อจำเป็น
- Restart ห้าม replay active task และห้ามสร้าง step ซ้ำ; recovery ใช้ persisted task/step state และ transaction เดียวกับการหยุด workflow หากสถานะเดิมไม่ยืนยันได้
- Workflow events ต้องมี global sequence ตาม commit order และสัมพันธ์กับ workflow/step/task; SSE และ snapshot ต้องทำให้ client reconnect ได้โดยไม่พลาด state หรือสมมติผลจาก animation

## ข้อที่ต้องพิสูจน์ก่อนเปิด API

กำหนดขีดจำกัด steps และ payload ตาม runtime จริง, ตรวจโครงสร้างข้อมูลที่ผู้ใช้จะส่งข้าม provider ใน UI, พิสูจน์ cancellation/permission ของแต่ละ provider, เพิ่ม HTTP tests กับ PostgreSQL จริงสำหรับ handoff, FIFO, completion/cancel race และ restart รวมถึง opt-in smoke test กับ provider ที่เปิดใช้
