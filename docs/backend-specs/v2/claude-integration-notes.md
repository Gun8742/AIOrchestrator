# Claude Code integration: ข้อมูลสำหรับ V2

ตรวจเอกสารทางการเมื่อ 2026-10-28; ข้อมูลนี้ยังไม่ยืนยันว่า integration ใช้งานได้ในเครื่องหรือบัญชีของผู้ใช้

- [Claude Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview) ระบุว่า SDK สำหรับ Python และ TypeScript เรียกใช้ Claude Code binary และมี agent loop, tools และ permissions; Go backend จึงต้องมี process adapter หรือเลือก integration อื่นที่พิสูจน์ได้
- [Programmatic CLI](https://code.claude.com/docs/en/headless) รองรับ non-interactive mode และ structured output แต่คำขอ permission ที่ไม่มี permission host จะถูกปฏิเสธ ไม่ใช่ approval ผ่านเว็บโดยอัตโนมัติ
- เอกสารเดียวกันระบุว่า `claude -p` ปกติอาจโหลด hooks และ MCP จาก configuration ของ project โดยไม่มี workspace trust dialog; `--bare` ปิดการค้นหาเหล่านี้ แต่มีข้อจำกัดด้าน authentication และ configuration ต้องตรวจให้ตรงกับวิธีใช้งานจริงก่อนเลือก
- [SDK permissions](https://code.claude.com/docs/en/agent-sdk/permissions) และ [approval callback](https://code.claude.com/docs/en/agent-sdk/user-input) อธิบาย `canUseTool` สำหรับคำขอที่ต้องถามผู้ใช้ โดยการตัดสิน permission อาจเกิดจาก rules หรือ hooks ก่อน callback จึงห้ามสมมติว่าทุก tool call จะเข้าหน้า approval
- [Subagents](https://code.claude.com/docs/en/agent-sdk/subagents) มีข้อมูลให้ติดตามผ่าน SDK แต่ต้องตรวจ event และความสัมพันธ์กับ task จริงก่อนแสดงใน UI

ก่อนเปิด provider ต้องพิสูจน์ authentication และเงื่อนไขการใช้งาน, streaming/result, cancellation รวม child processes, approval/denial, error และ event mapping ด้วย runtime จริงและ HTTP integration tests ตาม V2 Spec โดยไม่ให้ task ข้ามคิวหรือ bypass permission. จนกว่าจะผ่านให้รายงาน `available: false`.

## สถานะเครื่องที่ตรวจ

- `claude --version` รายงาน 2.1.260 และ `claude auth status` รายงานว่าล็อกอินด้วย OAuth แล้ว; ไม่ได้อ่านหรือบันทึก credential
- `--bare` ของ CLI ที่ติดตั้งระบุว่าไม่อ่าน OAuth หรือ keychain และต้องใช้ API key หรือ `apiKeyHelper`; การล็อกอิน CLI ที่พบจึงไม่ยืนยันว่า bare-mode integration พร้อมใช้
- [Claude API authentication](https://platform.claude.com/docs/en/manage-claude/authentication) อธิบายวิธีใช้ API key สำหรับ API; ยังไม่ได้ตรวจว่ามี key หรือสิทธิ์และค่าใช้จ่ายที่เหมาะสมสำหรับ SDK ใน environment นี้
- ยังไม่ได้ติดตั้ง SDK หรือรัน task จริง และยังไม่ใช้ OAuth ที่พบเป็นหลักฐานว่า Agent SDK ของแอปสามารถใช้ได้ตามเงื่อนไข
- ติดตั้ง `@anthropic-ai/claude-agent-sdk` 0.3.312 เฉพาะใน directory ชั่วคราวเพื่อตรวจ type definitions: `query` ส่ง `SDKMessage` ผ่าน async generator, `canUseTool` รับ permission callback และ `Query.interrupt()` มีให้ใน streaming input/output mode; ยังต้องพิสูจน์ว่า interrupt หยุด tool และ subprocess ก่อนปล่อยคิวได้จริง ไม่ถือว่า receipt เป็นหลักฐานเพียงพอ
- SDK นี้ต้องใช้ Node.js 18.14 ขึ้นไปตาม package metadata; ยังไม่ได้เพิ่ม dependency ลงโปรเจกต์หรือเลือก version สำหรับ production
- [Agent SDK quickstart](https://code.claude.com/docs/en/agent-sdk/quickstart) ระบุว่าผู้พัฒนาผลิตภัณฑ์บุคคลที่สามต้องไม่เสนอ claude.ai login หรือ rate limits เว้นแต่ได้รับอนุมัติ และให้ใช้ API key หรือ provider authentication ที่ระบุแทน; สถานะ OAuth ของ CLI จึงไม่ใช่ credential ที่ควรนำมาใช้กับแอปนี้โดยอัตโนมัติ
- Environment ของ backend ที่ตรวจไม่มี `ANTHROPIC_API_KEY` หรือ flag สำหรับ Bedrock, Vertex, Foundry หรือ Claude Platform on AWS; ตรวจเฉพาะว่าตัวแปรมีค่าหรือไม่ ไม่อ่านค่า credential และไม่ยืนยันสถานะการตั้งค่าใน shell อื่น
