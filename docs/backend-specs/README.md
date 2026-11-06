# Backend Feature Specifications

[ภาพรวมเดิม](backend-spec.md) เป็นบริบทเริ่มต้น; หากรายละเอียดขัดกัน ให้ยึด feature spec ด้านล่างเป็นข้อเสนอที่ละเอียดกว่า

## V1 — MVP

1. [Project Registry และ Local Service Security](v1/01-project-security.md)
2. [Codex Task Execution และ Cancellation](v1/02-task-execution.md)
3. [Task History และ Real-time Events](v1/03-events-history.md)
4. [Agent Approval](v1/04-approvals.md)

V1 ทั้งสี่ส่วนประกอบกันเป็น MVP เดียว ต้องพิสูจน์ integration และ approval capability ก่อนเริ่ม implementation

## V2 — Roadmap ที่เสนอ

1. [Multi-provider Agents](v2/01-multi-provider.md)
2. [Workflow Handoff และ Subagent Visibility](v2/02-workflow-subagents.md)

V2 ขึ้นกับ V1 และต้องตรวจความสามารถจริงของแต่ละ provider ก่อนสรุป contract; ยังไม่ใช่ข้อผูกมัดด้าน feature parity

## Infrastructure ที่กำหนดแล้ว

ใช้ PostgreSQL และ Docker Compose สำหรับ local development พร้อม persistent volume; รายละเอียด version, migrations, credentials และการรัน Backend ใน container ยังต้องกำหนดก่อน implementation

## ข้อตัดสินใจที่ยังเปิด

- วิธีเชื่อมต่อ Codex และ capability ของ provider, interactive approval และ subagent events
- ขนาด input, retention และขอบเขตข้อมูล log

## Test seam ที่ยืนยันแล้ว

ทดสอบผ่าน HTTP API เป็นหลัก โดยใช้ Agent runtime จำลองเพื่อควบคุมผลลัพธ์และเหตุการณ์ และมี opt-in smoke test กับ provider จริง
