const encoder = new TextEncoder()

export function taskInputError(projectId: number, prompt: string, provider: string): string | null {
  if (encoder.encode(prompt).length > 12000) return 'ข้อความยาวเกิน 12,000 bytes เมื่อเข้ารหัส UTF-8'
  const body = JSON.stringify({projectId, prompt, provider})
  if (encoder.encode(body).length > 16 * 1024) return 'ข้อมูลคำขอยาวเกินขนาดที่ระบบรับได้ โปรดลดความยาวข้อความ'
  return null
}
