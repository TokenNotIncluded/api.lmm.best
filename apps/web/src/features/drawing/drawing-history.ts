/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  createDrawingHistoryStore,
  retainDrawings,
  type DrawingHistoryStore,
  type DrawingMetadata,
  type StoredDrawing,
} from './history-storage'
import {
  drawingBase64Blob,
  drawingSource,
  fetchDrawingBlob,
  type GeneratedDrawing,
} from './image-bytes'

export type DrawingPreview = DrawingMetadata & {
  id: string
  userId: number
  revisedPrompt?: string
  src: string
  blob?: Blob
  saved: boolean
}

type Snapshot = {
  images: DrawingPreview[]
  loading: boolean
  saving: boolean
  clearing: boolean
  warning: 'load' | 'save' | 'clear' | null
}

// A session owns only generated-image URLs. Reference-upload URLs are managed
// separately by the workbench. A ticket is invalidated on clear/account change.
export class DrawingHistory {
  private snapshot: Snapshot = {
    images: [],
    loading: true,
    saving: false,
    clearing: false,
    warning: null,
  }
  private listeners = new Set<() => void>()
  private urls = new Set<string>()
  private revision = 0
  private active = false
  private epoch: number | null = null
  private controller = new AbortController()
  private ready: Promise<void> = Promise.resolve()

  constructor(
    private readonly userId: number,
    private readonly store: DrawingHistoryStore = createDrawingHistoryStore(),
    private readonly download: typeof fetchDrawingBlob = fetchDrawingBlob
  ) {}

  getSnapshot = () => this.snapshot
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }
  capture = () => this.revision
  private current = (ticket: number) => this.active && ticket === this.revision

  private update(patch: Partial<Snapshot>) {
    this.snapshot = { ...this.snapshot, ...patch }
    const used = new Set(this.snapshot.images.map((image) => image.src))
    for (const url of this.urls) {
      if (!used.has(url)) {
        URL.revokeObjectURL(url)
        this.urls.delete(url)
      }
    }
    for (const listener of this.listeners) listener()
  }

  private preview(image: StoredDrawing, saved: boolean): DrawingPreview {
    const src = URL.createObjectURL(image.blob)
    this.urls.add(src)
    return { ...image, src, saved }
  }

  start = () => {
    this.active = true
    this.controller = new AbortController()
    const ticket = ++this.revision
    this.update({ loading: true })
    this.ready = this.store
      .load(this.userId)
      .then((data) => {
        if (!this.current(ticket)) return
        this.epoch = data.epoch
        const images = data.images
          .filter((image) => image.userId === this.userId)
          .map((image) => this.preview(image, true))
        this.update({
          images: retainDrawings([...images, ...this.snapshot.images]),
        })
      })
      .catch(() => {
        if (this.current(ticket)) this.update({ warning: 'load' })
      })
      .finally(() => {
        if (this.current(ticket)) this.update({ loading: false })
      })
  }

  remember = async (
    raw: GeneratedDrawing[],
    metadata: DrawingMetadata,
    ticket: number
  ): Promise<void> => {
    if (!this.current(ticket) || this.snapshot.clearing) return
    const signal = this.controller.signal
    const additions: DrawingPreview[] = []
    let failed = false
    for (const image of raw) {
      const src = drawingSource(image)
      if (!src) continue
      const base = {
        ...metadata,
        userId: this.userId,
        id: crypto.randomUUID(),
        revisedPrompt: image.revised_prompt,
      }
      try {
        additions.push(
          image.b64_json
            ? this.preview(
                { ...base, blob: drawingBase64Blob(image.b64_json) },
                false
              )
            : { ...base, src, saved: false }
        )
      } catch {
        failed = true
        additions.push({ ...base, src, saved: false })
      }
    }
    this.update({
      images: retainDrawings([...this.snapshot.images, ...additions]),
      saving: true,
    })
    try {
      const stored = await Promise.all(
        additions.map(async (image): Promise<StoredDrawing | null> => {
          try {
            const blob = image.blob ?? (await this.download(image.src, signal))
            if (!this.current(ticket)) return null
            const record = {
              ...metadata,
              id: image.id,
              userId: this.userId,
              revisedPrompt: image.revisedPrompt,
              blob,
            }
            if (!image.blob) {
              const preview = this.preview(record, false)
              this.update({
                images: retainDrawings(
                  this.snapshot.images.map((item) =>
                    item.id === image.id ? preview : item
                  )
                ),
              })
            }
            return record
          } catch {
            failed = true
            return null
          }
        })
      )
      await this.ready
      if (!this.current(ticket)) return
      const valid = stored.filter(
        (image): image is StoredDrawing => image !== null
      )
      if (this.epoch === null) throw new Error('Drawing cache unavailable')
      const saved = await this.store.save(this.userId, this.epoch, valid)
      if (!this.current(ticket)) return
      if (!saved) {
        // Another tab cleared the account while this request was running.
        this.update({ images: [], saving: false })
        this.start()
        return
      }
      const ids = new Set(valid.map((image) => image.id))
      this.update({
        images: this.snapshot.images.map((image) =>
          ids.has(image.id) ? { ...image, saved: true } : image
        ),
      })
    } catch {
      failed = true
    } finally {
      if (this.current(ticket)) {
        this.update({
          saving: false,
          ...(failed ? { warning: 'save' as const } : {}),
        })
      }
    }
  }

  clear = async (): Promise<void> => {
    if (!this.active || this.snapshot.clearing) return
    const ticket = ++this.revision
    this.controller.abort()
    this.controller = new AbortController()
    // Discard pending loads/fetches immediately; clear is never a generation retry.
    this.update({
      images: [],
      loading: false,
      saving: false,
      clearing: true,
      warning: null,
    })
    try {
      const epoch = await this.store.clear(this.userId)
      if (this.current(ticket)) this.epoch = epoch
    } catch {
      if (this.current(ticket)) this.update({ warning: 'clear' })
    } finally {
      if (this.current(ticket)) this.update({ clearing: false })
    }
  }

  dispose = () => {
    this.active = false
    this.revision++
    this.controller.abort()
    for (const url of this.urls) URL.revokeObjectURL(url)
    this.urls.clear()
    this.epoch = null
    this.snapshot = {
      images: [],
      loading: true,
      saving: false,
      clearing: false,
      warning: null,
    }
  }
}
