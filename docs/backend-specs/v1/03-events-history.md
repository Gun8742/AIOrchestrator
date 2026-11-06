# V1 — Task History และ Real-time Events

## Problem Statement

ผู้ใช้ต้องรู้ว่า Agent ทำอะไรและงานอยู่สถานะใด แม้ refresh หรือเชื่อมต่อเว็บใหม่

## Solution

บันทึก task และ events แบบ persistent พร้อมส่งการเปลี่ยนแปลงผ่าน SSE; snapshot เป็นแหล่งข้อมูลที่ใช้กู้สถานะ

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการดูรายการงานที่ผ่านมา เพื่อกลับไปตรวจผลได้
2. ในฐานะผู้ใช้ ฉันต้องการดูเหตุการณ์เรียงลำดับ เพื่อเข้าใจความคืบหน้า
3. ในฐานะผู้ใช้ ฉันต้องการเห็นสถานะเปลี่ยนระหว่างทำงาน เพื่อไม่ต้อง refresh เอง
4. ในฐานะผู้ใช้ ฉันต้องการ reload แล้วเห็นสถานะล่าสุด เพื่อทำงานต่อได้
5. ในฐานะผู้ใช้ ฉันต้องการกู้ข้อมูลหลัง network หลุด เพื่อไม่พลาดสถานะสุดท้าย
6. ในฐานะผู้ใช้ ฉันต้องการเห็น error ที่มีประโยชน์ เพื่อเข้าใจสาเหตุที่งานล้มเหลว
7. ในฐานะผู้ใช้ ฉันต้องการไม่ให้ credential ปรากฏใน activity เพื่อป้องกันข้อมูลลับ

## Implementation Decisions

- Event มี global sequence ที่เรียงตาม commit order, taskId, type, timestamp และ payload; ประเภทขั้นต่ำคือ status change, activity, result, error และ approval ตามข้อมูลที่ runtime ส่งจริง
- ใช้ PostgreSQL เก็บ task และ events แบบ persistent; local development ใช้ Docker Compose พร้อม persistent volume; status change กับ event ต้อง commit พร้อมกัน และ publish หลัง commit เท่านั้น
- `GET /api/tasks` คืน `{tasks, sequence}` และ `GET /api/tasks/:id` คืน `{task, events, sequence}` โดย snapshot และ watermark อ่านจาก database snapshot เดียวกัน; `GET /api/events` เป็น SSE แจ้ง task events โดยมี sequence; ใช้ access control เดียวกับ API
- ใช้ transactional counter row ที่ lock จน commit เพื่อจัดลำดับ event transactions; ห้ามใช้ PostgreSQL sequence เพียงอย่างเดียวเป็น commit watermark. Dispatcher อ่าน committed events ตาม sequence และส่งตามลำดับ; หาก dispatcher ล้มเหลวให้ปิด stream เพื่อบังคับ resync
- Reconnect: client เปิด SSE และรอ ready event ที่ยืนยันว่าการ subscribe มีผลก่อนโหลด snapshot; buffer events ระหว่างโหลดแล้วกรอง sequence ที่ snapshot ครอบคลุม จากนั้น apply events ที่เหลือตามลำดับ หาก buffer overflow หรือ stream หลุดให้เริ่มใหม่; SSE ไม่รับประกัน replay ข้าม connection
- Preserve raw provider data เท่าที่จำเป็นต่อการแสดงผล; redact credential ที่ระบบรู้จักก่อน persist/publish และหลีกเลี่ยงการบันทึก environment หรือ auth payload; ห้ามรับประกันว่าตรวจจับ secret ทุกชนิดได้
- Error เปิดเผยข้อมูลที่จำเป็นต่อการแก้ปัญหา แต่ไม่ส่ง stack trace หรือ authentication data; ไม่สร้าง event หรือ subagent ที่ runtime ไม่รายงาน

## Testing Decisions

- ทดสอบผ่าน HTTP และ SSE โดยใช้ runtime จำลองกับ PostgreSQL จริงในสภาพแวดล้อมทดสอบแบบแยก; ไม่มี prior art
- ตรวจ concurrent commits และ rollback, event ordering, atomic persistence, ready handshake, reconnect ที่เกิด event ระหว่าง snapshot, duplicate filtering, stream failure/overflow, restart และการไม่แสดง credential ที่ระบบรู้จัก

## Out of Scope

Analytics, usage billing, เก็บ event ไม่จำกัดตลอดไปเป็นข้อรับประกัน, การ replay SSE แบบ cursor และการแสดง subagent ที่ไม่มีข้อมูลจริง

## Further Notes

กำหนด retention และขนาด payload ก่อน production; logs อาจมีข้อมูลอ่อนไหวจาก prompt หรือ output แม้ผ่าน redaction
