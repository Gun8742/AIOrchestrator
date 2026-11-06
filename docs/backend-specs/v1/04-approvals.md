# V1 — Agent Approval

## Problem Statement

Agent อาจขออนุญาตทำ action ที่มีผลต่อเครื่อง ผู้ใช้ต้องเห็นรายละเอียดและตัดสินใจได้เมื่อ integration รองรับ

## Solution

ส่งคำขอ approval ที่ runtime เปิดเผยให้ UI พร้อมผูกคำตอบกับ task และคำขอที่ถูกต้อง หากส่งต่อไม่ได้ให้รายงาน failure อย่างชัดเจน

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการเห็น action ที่ Agent ขออนุญาต เพื่อประเมินก่อนตัดสินใจ
2. ในฐานะผู้ใช้ ฉันต้องการอนุมัติหรือปฏิเสธคำขอ เพื่อควบคุมงาน
3. ในฐานะผู้ใช้ ฉันต้องการเห็นว่างานรอคำตอบ เพื่อเข้าใจว่าทำไมงานหยุด
4. ในฐานะผู้ใช้ ฉันต้องการเห็นคำขอที่ยังรออยู่หลัง reconnect เพื่อดำเนินงานต่อได้
5. ในฐานะผู้ใช้ ฉันต้องการให้คำตอบเก่าหรือซ้ำไม่อนุมัติ action อื่น เพื่อป้องกันความผิดพลาด
6. ในฐานะผู้ใช้ ฉันต้องการเห็น error เมื่อ runtime ไม่รองรับ approval หรือคำขอหมดอายุ เพื่อดำเนินการต่อได้ถูกต้อง
7. ในฐานะผู้ใช้ ฉันต้องการหยุด task ระหว่างรอ approval เพื่อควบคุมงาน

## Implementation Decisions

- Approval มี opaque ID, taskId, รายละเอียด action ที่ runtime ส่งมา, status pending/resolved/expired และเวลาที่สร้าง; ไม่เติมรายละเอียดที่ไม่มีหลักฐาน
- บันทึก pending approval และเปลี่ยน task เป็น waiting_for_user พร้อม event ใน transaction เดียว; snapshot ต้องมี pending approval
- `POST /api/tasks/:id/approvals/:approvalId` รับ `{decision: "approve" | "deny"}`; ตรวจ ownership, pending state และ runtime availability ก่อนส่งคำตอบ เมื่อ runtime รับคำตอบแล้วบันทึก resolved และ task กลับ running
- คำขอไม่พบคืน 404; คำตอบซ้ำ, หมดอายุ หรือ task ไม่รอคำขอนี้คืน 409; input ผิดคืน 400. UI ต้องไม่แสดงว่าสำเร็จจน runtime รับคำตอบ
- หาก runtime ไม่รองรับ interactive approval หรือ timeout/disconnect ทำให้ตอบไม่ได้ ให้ task failed พร้อมเหตุผล ไม่อนุมัติแทน; การ cancel ทำให้ approval pending หมดอายุเมื่อ task ยุติ
- รายละเอียด approval และ decision เป็นส่วนหนึ่งของ history แต่ห้ามเก็บ credential

## Testing Decisions

- ทดสอบผ่าน HTTP API ด้วย runtime จำลอง โดยตรวจ pending, approve, deny, duplicate, stale ID, disconnect, cancel และ recovery; ไม่มี prior art
- Smoke test กับ Codex จริงแบบ opt-in เฉพาะเมื่อ integration รองรับ approval forwarding

## Out of Scope

Policy editor, bulk approval, อนุมัติล่วงหน้า และ approval ที่ provider ไม่เปิดให้ client จัดการ

## Further Notes

Interactive approval เป็น capability ที่ต้องพิสูจน์ก่อนเลือก integration; หากไม่มี ให้ส่ง error ที่ actionable ตามที่ระบุ ไม่จำลองว่ารองรับ
