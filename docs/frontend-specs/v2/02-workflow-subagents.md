# V2 — Workflow และ Subagent Visibility

## Problem Statement

ผู้ใช้ต้องการมอบหมายงานหลายขั้นและเห็น Agent หรือ Subagent ที่เกี่ยวข้องโดยเข้าใจการส่งข้อมูลระหว่างขั้น

## Solution

แสดง workflow แบบลำดับขั้นและ task ของแต่ละขั้น พร้อมแสดง subagent เมื่อ Backend ได้รับข้อมูลจริง

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการกำหนดลำดับ provider และ prompt เพื่อส่งงานต่อกัน
2. ในฐานะผู้ใช้ ฉันต้องการเห็นข้อมูลที่จะส่งข้าม provider ก่อนเริ่ม เพื่อประเมินข้อมูลที่แชร์
3. ในฐานะผู้ใช้ ฉันต้องการเห็นสถานะและผลของแต่ละขั้น เพื่อเข้าใจความคืบหน้า
4. ในฐานะผู้ใช้ ฉันต้องการเห็นเหตุผลเมื่อ workflow หยุด เพื่อแก้ไขได้
5. ในฐานะผู้ใช้ ฉันต้องการยกเลิก workflow เพื่อหยุดงานที่กำลังทำและขั้นถัดไป
6. ในฐานะผู้ใช้ ฉันต้องการเห็น subagent และ parent เมื่อข้อมูลมีจริง เพื่อเข้าใจการแบ่งงาน
7. ในฐานะผู้ใช้ ฉันต้องการเห็นข้อจำกัดเมื่อไม่มีข้อมูล subagent เพื่อไม่เข้าใจว่าระบบไม่มี subagent เสมอไป
8. ในฐานะผู้ใช้ ฉันต้องการเข้าถึง workflow ผ่าน keyboard และข้อความ เพื่อใช้งานได้แม้ไม่ดูฉาก

## Implementation Decisions

- ให้สร้าง workflow แบบเรียงลำดับและเลือก provider/prompt ของแต่ละ step; ก่อนเริ่มแสดงว่า output ของขั้นก่อนจะส่งเป็น input ให้ขั้นถัดไปและอาจมี source code หรือข้อมูลอ่อนไหว
- แสดงขั้น, task, provider, สถานะ, ผลและ error จาก Backend; เริ่มหรือยกเลิกผ่าน contract ที่ตกลงกับ Backend V2 ภายหลัง
- เมื่อ cancel ให้แสดง pending และสถานะจริงจาก Backend; ไม่แสดงว่าทุกขั้นหยุดแล้วก่อนยืนยัน
- แสดง parent, child และสถานะ subagent เฉพาะ event ที่ Backend ผูกกับ task ได้; หาก capability ไม่มีให้แสดง unavailable ไม่สร้างตัวละครปลอม
- ข้อมูลใน panel เป็นแหล่งที่ตรวจสอบได้; office animation เป็นภาพประกอบและไม่แทนการส่งข้อมูลจริง

## Testing Decisions

- ทดสอบ UI กับ Backend จำลอง: ลำดับ step, handoff disclosure, failed step, cancellation race, reconnect และ subagent มี/ไม่มีข้อมูล; ไม่มี prior art
- ตรวจ keyboard และการนำเสนอข้อมูลที่ไม่พึ่ง animation

## Out of Scope

Parallel workflow editor, drag-and-drop graph, autonomous routing และการสื่อสารตรงระหว่าง Agent

## Further Notes

Workflow API แบบ sequential สำหรับ Codex ระบุไว้ใน [Workflow contract](../../backend-specs/v2/workflow-contract-draft.md); provider อื่นต้องตรวจ capability ก่อนเปิดใช้งาน
