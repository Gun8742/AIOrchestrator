# V2 — Multi-provider Agents

## Problem Statement

ผู้ใช้ต้องการใช้ Codex, Claude Code และ Antigravity ร่วมกัน แต่แต่ละ runtime มีความสามารถและเงื่อนไขต่างกัน

## Solution

เพิ่ม provider ที่พิสูจน์การเชื่อมต่อได้ทีละตัว และให้ผู้ใช้เลือก provider สำหรับ task โดยแสดง capability และข้อจำกัดตามจริง

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการเห็น provider ที่พร้อมใช้เพื่อเลือก Agent ได้
2. ในฐานะผู้ใช้ ฉันต้องการเลือก provider ก่อนเริ่ม task เพื่อใช้เครื่องมือที่เหมาะกับงาน
3. ในฐานะผู้ใช้ ฉันต้องการเห็น provider ของแต่ละ task เพื่อเข้าใจแหล่งที่มาของผล
4. ในฐานะผู้ใช้ ฉันต้องการเห็นสาเหตุที่ provider ไม่พร้อม เพื่อแก้ configuration
5. ในฐานะผู้ใช้ ฉันต้องการให้ระบบไม่อ้างว่าหยุดหรืออนุมัติได้หาก provider ไม่รองรับ เพื่อเชื่อถือสถานะได้
6. ในฐานะผู้ใช้ ฉันต้องการให้การใช้งาน provider ใหม่ยังเคารพคิวและสิทธิ์โปรเจกต์ เพื่อป้องกันงานชนกัน

## Implementation Decisions

- ขยาย task ให้ระบุ provider; task เดิมเป็น Codex และยังอ่านได้ ต้องมี migration ที่ไม่ลบประวัติ
- `GET /api/providers` คืน provider ID, availability และ capabilities ที่ตรวจสอบได้; `POST /api/tasks` รับ provider ID และตรวจ availability ก่อนสร้าง task
- คิวหนึ่ง active task ต่อ project ใช้ร่วมกันทุก provider; lifecycle, history, security และ cancellation ตาม V1
- แต่ละ provider ต้องพิสูจน์วิธี authentication, execution, cancellation, approval, event และข้อตกลงการใช้งานก่อนเปิดใช้; หาก capability ไม่รองรับให้รายงานอย่างชัดเจน ไม่ fallback อัตโนมัติ
- Credential อยู่ฝั่ง backend หรือระบบ auth ของ provider และไม่ใส่ task history; ไม่ออกแบบ plugin framework จนมี integration ที่จำเป็นจริง

## Testing Decisions

- ทดสอบ HTTP contract กับ provider runtime จำลองแต่ละ capability และ migration จาก V1; ไม่มี prior art ปัจจุบัน
- Smoke test แบบ opt-in กับ provider จริงแต่ละตัว รวม error และ cancellation ตาม capability

## Out of Scope

การสลับ provider กลาง task, model routing อัตโนมัติ และการรับประกัน feature parity

## Further Notes

ชื่อ V2 เป็น roadmap ที่เสนอ ไม่ใช่คำยืนยันว่าทั้ง Claude Code และ Antigravity เชื่อมต่อได้ในสภาพแวดล้อมของผู้ใช้
