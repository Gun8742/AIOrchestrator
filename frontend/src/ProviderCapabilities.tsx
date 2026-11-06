import type { Provider } from './api'

export function ProviderCapabilities({provider}: {provider?: Provider}) {
  if (!provider) return null
  return <p className="hint">การยกเลิกผ่านเว็บ: {provider.capabilities.cancellation ? 'รองรับ' : 'ไม่รองรับ'} · Interactive approval: {provider.capabilities.approval ? 'รองรับ' : 'ไม่รองรับ'}</p>
}
