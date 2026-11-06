# V1 — Approval Interaction

## Problem Statement

ผู้ใช้ต้องตัดสินใจเกี่ยวกับ action ที่ Agent ขอได้อย่างชัดเจน โดยไม่เข้าใจผิดว่าระบบได้อนุมัติหรือหยุดงานแล้ว

## Solution

แสดง pending approval จาก Backend พร้อมรายละเอียดที่มี ปุ่ม approve/deny และผลการตอบที่ยืนยันจาก service

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการเห็นว่างานรอ approval เพื่อรู้ว่าต้องดำเนินการ
2. ในฐานะผู้ใช้ ฉันต้องการอ่านรายละเอียด action และ project ก่อนตอบ เพื่อประเมินผลกระทบ
3. ในฐานะผู้ใช้ ฉันต้องการ approve หรือ deny เพื่อควบคุมการทำงาน
4. ในฐานะผู้ใช้ ฉันต้องการเห็นผลการตอบจาก Backend เพื่อรู้ว่าคำตอบถูกส่งจริง
5. ในฐานะผู้ใช้ ฉันต้องการกลับมาดู pending approval หลัง reload เพื่อทำงานต่อ
6. ในฐานะผู้ใช้ ฉันต้องการเห็นคำขอหมดอายุหรือใช้ไม่ได้ เพื่อไม่ตอบคำขอเก่า
7. ในฐานะผู้ใช้ ฉันต้องการหยุด task ที่รอ approval เพื่อควบคุมงาน
8. ในฐานะผู้ใช้ ฉันต้องการใช้ keyboard เพื่อตรวจและตอบคำขอได้

## Implementation Decisions

- แสดงรายละเอียด action ที่ Backend ส่งจริง พร้อม task, project และเวลาที่ร้องขอ; ถ้ารายละเอียดไม่ครบ ให้ระบุว่าไม่มีข้อมูล ไม่เติมเอง
- แสดง pending approval ใน task detail และแจ้งเตือนที่ task/office; ใช้ `POST /api/tasks/:id/approvals/:approvalId` ส่ง approve หรือ deny
- ปิดปุ่มขณะส่งและรอผล; เมื่อ 404/409 หรือเชื่อมต่อขาด ให้โหลด snapshot ล่าสุดและแสดงผลตามจริง ไม่ retry approve อัตโนมัติ
- หาก integration ไม่รองรับ approval ให้แสดง failure และข้อจำกัดจาก Backend; ไม่เสนอปุ่มที่ใช้งานไม่ได้หรืออนุมัติแทนผู้ใช้
- จัดการ focus, accessible labels และข้อความสถานะ; แสดงคำขอที่ยัง pending หลัง reload/reconnect ตาม snapshot

## Testing Decisions

- ทดสอบ UI กับ Backend จำลอง: approve, deny, duplicate click, 404/409, disconnect, reload, cancellation และ unsupported capability; ไม่มี prior art
- ตรวจ keyboard/focus และรายละเอียดที่แสดงตรงกับคำขอจริง

## Out of Scope

Bulk approval, policy editor และ pre-approval

## Further Notes

การใช้งานจริงขึ้นอยู่กับ interactive approval capability ของ Codex integration
