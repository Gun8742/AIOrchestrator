# Frontend Feature Specifications

[ภาพรวมเดิม](frontend-spec.md) เป็นบริบทเริ่มต้น; หากรายละเอียดขัดกัน ให้ยึด feature spec ด้านล่างเป็นข้อเสนอที่ละเอียดกว่า

## V1 — MVP

1. [Project Selection และ Task Submission](v1/01-project-task.md)
2. [Pixel Art Office](v1/02-office.md)
3. [Task History, Activity และ Connection](v1/03-activity.md)
4. [Approval Interaction](v1/04-approval.md)

V1 ทั้งสี่ส่วนประกอบกันเป็นหน้าใช้งานเดียวและพึ่ง Backend V1

## V2 — Roadmap ที่เสนอ

1. [Provider Selection และ Office Agents](v2/01-providers.md)
2. [Workflow และ Subagent Visibility](v2/02-workflow-subagents.md)

V2 ขึ้นกับ capability และ API ของ Backend ที่ต้องพิสูจน์ก่อน implementation

## ข้อตัดสินใจที่ยังเปิด

- Visual style รายละเอียด, asset และสิทธิ์ใช้งาน
- ความสามารถจริงของ approval, provider และ subagent events
- Test seam ที่เสนอ: ทดสอบการใช้งานผ่าน UI ด้วย Backend responses จำลอง และตรวจ integration กับ Backend จริงเมื่อพร้อม
