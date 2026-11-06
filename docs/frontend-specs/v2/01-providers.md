# V2 — Provider Selection และ Office Agents

## Problem Statement

ผู้ใช้ต้องการเลือก Codex, Claude Code หรือ Antigravity และแยกได้ว่า task ใช้ provider ใด

## Solution

แสดง provider ที่ Backend รายงานพร้อมสถานะและ capability โดยขยายออฟฟิศให้แสดง Agent ตามข้อมูลจริง

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการเห็น provider ที่พร้อมใช้ เพื่อเลือก Agent ได้
2. ในฐานะผู้ใช้ ฉันต้องการเห็นเหตุผลเมื่อ provider ใช้ไม่ได้ เพื่อแก้ปัญหา
3. ในฐานะผู้ใช้ ฉันต้องการเลือก provider ก่อนส่ง task เพื่อกำหนดผู้รับงาน
4. ในฐานะผู้ใช้ ฉันต้องการเห็น provider และสถานะใน task detail เพื่อแยกผลลัพธ์
5. ในฐานะผู้ใช้ ฉันต้องการเห็นข้อจำกัดด้าน approval และ cancellation เพื่อคาดการณ์การควบคุมงานได้
6. ในฐานะผู้ใช้ ฉันต้องการให้ตัวละครสะท้อนข้อมูลที่ Backend มี เพื่อไม่เข้าใจข้อมูลจำลองว่าเป็น Agent จริง

## Implementation Decisions

- โหลด provider และ capability จาก `GET /api/providers` ตาม Backend V2; selector แสดง availability และเหตุผลเมื่อมีข้อมูล
- ส่ง provider ID ที่เลือกพร้อม task; ไม่เปลี่ยน provider อัตโนมัติเมื่อใช้ไม่ได้ และไม่แสดงการควบคุมที่ capability ไม่รองรับ
- task list/detail แสดง provider; ออฟฟิศอาจมีตัวละครต่อ provider ที่ Backend รายงาน โดย task ที่เลือกเป็นแหล่งสถานะของตัวละคร
- แสดงข้อมูลด้วยข้อความและ accessible control เช่นเดียวกับ V1; ไม่เก็บ credential ใน UI

## Testing Decisions

- ทดสอบ UI กับ provider responses จำลองทั้งพร้อมใช้, unavailable และ capability ต่างกัน รวมการเลือกและส่งงาน; ไม่มี prior art

## Out of Scope

เลือก model ภายใน provider, automatic routing และการเปลี่ยน provider ระหว่าง task

## Further Notes

ความพร้อมของ Claude Code และ Antigravity ต้องพิสูจน์จาก integration จริงก่อนเปิดใช้งาน
