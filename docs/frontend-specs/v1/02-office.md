# V1 — Pixel Art Office

## Problem Statement

ผู้ใช้ต้องการเห็นงาน Agent เป็นภาพบริษัท 2D ที่เข้าใจง่ายโดยไม่สับสนระหว่าง animation กับสถานะจริง

## Solution

สร้างออฟฟิศ Pixel Art หนึ่งห้องพร้อมตัวละคร Codex และสถานะงานที่อิงข้อมูล Backend โดยมี panel ปกติสำหรับรายละเอียด

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการเห็นออฟฟิศ Pixel Art เพื่อให้การติดตามงานมีบรรยากาศเหมือนบริษัท
2. ในฐานะผู้ใช้ ฉันต้องการเห็นสถานะของงานที่เลือกใกล้ตัวละคร เพื่อเข้าใจภาพรวม
3. ในฐานะผู้ใช้ ฉันต้องการคลิกหรือใช้ keyboard เลือก Agent เพื่อเปิดรายละเอียดงาน
4. ในฐานะผู้ใช้ ฉันต้องการเห็นสัญญาณเมื่อ Agent รอคำตอบ เพื่อรู้ว่าต้องดำเนินการ
5. ในฐานะผู้ใช้ ฉันต้องการแยกสถานะจริงออกจาก animation เพื่อเชื่อถือสิ่งที่เห็น
6. ในฐานะผู้ใช้ ฉันต้องการใช้เว็บโดยลด motion ได้ เพื่อหลีกเลี่ยง animation ที่รบกวน
7. ในฐานะผู้ใช้ ฉันต้องการอ่านข้อมูลและใช้ controls บนจอเล็กได้ เพื่อไม่ให้ฉากบังงาน
8. ในฐานะผู้ใช้ ฉันต้องการเข้าถึงข้อมูลโดยไม่ต้องพึ่งภาพหรือสี เพื่อใช้ keyboard หรือ screen reader ได้

## Implementation Decisions

- ฉากหนึ่งห้องมีโต๊ะและตัวละคร Codex หนึ่งตัวใน MVP; ตัวละครสะท้อน task ที่เลือก ไม่อนุมานว่า task ทั้งหมดมีสถานะเดียวกัน
- แสดงชื่อและสถานะ `queued`, `running`, `waiting_for_user`, `completed`, `failed`, `cancelled` เป็นข้อความ; เมื่อไม่มี task ให้แสดง idle เป็นสถานะการแสดงผลของ UI ไม่ใช่ Backend task status
- Animation เป็นผลจากสถานะที่ได้รับจริง: working เมื่อ running, สัญลักษณ์แจ้งเตือนเมื่อ waiting_for_user; ไม่แสดงข้อความ ความคืบหน้า หรือ subagent ที่ระบบไม่ได้รายงาน
- ตัวละครเป็น control ที่เข้าถึงได้ด้วย keyboard พร้อม accessible name; ข้อมูลเดียวกันเปิดจาก task list ได้เมื่อฉากไม่เหมาะกับ assistive technology
- เคารพ `prefers-reduced-motion`; ฉากต้องไม่ขัดขวาง panel หรือทำให้ข้อมูลสำคัญอ่านไม่ได้บนจอเล็ก
- แยกการ render ฉากจาก task state; Canvas หรือ DOM/CSS และแหล่ง asset ยังเป็นทางเลือกหลังทดลอง โดยไม่เพิ่ม game engine ก่อนมีความจำเป็น

## Testing Decisions

- ทดสอบ UI ด้วย task snapshots จำลองทุกสถานะ รวม no task, การเปลี่ยน task, keyboard selection และ reduced motion; ไม่มี prior art
- ตรวจ visual layout บน desktop และ mobile และตรวจว่าไม่มี animation แทนเหตุการณ์ที่ไม่ได้รับ

## Out of Scope

ตกแต่งห้อง, ระบบเกม, multiplayer, avatar หลาย provider และแสดงจำนวน subagent จากการคาดเดา

## Further Notes

Art direction, palette และ asset ยังไม่ถูกเลือก; สิทธิ์ใช้งาน asset ต้องตรวจเมื่อเลือกจริง
