import { type FormEvent, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { YouTubeService, type DownloadedChapter, type DownloadUpdate } from '../bindings/aclip'

type Chapter = DownloadedChapter

function App() {
  const [url, setUrl] = useState('')
  const [started, setStarted] = useState(false)
  const [busy, setBusy] = useState(false)
  const [phase, setPhase] = useState('')
  const [title, setTitle] = useState('')
  const [message, setMessage] = useState('')
  const [progress, setProgress] = useState<number | null>(null)
  const [outputDir, setOutputDir] = useState('')
  const [chapters, setChapters] = useState<Chapter[]>([])
  const [error, setError] = useState('')

  useEffect(() => Events.On('download:update', (event) => {
    const update = event.data as DownloadUpdate
    setPhase(update.status)
    if (update.title) setTitle(update.title)
    if (update.message) setMessage(update.message)
    if (typeof update.progress === 'number') setProgress(update.progress)
    if (update.outputDir) setOutputDir(update.outputDir)
    if (update.chapters) setChapters(update.chapters)
    if (update.status === 'error') setError(update.message ?? 'Download failed')
  }), [])

  async function submitURL(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const videoURL = url.trim()
    if (!videoURL || busy) return

    setStarted(true)
    setBusy(true)
    setPhase('starting')
    setTitle('')
    setMessage('Finding video details…')
    setProgress(null)
    setOutputDir('')
    setChapters([])
    setError('')

    try {
      const result = await YouTubeService.DownloadYouTube(videoURL)
      setTitle(result.title)
      setOutputDir(result.outputDir)
      setChapters(result.chapters ?? [])
      setMessage('Download and subtitles complete')
    } catch (reason) {
      setPhase('error')
      setError(reason instanceof Error ? reason.message : String(reason))
      setMessage('The download could not be completed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={`app-shell${started ? ' app-shell--active' : ''}`}>
      <main className="page">
        <form className="entry-form" onSubmit={submitURL}>
          <label className="visually-hidden" htmlFor="youtube-url">
            YouTube video URL
          </label>
          <input
            id="youtube-url"
            type="text"
            inputMode="url"
            value={url}
            onChange={(event) => setUrl(event.target.value)}
            placeholder="Paste a YouTube URL"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            disabled={busy}
          />
          <button type="submit" disabled={!url.trim() || busy}>
            {busy ? <span className="button-spinner" aria-hidden="true" /> : null}
            <span>{busy ? 'Working…' : 'Download'}</span>
          </button>
        </form>

        {started && (
          <section className="download-results" aria-live="polite" aria-busy={busy}>
            <div className="video-summary">
              <p className="eyebrow">{title ? 'VIDEO' : 'PREPARING VIDEO'}</p>
              {title ? <h1>{title}</h1> : <div className="title-skeleton" />}
              <p className="status-message">{message}</p>
            </div>

            {progress !== null && busy && phase === 'downloading' && (
              <div
                className="progress-track"
                role="progressbar"
                aria-label="Download progress"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={Math.round(progress)}
              >
                <span style={{ width: `${progress}%` }} />
              </div>
            )}

            {chapters.length > 0 ? (
              <ol className="chapter-list">
                {chapters.map((chapter, index) => (
                  <li className="chapter-card" key={chapter.videoPath}>
                    <span className="chapter-index">{String(index + 1).padStart(2, '0')}</span>
                    <div className="chapter-copy">
                      <span className="chapter-name">{chapter.name}</span>
                      <span className={`subtitle-state subtitle-state--${chapter.subtitleStatus}`}>
                        <span className="state-dot" />
                        {subtitleLabel(chapter.subtitleStatus)}
                      </span>
                      {chapter.subtitleError ? <span className="chapter-error">{chapter.subtitleError}</span> : null}
                    </div>
                    <span className="chapter-extension">MP4</span>
                  </li>
                ))}
              </ol>
            ) : busy ? (
              <div className="chapter-skeletons" aria-label="Loading downloaded chapters" role="status">
                {[0, 1, 2].map((item) => (
                  <div className="chapter-skeleton" key={item}>
                    <span className="skeleton-index" />
                    <span className="skeleton-lines"><i /><i /></span>
                    <span className="skeleton-status" />
                  </div>
                ))}
              </div>
            ) : error ? (
              <p className="error-message" role="alert">{error}</p>
            ) : null}

            {error && chapters.length > 0 ? <p className="error-message" role="alert">{error}</p> : null}
            {outputDir ? <p className="output-location">Saved in <span>{outputDir}</span></p> : null}
          </section>
        )}
      </main>
    </div>
  )
}

function subtitleLabel(status: string) {
  switch (status) {
    case 'working': return 'Generating subtitles'
    case 'ready': return 'SRT ready'
    case 'failed': return 'Subtitle failed'
    default: return 'Waiting for subtitles'
  }
}

export default App
