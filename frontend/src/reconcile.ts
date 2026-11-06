const MAX_BUFFERED_EVENTS = 512

export class Reconciler<T> {
  private revision = 0
  private running = false
  private closed = false
  private buffered = 0
  private latestEvent = 0
  private snapshotSequence = 0
  private readonly load: () => Promise<T>
  private readonly publish: (value: T) => void
  private readonly fail: () => void
  private readonly sequence?: (value: T) => number

  constructor(
    load: () => Promise<T>,
    publish: (value: T) => void,
    fail: () => void,
    sequence?: (value: T) => number,
  ) {
    this.load = load
    this.publish = publish
    this.fail = fail
    this.sequence = sequence
  }

  changed(sequence?: number) {
    if (this.closed) return
    if (sequence === undefined || !this.sequence) {
      this.revision++
    } else {
      if (sequence <= this.snapshotSequence) return
      this.latestEvent = Math.max(this.latestEvent, sequence)
    }
    if (this.running && ++this.buffered > MAX_BUFFERED_EVENTS) {
      this.closed = true
      this.fail()
      return
    }
    if (!this.running) void this.refresh()
  }

  close() { this.closed = true }

  private async refresh() {
    this.running = true
    try {
      while (!this.closed) {
        const revision = this.revision
        this.buffered = 0
        const snapshot = await this.load()
        if (this.closed) return
        const sequence = this.sequence?.(snapshot) ?? 0
        if (revision === this.revision && this.latestEvent <= sequence) {
          this.snapshotSequence = sequence
          this.publish(snapshot)
          if (revision === this.revision && this.latestEvent <= sequence) return
        }
      }
    } catch {
      if (!this.closed) this.fail()
    } finally {
      this.running = false
    }
  }
}
