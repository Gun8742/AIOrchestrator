import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import * as api from './api'
import type { Project, Task, TaskDetail, TaskStatus, TaskEvent, Provider } from './api'
import { Reconciler } from './reconcile'
import { taskInputError } from './task-validation'
import { restorePanelFocus } from './focus'
import { latestSubagentActivities } from './subagents'
import { WorkflowPanel } from './WorkflowPanel'
import { ProviderCapabilities } from './ProviderCapabilities'
import type { WorkflowDetail, WorkflowSummary } from './api'

const labels: Record<TaskStatus, string> = { queued: 'รอคิว', running: 'กำลังทำงาน', waiting_for_user: 'รอการตัดสินใจ', completed: 'เสร็จแล้ว', failed: 'ไม่สำเร็จ', cancelled: 'ยกเลิกแล้ว' }
const terminal = new Set<TaskStatus>(['completed', 'failed', 'cancelled'])
const subagentEvents: Record<string, string> = {started: 'เริ่มทำงาน', interacted: 'มีการติดต่อ', interrupted: 'ถูกขัดจังหวะ', completed: 'เสร็จสิ้น'}
const activityNames: Record<string, string> = {agentMessage: 'ข้อความจาก Agent', commandExecution: 'คำสั่ง', fileChange: 'การแก้ไขไฟล์', mcpToolCall: 'การใช้เครื่องมือ', reasoning: 'การวิเคราะห์', collabAgentToolCall: 'การทำงานร่วมกับ Agent', subAgentActivity: 'กิจกรรมของ Subagent'}
function eventLabel(event: TaskEvent) {
  if (event.type === 'status') return 'สถานะเปลี่ยน'
  if (event.type === 'approval') return 'การอนุมัติ'
  if (event.type === 'result') return 'ผลลัพธ์'
  if (event.type === 'error') return 'ข้อผิดพลาด'
  if (event.type !== 'activity') return event.type
  const itemType = String(event.payload.itemType ?? '')
  const phase = event.payload.phase === 'started' ? 'เริ่ม' : 'เสร็จสิ้น'
  return `${phase}: ${activityNames[itemType] ?? 'กิจกรรมของ Agent'}`
}
function eventContent(event: TaskEvent) {
  const value = event.payload[event.type]
  if ((event.type === 'result' || event.type === 'error') && typeof value === 'string') return value
  return JSON.stringify(event.payload, null, 2)
}
const formatTime = (value: string) => new Date(value).toLocaleString('th-TH', {dateStyle: 'short', timeStyle: 'short'})

function GuildScene({selected, providerName, onSelect}: {selected?: Task; providerName: string; onSelect: () => void}) {
  const status = selected ? labels[selected.status] : 'พร้อมรับงาน'
  return <section className="guild" aria-label={`โถง guild ของ ${providerName}`}>
    <div className="scene-header"><div><span className="scene-kicker">THE GUILD HALL</span><h2>โถงนักผจญภัย</h2></div><span className="scene-live">LOCAL OFFICE</span></div>
    <div className="scene">
      <div className="back-wall" aria-hidden="true"><div className="banner banner-left"><span>✦</span></div><div className="window"><i /><i /><i /><i /></div><div className="banner banner-right"><span>✦</span></div><div className="shelf"><i /><i /><i /></div></div>
      <div className="floor"><div className="rug" /><div className="table"><div className="table-top"><i /><b /></div><div className="table-legs" /></div><div className="plant"><i /><b /></div><div className="crate" /><button type="button" className={`character ${selected?.status === 'running' ? 'working' : ''}`} aria-label={`${providerName}: ${status}${selected ? ' · ดูรายละเอียดงาน' : ''}`} disabled={!selected} onClick={onSelect}>{selected?.status === 'waiting_for_user' && <span className="approval-marker" aria-hidden="true">!</span>}<span className="hat" /><span className="head"><i /><i /></span><span className="body" /><span className="feet" /></button></div>
    </div>
    <div className="scene-footer"><div><span className="agent-dot" /><strong>{providerName}</strong><span className="agent-role">Guild member</span></div><button type="button" className="agent-status" onClick={onSelect} disabled={!selected}>{status}{selected ? ' · ดูงาน' : ''}</button></div>
  </section>
}

