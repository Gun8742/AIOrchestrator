# V1 — Project Registry และ Local Service Security

## Problem Statement

ผู้ใช้ต้องเลือก repository ให้ Agent ทำงานบนเครื่องได้ โดยไม่เปิดช่องให้เว็บไซต์อื่นสั่งงาน local service หรือเข้าถึงโปรเจกต์โดยไม่ตั้งใจ

## Solution

Backend รับเฉพาะโปรเจกต์ที่ผู้ใช้ลงทะเบียน ตรวจสอบการเข้าถึงทุก API และทำงานเฉพาะบน loopback สำหรับผู้ใช้คนเดียว

## User Stories

1. ในฐานะผู้ใช้ ฉันต้องการลงทะเบียน local project directory เพื่อสั่งงานในโปรเจกต์ที่เลือก
2. ในฐานะผู้ใช้ ฉันต้องการเห็นชื่อและ path ที่แท้จริงของโปรเจกต์เพื่อยืนยันพื้นที่ทำงาน
3. ในฐานะผู้ใช้ ฉันต้องการเห็นข้อผิดพลาดเมื่อ path ไม่มีอยู่หรือไม่ใช่ directory เพื่อแก้ไขได้
4. ในฐานะผู้ใช้ ฉันต้องการให้ path ซ้ำไม่สร้างรายการซ้ำเพื่อเลือกโปรเจกต์ได้ถูกต้อง
5. ในฐานะผู้ใช้ ฉันต้องการให้โปรเจกต์ยังปรากฏหลัง restart เพื่อไม่ต้องลงทะเบียนใหม่
6. ในฐานะผู้ใช้ ฉันต้องการให้เว็บอื่นไม่สามารถอ่านหรือสั่งงาน service เพื่อปกป้องข้อมูลและไฟล์
7. ในฐานะผู้ใช้ ฉันต้องการทราบเมื่อโปรเจกต์ที่ลงทะเบียนไว้เข้าไม่ได้ เพื่อไม่ให้ task เริ่มใน directory ผิด

## Implementation Decisions

- รัน local service บน loopback เท่านั้น; MVP รองรับผู้ใช้คนเดียวและไม่เปิด remote access
- Project มี opaque ID, display name, canonical absolute path และเวลาที่สร้าง เก็บแบบ persistent; canonical path ต้องไม่ซ้ำ. `GET /api/projects` แสดง queue state (`ready` หรือ `blocked`) และเหตุผลเมื่อ blocked; recovery ตรวจซ้ำและปลด block อัตโนมัติเมื่อยืนยันว่า execution เดิมหยุดแล้ว
- `POST /api/projects` รับ `{path}` ตรวจชนิด input, canonicalize path, ตรวจว่าเป็น directory ที่เข้าถึงได้ แล้วคืน project; `GET /api/projects` คืนรายการที่ลงทะเบียน
- ตรวจ path อีกครั้งก่อนเริ่ม task และปฏิเสธเมื่อ path เปลี่ยนหรือเข้าไม่ได้; การลงทะเบียนเป็นการเลือก working directory ไม่ใช่ filesystem sandbox ของ Agent
- ให้ Backend serve Frontend และ API จาก loopback origin เดียวกัน; development ใช้ same-origin proxy. ตรวจ Host แบบ exact allowlist และปฏิเสธ Origin ที่ไม่ตรง รวมถึง DNS rebinding; ตรวจ Origin บนคำขอที่เปลี่ยน state และใช้ CSRF token นอก cookie สำหรับทุก mutation
- Bootstrap session ด้วย one-time random secret ที่ local service พิมพ์ให้ผู้ใช้กรอกในหน้าเว็บผ่าน POST จาก origin ที่อนุญาต; secret อายุจำกัด ใช้ครั้งเดียว และไม่อยู่ใน URL หรือ logs. หลังตรวจสำเร็จออก HttpOnly, SameSite=Strict session cookie สำหรับ HTTP และ SSE; ไม่ใช้ wildcard CORS หรือเปิด bootstrap ให้เว็บอื่นเข้าถึง. กำหนด cookie Secure เมื่อใช้ HTTPS และทดสอบ behavior บน localhost ตาม browser ที่รองรับ
- Bootstrap response ส่ง CSRF token ให้ Frontend เก็บใน memory; `GET /api/session` ที่ต้องมี session และผ่าน Host/Origin checks คืน CSRF token หลัง reload. API และ SSE ทุก endpoint ยกเว้น bootstrap ต้องตรวจ session; session cookie ไม่ใช่สิ่งทดแทน Origin/Host validation และ CSRF protection; ไม่พึ่งการซ่อนปุ่มใน UI
- 400 สำหรับ input/path ไม่ถูกต้อง, 403 สำหรับคำขอที่ไม่ผ่านการตรวจ, 409 เมื่อ path ซ้ำ พร้อม error ที่ปลอดภัย; ไม่เปิด filesystem browsing API ใน MVP

## Testing Decisions

- ทดสอบ observable behavior ผ่าน HTTP API เป็นหลัก โดยใช้ temporary directories และ persistent store จริง; ไม่มี test prior art เพราะยังไม่มี codebase
- ตรวจ path ไม่มีอยู่, file, symlink/canonical path ซ้ำ, permission error, restart, Host/Origin/session/CSRF ไม่ถูกต้อง, bootstrap secret หมดอายุหรือใช้ซ้ำ, SSE ที่ไม่ได้รับสิทธิ์ และ path ใช้ไม่ได้ก่อน start

## Out of Scope

Remote access, multi-user, directory browser, ลบโปรเจกต์ และการรับประกันว่า Agent เข้าถึงได้เฉพาะ working directory

## Further Notes

การเลือก path ไม่ได้จำกัดสิทธิ์ process; ต้องใช้ sandbox และ approval ของ runtime ตาม V1 Agent Execution
