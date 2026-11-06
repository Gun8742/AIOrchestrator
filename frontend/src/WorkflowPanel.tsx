import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import * as api from './api'
import { ProviderCapabilities } from './ProviderCapabilities'
import { restorePanelFocus } from './focus'
import type { Project, Provider, WorkflowDetail, WorkflowStepInput, WorkflowSummary } from './api'

const stateLabels: Record<WorkflowSummary['status'], string> = {running: 'กำลังดำเนินการ', completed: 'เสร็จแล้ว', failed: 'ไม่สำเร็จ', cancelled: 'ยกเลิกแล้ว'}
const stepLabels: Record<string, string> = {queued: 'รอคิว', running: 'กำลังทำงาน', waiting_for_user: 'รอการตัดสินใจ', completed: 'เสร็จแล้ว', failed: 'ไม่สำเร็จ', cancelled: 'ยกเลิกแล้ว'}
const emptyStep = (): WorkflowStepInput => ({provider: 'codex', prompt: ''})

interface Props {
  csrf: string
  projectId: string
  projects: Project[]
  providers: Provider[]
  workflows: WorkflowSummary[]
  selectedId: number | null
  detail: WorkflowDetail | null
  live: boolean
  onSelect: (id: number) => void
  onSelectTask: (id: number) => void
  onRefresh: () => void
}