function SignIn({onLogin}: {onLogin: (token: string) => void}) {
  const [secret, setSecret] = useState(''); const [error, setError] = useState(''); const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {e.preventDefault(); setBusy(true); setError(''); try { const result = await api.bootstrap(secret); setSecret(''); onLogin(result.csrfToken) } catch { setError('รหัสไม่ถูกต้องหรือหมดอายุ โปรดเริ่ม Backend ใหม่และใช้รหัสล่าสุด') } finally { setBusy(false) }}
  return <main className="login-wrap"><div className="login-card"><div className="crest">A</div><p className="brand">AGENT GUILD</p><h1>ยินดีต้อนรับกลับ<br />สู่ guild</h1><p className="subtle">กรอกรหัสครั้งเดียวที่แสดงใน Terminal ของ Backend เพื่อเปิดห้องทำงานบนเครื่องนี้</p><form onSubmit={submit}><label htmlFor="secret">รหัสเข้าใช้งาน</label><input id="secret" type="password" autoComplete="off" value={secret} onChange={e => setSecret(e.target.value)} required /><button className="primary" disabled={busy || !secret}>{busy ? 'กำลังตรวจสอบ…' : 'เข้า guild →'}</button>{error && <p className="error" role="alert">{error}</p>}</form></div></main>
}

