# V1 — Project Selection และ Task Submission

## Problem Statement

ผู้ใช้ต้องเลือกโปรเจกต์และสั่ง Codex ทำงานจากหน้าเว็บ โดยรู้ว่าคำสั่งจะทำงานกับ directory ใด

## Solution

แสดงรายการโปรเจกต์ ให้เพิ่ม local path และส่ง prompt พร้อม feedback จาก Backend อย่างชัดเจน

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการเห็นรายการโปรเจกต์และ path เพื่อเลือกพื้นที่ทำงานได้ถูกต้อง
2. ในฐานะผู้ใช้ ฉันต้องการเพิ่ม project path เพื่อเริ่มใช้งานโปรเจกต์บนเครื่อง
3. ในฐานะผู้ใช้ ฉันต้องการทราบเมื่อ path ไม่ถูกต้องหรือซ้ำ เพื่อแก้ไขได้
4. ในฐานะผู้ใช้ ฉันต้องการเลือกโปรเจกต์และกรอก prompt ภาษาไทยหรืออังกฤษ เพื่อมอบหมายงาน
5. ในฐานะผู้ใช้ ฉันต้องการเห็น path และขอบเขตสิทธิ์ที่ทราบก่อนส่ง เพื่อเข้าใจพื้นที่ทำงานและสิ่งที่ Agent อาจทำได้
6. ในฐานะผู้ใช้ ฉันต้องการเห็น validation และข้อผิดพลาด เพื่อแก้ไขโดยไม่เสีย prompt
7. ในฐานะผู้ใช้ ฉันต้องการเห็นงานที่ส่งแล้วและสถานะคิว เพื่อรู้ว่าคำสั่งได้รับการบันทึก
8. ในฐานะผู้ใช้ ฉันต้องการไม่ส่งคำสั่งซ้ำจากการกดปุ่มซ้ำ เพื่อหลีกเลี่ยงงานซ้ำ
9. ในฐานะผู้ใช้ ฉันต้องการใช้ keyboard และ screen reader เพื่อสั่งงานได้

## Implementation Decisions

- แสดง project selector พร้อมชื่อและ canonical path; empty state ให้เพิ่ม path ด้วย form และแสดงผล validation จาก service
- ใช้ `GET /api/projects`, `POST /api/projects` และ `POST /api/tasks` ตาม Backend V1; Backend เป็นผู้ตรวจ path และ input ขั้นสุดท้าย
- แสดง project path, prompt และข้อมูลสิทธิ์ที่ตรวจสอบได้ก่อนเริ่ม; ห้ามอ้างว่า project path เป็น sandbox หรือเติมรายละเอียดสิทธิ์ที่ยังไม่ยืนยัน
- ปิดปุ่ม submit ระหว่าง request เดียวกัน; เมื่อสำเร็จเลือก task ที่สร้างและแสดงสถานะจาก response; เมื่อผิดพลาดเก็บ prompt และ project ที่เลือกไว้
- หาก request ขาดการเชื่อมต่อโดยไม่ทราบผล ให้แจ้งว่าผลการส่งไม่แน่นอนและให้ตรวจรายการงานก่อนส่งใหม่ ไม่ retry สร้าง task อัตโนมัติ
- task ในคิวแสดง `queued`; หลัง restart งานที่ยังไม่เริ่มเดินต่อแบบ FIFO เมื่อ project ปลอดภัย หาก Backend รายงาน project blocked ให้แสดงเหตุผลและวิธีแก้ตามข้อมูลจริง ไม่สร้างปุ่มปลด block ที่ Backend ยังไม่มี
- รองรับ keyboard, label ที่สัมพันธ์กับ form, ข้อความ error และ focus ที่เหมาะสม; ไม่ใช้สีอย่างเดียวสื่อสถานะ
- ติดต่อ local service ผ่านวิธี authentication และ origin ที่ Backend กำหนด; วิธี bootstrap ยังต้องออกแบบร่วมกัน

## Testing Decisions

- ทดสอบผ่าน UI ด้วย Backend responses จำลอง: เพิ่ม project สำเร็จ/ผิดพลาด/ซ้ำ, เลือก project, ส่ง prompt, duplicate click, timeout และการรักษา input
- ทดสอบ keyboard และ accessible names ของ controls หลัก; ไม่มี prior art เพราะยังไม่มี frontend codebase

## Out of Scope

Filesystem browser, remote projects, แก้ prompt ของ task ที่เริ่มแล้ว และ model/provider selector ใน V1

## Further Notes

ขีดจำกัดความยาว prompt และรายละเอียดสิทธิ์ต้องสอดคล้องกับ Backend และ runtime จริง
