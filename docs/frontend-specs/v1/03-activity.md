# V1 — Task History, Activity และ Connection

## Problem Statement

ผู้ใช้ต้องติดตามงานและตรวจผลหลัง refresh หรือเครือข่ายหลุด โดยไม่เห็นสถานะเก่าราวกับเป็นข้อมูลปัจจุบัน

## Solution

แสดง task list และ activity ของงานที่เลือกจาก Backend พร้อมรับ event แบบ real-time และกู้ snapshot เมื่อเชื่อมต่อใหม่

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการเห็นรายการงานและสถานะ เพื่อเลือกงานที่สนใจ
2. ในฐานะผู้ใช้ ฉันต้องการอ่าน prompt, events, result และ error เพื่อประเมินผล
3. ในฐานะผู้ใช้ ฉันต้องการเห็นเวลาและลำดับเหตุการณ์ เพื่อเข้าใจสิ่งที่เกิดขึ้น
4. ในฐานะผู้ใช้ ฉันต้องการเห็นความคืบหน้าจริงโดยไม่ refresh เพื่อเฝ้าดูงาน
5. ในฐานะผู้ใช้ ฉันต้องการเห็นสถานะเชื่อมต่อ เพื่อรู้ว่าข้อมูลอาจไม่เป็นปัจจุบัน
6. ในฐานะผู้ใช้ ฉันต้องการ reload และ reconnect แล้วเห็นสถานะล่าสุด เพื่อทำงานต่อ
7. ในฐานะผู้ใช้ ฉันต้องการยกเลิกงานและรู้ผลจริง เพื่อควบคุม execution
8. ในฐานะผู้ใช้ ฉันต้องการทราบว่าการยกเลิกไม่ย้อนการแก้ไฟล์ เพื่อพิจารณาผลกระทบ
9. ในฐานะผู้ใช้ ฉันต้องการเห็น error เมื่อ service หยุดระหว่างงาน เพื่อไม่เข้าใจว่างานยังทำอยู่
10. ในฐานะผู้ใช้ ฉันต้องการอ่าน activity ที่มีข้อความยาวบนจอเล็ก เพื่อไม่ให้ข้อมูลล้นจอ

## Implementation Decisions

- โหลด task list และรายละเอียดจาก HTTP; แสดง prompt, project, status, timestamps, event timeline, result และ error ที่ Backend ให้มา; แสดง empty/loading/error states
- ใช้ SSE ตาม Backend contract: เปิด SSE และรอ ready event ก่อนโหลด snapshot, buffer events ระหว่างโหลด และใช้ snapshot sequence กรอง event ซ้ำ จากนั้น apply events ใหม่ตามลำดับ; หาก stream หลุดหรือ buffer overflow ให้เริ่ม reconciliation ใหม่
- เมื่อหลุดแสดง disconnected/reconnecting และระบุว่าข้อมูลอาจเก่า; reconnect แล้วทำ reconciliation ใหม่ หาก snapshot โหลดไม่สำเร็จคงสถานะไม่เชื่อมต่อและเสนอให้ลองใหม่
- แสดงเวลาใน local timezone โดยเก็บ timestamp ที่ Backend ส่ง; ไม่สร้าง progress percentage หรือข้อความ activity เอง
- ให้ cancel task ที่ยังไม่ terminal; แสดง pending ระหว่าง request และคงสถานะจริงจน Backend ยืนยัน การ cancel สำเร็จไม่หมายถึง rollback ไฟล์
- หาก cancel request มีผลไม่แน่นอน ให้ reload task ก่อนเสนอให้ลองใหม่; หากเสร็จก่อน cancel ให้แสดง terminal status จริง
- แสดง output เป็นข้อความที่ปลอดภัย ไม่ render HTML จาก Agent; ไม่ใส่ credentials ลง client logs

## Testing Decisions

- ทดสอบผ่าน UI กับ HTTP/SSE จำลอง: event ระหว่าง snapshot, event ซ้ำ, reconnect, loading/error, stale state, cancel race และ reload; ไม่มี prior art
- ตรวจ keyboard, screen reader announcements ที่ไม่รบกวนเกินไป และ layout ของข้อความยาว

## Out of Scope

Live terminal, analytics, replay SSE จาก cursor และ export logs

## Further Notes

List และ task snapshot มี sequence ตาม Backend V1; UI ต้องรอ SSE ready ก่อนเริ่ม snapshot เพื่อไม่พลาด event ระหว่างเชื่อมต่อ
