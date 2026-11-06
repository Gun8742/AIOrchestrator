# V2 — Workflow Handoff และ Subagent Visibility

## Problem Statement

ผู้ใช้ต้องการให้ Agent หลายตัวส่งงานต่อกันและมองเห็นทีมย่อย โดยไม่สับสนระหว่างข้อมูลจริงกับภาพจำลอง

## Solution

รองรับ workflow แบบลำดับขั้นที่กำหนด provider ต่อขั้น และแสดงข้อมูล subagent เฉพาะเมื่อ runtime ส่ง event ที่เชื่อถือได้

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการกำหนดลำดับ Agent เพื่อให้ผลลัพธ์ส่งต่อกัน
2. ในฐานะผู้ใช้ ฉันต้องการเห็นสถานะแต่ละขั้น เพื่อทราบว่างานติดอยู่ที่ใด
3. ในฐานะผู้ใช้ ฉันต้องการให้ขั้นถัดไปได้รับผลขั้นก่อน เพื่อทำงานต่อได้
4. ในฐานะผู้ใช้ ฉันต้องการให้งานหยุดเมื่อขั้นหนึ่งล้มเหลว เพื่อไม่ส่งผลที่ผิดไปต่อโดยอัตโนมัติ
5. ในฐานะผู้ใช้ ฉันต้องการยกเลิก workflow เพื่อหยุดขั้นที่กำลังทำและไม่เริ่มขั้นถัดไป
6. ในฐานะผู้ใช้ ฉันต้องการเห็น subagent จริงและความสัมพันธ์กับ parent เมื่อมีข้อมูล เพื่อเข้าใจการแบ่งงาน
7. ในฐานะผู้ใช้ ฉันต้องการรู้เมื่อ runtime ไม่เปิดเผย subagent เพื่อไม่เข้าใจ animation ว่าเป็นข้อมูลจริง
8. ในฐานะผู้ใช้ ฉันต้องการตรวจเนื้อหาที่ส่งข้าม provider เพื่อควบคุมข้อมูลที่แชร์

## Implementation Decisions

- Workflow เป็นชุด step แบบเรียงลำดับ มี project, prompt และ provider ต่อ step; step สร้าง task ที่สัมพันธ์กับ workflow และใช้คิวร่วมกับ task อื่น
- ส่งเฉพาะผลลัพธ์ของ step ก่อนที่บันทึกสำเร็จเป็น input เพิ่มให้ step ถัดไป พร้อมขอบเขตข้อมูลที่ระบุให้ผู้ใช้ทราบก่อนเริ่ม; ไม่ส่ง credential หรือ history ทั้งหมดโดยปริยาย
- เริ่ม step ถัดไปเฉพาะเมื่อ step ก่อน completed; failed/cancelled หยุด workflow. Cancel workflow ยกเลิก active task และป้องกัน step ถัดไป; รายงานผลตาม lifecycle จริง
- Workflow และ step state ต้อง persistent; restart ไม่ replay step ที่เริ่มแล้ว; ใช้ recovery แบบ V1 และหยุด workflow หากสถานะ step ไม่ยืนยันได้
- แสดง subagent ID, parent, status และ events เฉพาะข้อมูลที่ runtime เปิดเผยและผูกกับ task ได้; capability ไม่รองรับให้ระบุ unavailable ไม่สร้าง child ปลอม
- Workflow API สำหรับ Codex ระบุไว้ใน [Workflow contract](workflow-contract-draft.md); provider อื่นต้องตรวจ contract และ event จริงก่อนเปิดใช้งาน

## Testing Decisions

- ทดสอบที่ HTTP API ด้วย runtimes จำลอง: handoff payload, sequence, failure, cancellation race, restart, การไม่ส่ง credential และ subagent event ที่ไม่มี/มีข้อมูล; ไม่มี prior art
- ทดสอบ provider จริงแบบ opt-in หลังยืนยัน capability และข้อมูลที่แชร์

## Out of Scope

Parallel DAG, autonomous routing, การให้ Agents สื่อสารกันโดยตรง, retry อัตโนมัติ และการแสดง subagent ที่คาดเดาขึ้น

## Further Notes

การส่ง output ข้าม provider อาจส่ง source code หรือข้อมูลอ่อนไหวออกนอกเครื่องตามนโยบายของ provider; ผู้ใช้ต้องเห็นขอบเขตก่อนเริ่ม