export function WorkflowPanel({csrf, projectId, projects, providers, workflows, selectedId, detail, live, onSelect, onSelectTask, onRefresh}: Props) {
  const [steps, setSteps] = useState<WorkflowStepInput[]>([emptyStep(), emptyStep()])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [cancelPending, setCancelPending] = useState(false)
  const detailPanel = useRef<HTMLDivElement>(null)
  const selected = detail?.id === selectedId ? detail : null
  useEffect(() => { if (live && detail?.id === selectedId) setCancelPending(false) }, [detail, live, selectedId])
  const inputValid = steps.every(step => step.prompt.trim() && new TextEncoder().encode(step.prompt).length <= 12000 && providers.some(provider => provider.id === step.provider && provider.available))
  const requestValid = new TextEncoder().encode(JSON.stringify({projectId: Number(projectId), steps})).length <= 16 * 1024

  function updateStep(index: number, field: keyof WorkflowStepInput, value: string) {
    setSteps(current => current.map((step, i) => i === index ? {...step, [field]: value} : step))
  }
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!projectId || !inputValid || !requestValid || !live || busy) return
    setBusy(true); setError('')
    try {
      const created = await api.createWorkflow(Number(projectId), steps, csrf)
      setSteps([emptyStep(), emptyStep()]); setCancelPending(false); onSelect(created.id); onSelectTask(created.firstTaskId); onRefresh()
    } catch {
      setError('สร้าง workflow ไม่สำเร็จหรือผลไม่แน่นอน โปรดตรวจรายการก่อนลองใหม่')
      onRefresh()
    } finally { setBusy(false) }
  }
  async function cancel() {
    if (!selected || busy || !live) return
    setBusy(true); setCancelPending(true); setError('')
    try { await api.cancelWorkflow(selected.id, csrf) }
    catch { setError('ยังยืนยันการยกเลิกไม่ได้ โปรดตรวจสถานะล่าสุด') }
    finally { setBusy(false); onRefresh(); restorePanelFocus(detailPanel.current, selected.id) }
  }

  return <section className="panel workflow-panel">
    <div className="panel-heading"><div><h2>Workflow</h2></div></div>
    <p className="panel-description">มอบหมายงานเป็นขั้นตามลำดับ</p>
    <p className="hint">โปรเจกต์: {projects.find(project => String(project.id) === projectId)?.path ?? 'เลือกโปรเจกต์ด้านบน'}</p>
    <form onSubmit={submit}>
      {steps.map((step, index) => <fieldset className="workflow-step" key={index}>
        <legend>ขั้นที่ {index + 1}</legend>
        <label htmlFor={`workflow-provider-${index}`}>Agent</label>
        <select id={`workflow-provider-${index}`} value={step.provider} onChange={event => updateStep(index, 'provider', event.target.value)}>
          {providers.map(provider => <option key={provider.id} value={provider.id} disabled={!provider.available}>{provider.name}{provider.available ? '' : ' · ยังไม่พร้อม'}</option>)}
        </select>
        <p className="hint">{providers.find(provider => provider.id === step.provider)?.reason ?? 'กำลังโหลดข้อมูล Agent'}</p>
        <ProviderCapabilities provider={providers.find(provider => provider.id === step.provider)} />
        <label htmlFor={`workflow-prompt-${index}`}>คำสั่ง</label>
        <textarea id={`workflow-prompt-${index}`} value={step.prompt} onChange={event => updateStep(index, 'prompt', event.target.value)} rows={3} maxLength={12000} required />
        {steps.length > 1 && <button type="button" className="secondary" onClick={() => setSteps(current => current.filter((_, i) => i !== index))}>ลบขั้นที่ {index + 1}</button>}
      </fieldset>)}
      <button type="button" className="secondary" disabled={steps.length >= 10 || busy} onClick={() => setSteps(current => [...current, emptyStep()])}>เพิ่มขั้น</button>
      <p className="hint">ผลลัพธ์ของแต่ละขั้นจะถูกส่งพร้อมคำสั่งไปยัง Agent ขั้นถัดไป และอาจมี source code หรือข้อมูลอ่อนไหว ระบบปกปิดเฉพาะ API key ที่ตั้งไว้บางรายการ จึงตรวจไม่พบความลับทุกชนิด หากคำสั่งรวมกับผลลัพธ์เกิน 12,000 bytes Workflow จะหยุดก่อนเริ่มขั้นถัดไป</p>
      {!inputValid && steps.some(step => new TextEncoder().encode(step.prompt).length > 12000) && <p className="warning">คำสั่งแต่ละขั้นต้องไม่เกิน 12,000 bytes (UTF-8)</p>}
      {!requestValid && <p className="warning">คำขอรวมยาวเกินขนาดที่ระบบรับได้</p>}
      <button className="primary" disabled={!projectId || !inputValid || !requestValid || !live || busy}>{busy ? 'กำลังดำเนินการ…' : 'เริ่ม Workflow'}</button>
    </form>
    {error && <p className="error" role="alert">{error}</p>}
    <h3>Workflow ล่าสุด</h3>
    {workflows.length === 0 ? <p className="hint">ยังไม่มี Workflow</p> : <div className="workflow-list">{workflows.map(workflow => <button type="button" className="secondary" key={workflow.id} onClick={() => {setCancelPending(false); onSelect(workflow.id)}} aria-pressed={selectedId === workflow.id}>#{workflow.id} · {projects.find(project => project.id === workflow.projectId)?.name ?? `โปรเจกต์ #${workflow.projectId}`} · {stateLabels[workflow.status]}</button>)}</div>}
    {selectedId !== null && !selected && <p className="hint">กำลังโหลด Workflow…</p>}
    {selected && <div className="workflow-detail" ref={detailPanel} data-record-id={selected.id} tabIndex={-1} role="region" aria-label={`Workflow #${selected.id}`}>
      <div role="status" aria-live="polite" aria-atomic="true"><h4>Workflow #{selected.id} · {stateLabels[selected.status]}</h4></div>
      {selected.error && <p className="warning">{selected.error}</p>}
      <ol>{selected.steps.map(step => <li key={step.index}>
        <strong>ขั้นที่ {step.index + 1} · {providers.find(provider => provider.id === step.provider)?.name ?? step.provider}</strong>
        <p>{step.status ? stepLabels[step.status] : 'ยังไม่เริ่ม'} · {step.prompt}</p>
        {step.taskId !== undefined && <button type="button" className="secondary" onClick={() => {if (step.taskId !== undefined) onSelectTask(step.taskId)}}>ดู Task #{step.taskId}</button>}
        {step.result && <pre>{step.result}</pre>}{step.error && <pre>{step.error}</pre>}
      </li>)}</ol>
      {selected.status === 'running' && <button type="button" className="danger" onClick={cancel} disabled={busy || !live || cancelPending || selected.cancelRequested}>{cancelPending || selected.cancelRequested ? 'กำลังรอการยกเลิก…' : 'ยกเลิก Workflow'}</button>}
    </div>}
  </section>
}