function App() {
  const [csrf, setCsrf] = useState<string | null>(null); const [checking, setChecking] = useState(true); const [sessionError, setSessionError] = useState(false); const [sessionRetry, setSessionRetry] = useState(0)
  const [providerList, setProviders] = useState<Provider[]>([]); const [providerId, setProviderId] = useState('codex'); const [projectList, setProjects] = useState<Project[]>([]); const [taskList, setTasks] = useState<Task[]>([])
  const [workflowList, setWorkflows] = useState<WorkflowSummary[]>([]); const [workflowId, setWorkflowId] = useState<number | null>(null); const [workflowDetail, setWorkflowDetail] = useState<WorkflowDetail | null>(null)
  const [projectId, setProjectId] = useState(''); const [selectedId, setSelectedId] = useState<number | null>(null); const [detail, setDetail] = useState<TaskDetail | null>(null)
  const [prompt, setPrompt] = useState(''); const [path, setPath] = useState(''); const [showProject, setShowProject] = useState(false)
  const [connection, setConnection] = useState<'connecting' | 'live' | 'offline'>('connecting'); const [error, setError] = useState(''); const [busy, setBusy] = useState('')
  const [refresh, setRefresh] = useState(0); const [cancelPendingId, setCancelPendingId] = useState<number | null>(null)
  useEffect(() => { let active = true; setChecking(true); setSessionError(false); api.session().then(s => {if (active) setCsrf(s.csrfToken)}).catch(error => {if (active) {setCsrf(null); setSessionError(!(error instanceof api.ApiError && error.status === 401))}}).finally(() => {if (active) setChecking(false)}); return () => {active = false} }, [sessionRetry])
  useEffect(() => {
    if (!csrf) return
    let stopped = false; let source: EventSource | undefined; let retry: ReturnType<typeof setTimeout> | undefined; let sync: Reconciler<{providers: Provider[]; projects: Project[]; tasks: Task[]; detail: TaskDetail | null; workflows: WorkflowSummary[]; workflowDetail: WorkflowDetail | null; sequence: number}> | undefined
    const connect = () => {
      if (stopped) return
      setConnection('connecting'); source = new EventSource('/api/events')
      sync = new Reconciler(async () => { const [providers, p, t, d, w, wd] = await Promise.all([api.providers(), api.projects(), api.tasks(), selectedId === null ? Promise.resolve(null) : api.task(selectedId), api.workflows(), workflowId === null ? Promise.resolve(null) : api.workflow(workflowId)]); return {providers, projects: p, tasks: t.tasks, detail: d, workflows: w.workflows, workflowDetail: wd, sequence: Math.min(t.sequence, w.sequence, d?.sequence ?? Infinity, wd?.sequence ?? Infinity)} }, snapshot => {setProviders(snapshot.providers); setProjects(snapshot.projects); setTasks(snapshot.tasks); setDetail(snapshot.detail); setWorkflows(snapshot.workflows); setWorkflowDetail(snapshot.workflowDetail); setConnection('live')}, () => {setConnection('offline'); source?.close(); sync?.close(); if (!stopped) retry = setTimeout(connect, 2000)}, snapshot => snapshot.sequence)
      source.addEventListener('ready', () => sync?.changed())
      source.addEventListener('task', event => {
        try {
          const value: unknown = JSON.parse(event.data)
          // Project state is loaded separately and has no snapshot sequence.
          if (typeof value === 'object' && value !== null && 'type' in value && (value.type === 'status' || value.type === 'project')) { sync?.changed(); return }
          if (typeof value === 'object' && value !== null && 'sequence' in value && typeof value.sequence === 'number' && Number.isSafeInteger(value.sequence) && value.sequence > 0) { sync?.changed(value.sequence); return }
        } catch { /* Re-fetch if the event cannot be decoded. */ }
        sync?.changed()
      })
      source.onerror = () => {
        setConnection('offline'); source?.close(); sync?.close()
        void api.sessionExpired().then(expired => {
          if (stopped) return
          if (expired) { setCsrf(null); setCancelPendingId(null); return }
          retry = setTimeout(connect, 2000)
        })
      }
    }
    connect(); return () => {stopped = true; source?.close(); sync?.close(); if (retry) clearTimeout(retry)}
  }, [csrf, selectedId, workflowId, refresh])
  useEffect(() => {if (!projectId && projectList.length) setProjectId(String(projectList[0].id))}, [projectId, projectList])
  useEffect(() => {if (cancelPendingId !== null && taskList.some(task => task.id === cancelPendingId && terminal.has(task.status))) setCancelPendingId(null)}, [cancelPendingId, taskList])
  const selected = taskList.find(t => t.id === selectedId)
  const currentDetail = detail?.task.id === selectedId ? detail : null
  const subagents = currentDetail ? latestSubagentActivities(currentDetail.events) : []
  const provider = providerList.find(p => p.id === providerId)
  const project = projectList.find(p => String(p.id) === projectId)
  const promptError = taskInputError(Number(projectId), prompt, providerId)
  function reload() {setRefresh(value => value + 1)}
  async function addProject(e: FormEvent) { e.preventDefault(); if (!csrf) return; setBusy('project'); setError(''); try {const p = await api.addProject(path, csrf); setProjectId(String(p.id)); setPath(''); setShowProject(false); reload()} catch (e) {setError(e instanceof api.ApiError && e.status === 409 ? 'โปรเจกต์นี้ถูกเพิ่มแล้ว' : 'เพิ่มโปรเจกต์ไม่ได้ โปรดตรวจสอบ path และสิทธิ์เข้าถึง')} finally {setBusy('')} }
  async function submitTask(e: FormEvent) {e.preventDefault(); if (!csrf || !projectId || !prompt.trim() || promptError) return; setBusy('submit'); setError(''); try { const t = await api.createTask(Number(projectId), prompt, providerId, csrf); setPrompt(''); setSelectedId(t.id); reload() } catch {setError('ส่งงานไม่สำเร็จหรือผลการส่งไม่แน่นอน โปรดตรวจรายการงานก่อนลองใหม่')} finally {setBusy('')} }
  async function cancelTask() {if (!csrf || !selected) return; const id = selected.id; setBusy('cancel'); setCancelPendingId(id); setError(''); try {const current = await api.cancelTask(id, csrf); if (terminal.has(current.status)) setCancelPendingId(null)} catch {setCancelPendingId(null); setError('ยังยืนยันการยกเลิกไม่ได้ กำลังโหลดสถานะล่าสุด')} finally {setBusy(''); reload(); restorePanelFocus(document.getElementById('task-detail'), id)} }
  async function answer(decision: 'approve' | 'deny') {if (!csrf || !currentDetail?.pendingApproval) return; setBusy('approval'); setError(''); try {await api.answerApproval(currentDetail.task.id, currentDetail.pendingApproval.id, decision, csrf)} catch {setError('ส่งคำตอบไม่สำเร็จหรือคำขอหมดอายุ โปรดตรวจสถานะล่าสุด')} finally {setBusy(''); reload(); restorePanelFocus(document.getElementById('task-detail'), currentDetail.task.id)} }
  if (checking) return <main className="login-wrap"><p>กำลังตรวจสอบการเชื่อมต่อ…</p></main>
  if (sessionError) return <main className="login-wrap"><div className="login-card"><h1>เชื่อมต่อ guild ไม่ได้</h1><p className="subtle">ตรวจสอบว่า Backend ทำงานอยู่ แล้วลองอีกครั้ง</p><button className="primary" onClick={() => setSessionRetry(value => value + 1)}>ลองเชื่อมต่ออีกครั้ง</button></div></main>
  if (!csrf) return <SignIn onLogin={setCsrf} />
  return <div className="app-shell">
    <header className="topbar"><div className="wordmark"><span className="brand-mark">A</span><div><strong>AGENT GUILD</strong><small>YOUR LOCAL WORKSPACE</small></div></div><div className="topbar-right"><span className={`connection ${connection}`} role="status" aria-live="polite"><i />{connection === 'live' ? 'เชื่อมต่อแล้ว' : connection === 'connecting' ? 'กำลังเชื่อมต่อ' : 'ขาดการเชื่อมต่อ · ข้อมูลอาจเก่า'}</span>{connection === 'offline' && <button type="button" className="secondary" onClick={reload}>ลองเชื่อมต่ออีกครั้ง</button>}<span className="topbar-divider" /><span className="location">LOCAL GUILD</span></div></header>
    <main className="workspace"><div className="intro"><div><p className="eyebrow">YOUR WORKSPACE / GUILD HALL</p><h1>มอบหมายภารกิจให้ guild</h1><p>วางแผน สั่งงาน และติดตามความคืบหน้าของ Agent ในที่เดียว</p></div><span className="edition">✦ &nbsp; ADVENTURER'S DESK</span></div>
      {error && <div className="error-banner" role="alert">{error}<button type="button" onClick={() => setError('')} aria-label="ปิดข้อความ">×</button></div>}
      <div className="layout"><aside className="left-column"><section className="panel quest-panel"><div className="panel-heading"><div><span className="section-number">✦</span><h2>มอบหมายภารกิจ</h2></div></div><p className="panel-description">บอก Agent ว่าต้องการให้ช่วยอะไร</p><form onSubmit={submitTask}><label htmlFor="provider">Agent</label><select id="provider" value={providerId} onChange={e => setProviderId(e.target.value)}>{providerList.map(p => <option key={p.id} value={p.id} disabled={!p.available}>{p.name}{p.available ? '' : ' · ยังไม่พร้อม'}</option>)}</select><p className="hint">{provider?.reason ?? 'กำลังโหลดข้อมูล Agent'}</p><ProviderCapabilities provider={provider} /><details className="provider-details"><summary>สถานะ Agent ทั้งหมด</summary><ul>{providerList.map(p => <li key={p.id}><strong>{p.name}</strong>: {p.available ? 'พร้อมใช้' : 'ยังไม่พร้อม'} — {p.reason}</li>)}</ul></details><label htmlFor="project">โปรเจกต์</label><div className="project-row"><select id="project" value={projectId} onChange={e => setProjectId(e.target.value)} required><option value="">เลือกโปรเจกต์</option>{projectList.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select><button type="button" className="icon-button" onClick={() => setShowProject(!showProject)} aria-label="เพิ่มโปรเจกต์">+</button></div>{project && <p className="path">{project.path}</p>}{project?.queueState === 'blocked' && <div className="warning"><p>คิวถูกระงับ: {project.blockedReason || 'ตรวจสอบการทำงานเดิมก่อนเริ่มงาน'}</p><p>เริ่ม Backend อีกครั้งเพื่อให้ระบบตรวจสอบใหม่ สำหรับงาน Codex อาจต้องรีสตาร์ตเครื่อง macOS เดิมก่อน หากยังระงับอยู่ โปรดดูขั้นตอนใน README ของโปรเจกต์</p></div>}<label htmlFor="prompt">รายละเอียดภารกิจ</label><textarea id="prompt" value={prompt} onChange={e => setPrompt(e.target.value)} placeholder="เช่น ช่วยตรวจโค้ดและสรุปจุดที่ควรปรับปรุง" rows={5} maxLength={12000} aria-describedby="prompt-size" aria-invalid={!!promptError} required /><p id="prompt-size" className="hint">{promptError ?? 'จำกัดข้อความ 12,000 bytes (UTF-8)'}</p><p className="hint">Agent อาจอ่านหรือแก้ไฟล์ตามสิทธิ์ที่ตั้งไว้</p><button className="primary" disabled={busy !== '' || !projectId || !prompt.trim() || !provider?.available || !!promptError || connection !== 'live'}>{busy === 'submit' ? 'กำลังส่ง…' : 'ส่งภารกิจ  →'}</button></form>{showProject && <form className="add-project" onSubmit={addProject}><label htmlFor="path">เพิ่ม local project path</label><input id="path" value={path} onChange={e => setPath(e.target.value)} placeholder="/Users/you/Projects/my-app" required /><button className="secondary" disabled={busy !== ''}>เพิ่มโปรเจกต์</button></form>}</section>
      <WorkflowPanel csrf={csrf} projectId={projectId} projects={projectList} providers={providerList} workflows={workflowList} selectedId={workflowId} detail={workflowDetail} live={connection === 'live'} onSelect={setWorkflowId} onSelectTask={setSelectedId} onRefresh={reload} />
      <section className="panel task-panel"><div className="panel-heading"><div><span className="section-number">☰</span><h2>กระดานภารกิจ</h2></div><span className="count">{taskList.length}</span></div><p className="panel-description">งานล่าสุดของ guild</p>{taskList.length === 0 ? <div className="empty">ยังไม่มีภารกิจ<br /><span>ส่งงานแรกให้ Agent เพื่อเริ่มต้น</span></div> : <div className="task-list">{taskList.map(t => <button type="button" key={t.id} className={`task-item ${selectedId === t.id ? 'selected' : ''}`} onClick={() => setSelectedId(t.id)}><span className={`status-dot ${t.status}`} /><span className="task-copy"><strong>{t.prompt}</strong><small>{labels[t.status]} · {providerList.find(p => p.id === t.provider)?.name ?? t.provider} · {formatTime(t.createdAt)}</small></span><span className="arrow">›</span></button>)}</div>}</section></aside>
      <div className="center-column"><GuildScene selected={selected} providerName={selected ? providerList.find(p => p.id === selected.provider)?.name ?? selected.provider : 'Agent'} onSelect={() => document.getElementById('task-detail')?.focus()} /><div className="floor-note"><span>✦</span><p><strong>ทุกภารกิจมีเรื่องราว</strong><br />สถานะในโถงนี้อัปเดตจากการทำงานจริงของ Agent</p></div></div>
      <aside className="panel detail-panel" id="task-detail" data-record-id={currentDetail?.task.id} tabIndex={-1}><div className="panel-heading"><div><span className="section-number">◇</span><h2>รายละเอียดภารกิจ</h2></div></div>{!currentDetail ? <div className="detail-empty"><div className="scroll-icon">▤</div><strong>{selectedId === null ? 'เลือกภารกิจจากกระดาน' : 'กำลังโหลดรายละเอียดภารกิจ…'}</strong><p>รายละเอียด เหตุการณ์ และผลลัพธ์จะแสดงที่นี่</p></div> : <div className="detail-content"><div className="detail-status" role="status" aria-live="polite"><span className={`status-dot ${currentDetail.task.status}`} />{labels[currentDetail.task.status]}</div><h3>{currentDetail.task.prompt}</h3><p className="meta">ภารกิจ #{currentDetail.task.id} · {providerList.find(p => p.id === currentDetail.task.provider)?.name ?? currentDetail.task.provider}</p><p className="meta">สร้างเมื่อ: {formatTime(currentDetail.task.createdAt)} · อัปเดตล่าสุด: {formatTime(currentDetail.task.updatedAt)}</p><p className="meta">โปรเจกต์: {projectList.find(p => p.id === currentDetail.task.projectId)?.name ?? `#${currentDetail.task.projectId}`}</p>{currentDetail.pendingApproval && <div className="approval"><strong>รอการตัดสินใจของคุณ</strong><p className="meta">ร้องขอเมื่อ: {formatTime(currentDetail.pendingApproval.createdAt)}</p><p>ตรวจสอบรายละเอียดก่อนตอบ</p>{currentDetail.pendingApproval.details.approveAllowed !== true && <p>ข้อมูลการกระทำไม่ครบ จึงอนุมัติไม่ได้ คุณยังปฏิเสธได้</p>}<pre>{JSON.stringify(currentDetail.pendingApproval.details, null, 2)}</pre><div className="button-row"><button className="primary" onClick={() => answer('approve')} disabled={busy !== '' || connection !== 'live' || currentDetail.pendingApproval.details.approveAllowed !== true}>อนุมัติ</button><button className="secondary" onClick={() => answer('deny')} disabled={busy !== '' || connection !== 'live'}>ปฏิเสธ</button></div></div>}{currentDetail.task.result && <><h4>ผลลัพธ์</h4><pre>{currentDetail.task.result}</pre></>}{currentDetail.task.error && <><h4>ข้อผิดพลาด</h4><pre>{currentDetail.task.error}</pre></>}<h4>Subagents</h4>{subagents.length === 0 ? <p className="hint">ยังไม่มี event ของ Subagent จาก runtime จึงไม่สามารถยืนยันว่ามีหรือไม่มี Subagent</p> : <ul className="subagent-list">{subagents.map(agent => <li key={agent.agentThreadId}><strong>{agent.agentThreadId}</strong><span>Parent: {agent.parentThreadId}</span><span>Event ล่าสุด: {subagentEvents[agent.kind] ?? agent.kind}</span></li>)}</ul>}<h4>บันทึกการเดินทาง</h4><ol className="timeline">{currentDetail.events.map(e => <li key={e.sequence}><span className="timeline-dot" /><div><strong>{eventLabel(e)}</strong><small>{formatTime(e.timestamp)}</small>{e.type === 'activity' && e.payload.itemType === 'subAgentActivity' && typeof e.payload.agentThreadId === 'string' && typeof e.payload.parentThreadId === 'string' && typeof e.payload.kind === 'string' && <pre>Agent: {e.payload.agentThreadId}{"\n"}Parent: {e.payload.parentThreadId}{"\n"}Event: {e.payload.kind}</pre>}{e.type !== 'activity' && <pre>{eventContent(e)}</pre>}</div></li>)}</ol>{!terminal.has(currentDetail.task.status) && <>{providerList.find(p => p.id === currentDetail.task.provider)?.capabilities.cancellation ? <button type="button" className="danger" onClick={cancelTask} disabled={busy !== '' || connection !== 'live' || cancelPendingId === currentDetail.task.id}>{cancelPendingId === currentDetail.task.id ? 'กำลังรอการยกเลิก…' : 'ยกเลิกภารกิจ'}</button> : <p className="hint">Provider นี้ยังไม่รองรับการยกเลิกผ่านเว็บ</p>}<p className="hint">การยกเลิกไม่ย้อนการแก้ไขไฟล์ที่เกิดขึ้นแล้ว</p></>}</div>}</aside></div>
    </main><footer><span>AGENT GUILD</span><span>Built for thoughtful adventures · Local-first</span></footer>
  </div>
}
export default App
