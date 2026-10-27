import { type FormEvent, useEffect, useState } from 'react'
import { Clipboard, Events } from '@wailsio/runtime'
import {
  YouTubeService,
  type AppSettings,
  type DownloadedChapter,
  type DownloadUpdate,
  type SettingsInput,
} from '../bindings/aclip'

type SettingsTab = 'general' | 'ai' | 'notifications'

function App() {
  const [url, setUrl] = useState('')
  const [started, setStarted] = useState(false)
  const [busy, setBusy] = useState(false)
  const [phase, setPhase] = useState('')
  const [title, setTitle] = useState('')
  const [message, setMessage] = useState('')
  const [progress, setProgress] = useState<number | null>(null)
  const [outputDir, setOutputDir] = useState('')
  const [chapters, setChapters] = useState<DownloadedChapter[]>([])
  const [error, setError] = useState('')
  const [expandedChapters, setExpandedChapters] = useState<Record<string, boolean>>({})
  const [openMenuPath, setOpenMenuPath] = useState('')
  const [analysisBusyPath, setAnalysisBusyPath] = useState('')
  const [savingCaptionsPath, setSavingCaptionsPath] = useState('')
  const [dirtyCaptions, setDirtyCaptions] = useState<Record<string, boolean>>({})
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settingsTab, setSettingsTab] = useState<SettingsTab>('general')
  const [settings, setSettings] = useState<AppSettings | null>(null)
  const [settingsDraft, setSettingsDraft] = useState<SettingsInput | null>(null)
  const [apiToken, setApiToken] = useState('')
  const [settingsBusy, setSettingsBusy] = useState(false)
  const [settingsMessage, setSettingsMessage] = useState('')
  const [settingsError, setSettingsError] = useState('')

  useEffect(() => {
    let active = true
    YouTubeService.GetSettings().then((saved) => {
      if (active) setSettings(saved)
    }).catch(() => {})
    return () => { active = false }
  }, [])

  useEffect(() => Events.On('download:update', (event) => {
    const update = event.data as DownloadUpdate
    setPhase(update.status)
    if (update.title) setTitle(update.title)
    if (update.message) setMessage(update.message)
    if (typeof update.progress === 'number') setProgress(update.progress)
    if (update.outputDir) setOutputDir(update.outputDir)
    if (update.chapters) {
      if (['analysis', 'subtitles', 'chapter_downloaded'].includes(update.status) && update.chapters.length === 1) {
        const changedChapter = update.chapters[0]
        setChapters((current) => {
          const found = current.some((chapter) => chapter.videoPath === changedChapter.videoPath)
          return found
            ? current.map((chapter) => chapter.videoPath === changedChapter.videoPath ? changedChapter : chapter)
            : [...current, changedChapter].sort((a, b) => a.videoPath.localeCompare(b.videoPath))
        })
      } else {
        setChapters(update.chapters)
      }
    }
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
    setExpandedChapters({})
    setOpenMenuPath('')
    setDirtyCaptions({})
    setError('')

    try {
      const result = await YouTubeService.DownloadYouTube(videoURL)
      setTitle(result.title)
      setOutputDir(result.outputDir)
      setChapters(result.chapters ?? [])
      setMessage('Download and chapter processing complete')
    } catch (reason) {
      setPhase('error')
      setError(reason instanceof Error ? reason.message : String(reason))
      setMessage('The download could not be completed')
    } finally {
      setBusy(false)
    }
  }

  async function openVideoFolder() {
    if (busy) return
    setBusy(true)
    setPhase('opening-folder')
    setError('')
    try {
      const folder = await YouTubeService.OpenVideoFolder()
      if (!folder.folderPath) return
      setStarted(true)
      setTitle(folder.title)
      setOutputDir(folder.folderPath)
      setChapters(folder.chapters ?? [])
      setExpandedChapters({})
      setOpenMenuPath('')
      setDirtyCaptions({})
      setProgress(null)
      setMessage(`Loaded ${folder.chapters?.length ?? 0} chapter(s) from folder`)
    } catch (reason) {
      setError(errorText(reason))
      setMessage('The folder could not be loaded')
    } finally {
      setBusy(false)
    }
  }

  async function generateSubtitles(chapter: DownloadedChapter) {
    if (busy) return
    setBusy(true)
    setPhase('subtitles')
    setOpenMenuPath('')
    setError('')
    setChapters((current) => current.map((item) => item.videoPath === chapter.videoPath
      ? { ...item, subtitleStatus: 'working', subtitleError: '' }
      : item,
    ))
    try {
      const updated = await YouTubeService.GenerateSubtitles(chapter.videoPath)
      setChapters((current) => current.map((item) => item.videoPath === updated.videoPath ? updated : item))
      setMessage(`Subtitles generated for ${chapter.name}`)
    } catch (reason) {
      const failure = errorText(reason)
      setChapters((current) => current.map((item) => item.videoPath === chapter.videoPath
        ? { ...item, subtitleStatus: 'failed', subtitleError: failure }
        : item,
      ))
      setError(failure)
    } finally {
      setBusy(false)
    }
  }

  async function openChapterFolder(chapter: DownloadedChapter) {
    setOpenMenuPath('')
    try {
      await YouTubeService.OpenChapterFolder(chapter.videoPath)
    } catch (reason) {
      setError(errorText(reason))
    }
  }

  async function openChapterSubtitle(chapter: DownloadedChapter) {
    setOpenMenuPath('')
    try {
      await YouTubeService.OpenChapterSubtitle(chapter.videoPath)
    } catch (reason) {
      setError(errorText(reason))
    }
  }

  async function copyVideoPath(chapter: DownloadedChapter) {
    setOpenMenuPath('')
    try {
      await Clipboard.SetText(chapter.videoPath)
      setMessage('Video path copied to clipboard')
    } catch (reason) {
      setError(errorText(reason))
    }
  }

  function updateCaption(videoPath: string, clipIndex: number, caption: string) {
    setChapters((current) => current.map((chapter) => {
      if (chapter.videoPath !== videoPath || !chapter.analysis) return chapter
      const clips = (chapter.analysis.clips ?? []).map((clip, index) => index === clipIndex ? { ...clip, caption } : clip)
      return { ...chapter, analysis: { ...chapter.analysis, clips } }
    }))
    setDirtyCaptions((current) => ({ ...current, [videoPath]: true }))
  }

  async function saveCaptions(chapter: DownloadedChapter) {
    if (!chapter.analysis || savingCaptionsPath) return
    setSavingCaptionsPath(chapter.videoPath)
    try {
      const updated = await YouTubeService.SaveChapterAnalysis(chapter.videoPath, chapter.videoTitle || title, chapter.analysis)
      setChapters((current) => current.map((item) => item.videoPath === updated.videoPath ? updated : item))
      setDirtyCaptions((current) => ({ ...current, [chapter.videoPath]: false }))
      setMessage('Captions saved in clip-suggestions.json')
    } catch (reason) {
      setError(errorText(reason))
    } finally {
      setSavingCaptionsPath('')
    }
  }

  async function openSettings() {
    setSettingsOpen(true)
    setSettingsTab('general')
    setSettingsMessage('')
    setSettingsError('')
    setSettingsBusy(true)
    try {
      const saved = await YouTubeService.GetSettings()
      setSettings(saved)
      setSettingsDraft({
        downloadDirectory: saved.downloadDirectory,
        openRouterModel: saved.openRouterModel,
        notificationsEnabled: saved.notificationsEnabled,
      })
    } catch (reason) {
      setSettingsError(errorText(reason))
    } finally {
      setSettingsBusy(false)
    }
  }

  async function saveSettings() {
    if (!settingsDraft) return
    setSettingsBusy(true)
    setSettingsError('')
    try {
      const saved = await YouTubeService.SaveSettings(settingsDraft)
      setSettings(saved)
      setSettingsDraft({
        downloadDirectory: saved.downloadDirectory,
        openRouterModel: saved.openRouterModel,
        notificationsEnabled: saved.notificationsEnabled,
      })
      setSettingsMessage('Settings saved')
    } catch (reason) {
      setSettingsError(errorText(reason))
    } finally {
      setSettingsBusy(false)
    }
  }

  async function saveApiToken() {
    if (!apiToken.trim()) return
    setSettingsBusy(true)
    setSettingsError('')
    try {
      const saved = await YouTubeService.SaveOpenRouterToken(apiToken)
      setSettings(saved)
      setApiToken('')
      setSettingsMessage('API key saved securely in Windows Credential Manager')
    } catch (reason) {
      setSettingsError(errorText(reason))
    } finally {
      setSettingsBusy(false)
    }
  }

  async function clearApiToken() {
    setSettingsBusy(true)
    setSettingsError('')
    try {
      const saved = await YouTubeService.ClearOpenRouterToken()
      setSettings(saved)
      setSettingsMessage('Saved API key removed')
    } catch (reason) {
      setSettingsError(errorText(reason))
    } finally {
      setSettingsBusy(false)
    }
  }

  async function analyzeChapter(chapter: DownloadedChapter) {
    if (busy || analysisBusyPath) return
    setBusy(true)
    setPhase('analysis')
    setOpenMenuPath('')
    setAnalysisBusyPath(chapter.videoPath)
    setError('')
    setChapters((current) => current.map((item) => item.videoPath === chapter.videoPath
      ? { ...item, analysisStatus: 'working', analysisError: '' }
      : item,
    ))
    setExpandedChapters((current) => ({ ...current, [chapter.videoPath]: true }))
    try {
      const result = await YouTubeService.AnalyzeChapter(chapter.videoPath, chapter.videoTitle || title)
      setChapters((current) => current.map((item) => item.videoPath === result.videoPath ? result : item))
      setDirtyCaptions((current) => ({ ...current, [chapter.videoPath]: !!result.analysisError }))
    } catch (reason) {
      const errorMessage = errorText(reason)
      const status = errorMessage.toLowerCase().includes('openrouter api key') ? 'not_configured' : 'failed'
      setChapters((current) => current.map((item) => item.videoPath === chapter.videoPath
        ? { ...item, analysisStatus: status, analysisError: errorMessage }
        : item,
      ))
    } finally {
      setBusy(false)
      setAnalysisBusyPath('')
    }
  }

  function toggleChapter(path: string) {
    setExpandedChapters((current) => ({ ...current, [path]: !current[path] }))
  }

  const updateDraft = <K extends keyof SettingsInput>(key: K, value: SettingsInput[K]) => {
    setSettingsDraft((current) => current ? { ...current, [key]: value } : current)
  }

  return (
    <>
      <button className="settings-trigger" type="button" onClick={openSettings} aria-label="Open settings" title="Settings">
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 640" fill="currentColor"><path d="M259.1 73.5C262.1 58.7 275.2 48 290.4 48L350.2 48C365.4 48 378.5 58.7 381.5 73.5L396 143.5C410.1 149.5 423.3 157.2 435.3 166.3L503.1 143.8C517.5 139 533.3 145 540.9 158.2L570.8 210C578.4 223.2 575.7 239.8 564.3 249.9L511 297.3C511.9 304.7 512.3 312.3 512.3 320C512.3 327.7 511.8 335.3 511 342.7L564.4 390.2C575.8 400.3 578.4 417 570.9 430.1L541 481.9C533.4 495 517.6 501.1 503.2 496.3L435.4 473.8C423.3 482.9 410.1 490.5 396.1 496.6L381.7 566.5C378.6 581.4 365.5 592 350.4 592L290.6 592C275.4 592 262.3 581.3 259.3 566.5L244.9 496.6C230.8 490.6 217.7 482.9 205.6 473.8L137.5 496.3C123.1 501.1 107.3 495.1 99.7 481.9L69.8 430.1C62.2 416.9 64.9 400.3 76.3 390.2L129.7 342.7C128.8 335.3 128.4 327.7 128.4 320C128.4 312.3 128.9 304.7 129.7 297.3L76.3 249.8C64.9 239.7 62.3 223 69.8 209.9L99.7 158.1C107.3 144.9 123.1 138.9 137.5 143.7L205.3 166.2C217.4 157.1 230.6 149.5 244.6 143.4L259.1 73.5zM320.3 400C364.5 399.8 400.2 363.9 400 319.7C399.8 275.5 363.9 239.8 319.7 240C275.5 240.2 239.8 276.1 240 320.3C240.2 364.5 276.1 400.2 320.3 400z"/></svg>
      </button>

      <div className={`app-shell${started ? ' app-shell--active' : ''}`}>
        <main className="page">
          <form className="entry-form" onSubmit={submitURL}>
            <label className="visually-hidden" htmlFor="youtube-url">YouTube video URL</label>
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
          <button className="open-folder-button" type="button" onClick={openVideoFolder} disabled={busy}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3.5 6.5h6l2 2h9v9.8a1.7 1.7 0 0 1-1.7 1.7H5.2a1.7 1.7 0 0 1-1.7-1.7V6.5Z" /><path d="M3.5 9h17" /></svg>
            <span>Open an existing video folder</span>
          </button>
          {error && !started ? <p className="error-message landing-error" role="alert">{error}</p> : null}

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
                    <li className={`chapter-card${openMenuPath === chapter.videoPath ? ' has-open-menu' : ''}`} key={chapter.videoPath}>
                      <div className="chapter-row">
                        <button
                          className="chapter-trigger"
                          type="button"
                          onClick={() => toggleChapter(chapter.videoPath)}
                          aria-expanded={!!expandedChapters[chapter.videoPath]}
                        >
                          <span className="chapter-index">{String(index + 1).padStart(2, '0')}</span>
                          <span className="chapter-copy">
                            <span className="chapter-name">{chapter.name}</span>
                            <span className="chapter-metadata">
                              {formatDuration(chapter.durationSeconds ?? 0)}
                              <i />
                              {formatFileSize(chapter.sizeBytes)}
                              {chapter.width && chapter.height ? <><i />{chapter.width} × {chapter.height}</> : null}
                              {chapter.videoCodec ? <><i />{chapter.videoCodec}</> : null}
                            </span>
                            <span className="chapter-indicators">
                              <ArtifactIndicator kind="srt" ready={chapter.subtitleStatus === 'ready'} />
                              <ArtifactIndicator kind="ai" ready={chapter.analysisStatus === 'ready'} />
                            </span>
                          </span>
                          <svg className={`chapter-chevron${expandedChapters[chapter.videoPath] ? ' is-open' : ''}`} viewBox="0 0 20 20" aria-hidden="true">
                            <path d="m5 7.5 5 5 5-5" />
                          </svg>
                        </button>
                        <div
                          className="chapter-action-anchor"
                          onBlur={(event) => {
                            if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOpenMenuPath('')
                          }}
                        >
                          <button
                            className="chapter-menu-trigger"
                            type="button"
                            aria-label={`Actions for ${chapter.name}`}
                            aria-expanded={openMenuPath === chapter.videoPath}
                            onClick={() => setOpenMenuPath((current) => current === chapter.videoPath ? '' : chapter.videoPath)}
                          >
                            <svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="10" cy="4" r="1.5" /><circle cx="10" cy="10" r="1.5" /><circle cx="10" cy="16" r="1.5" /></svg>
                          </button>
                          {openMenuPath === chapter.videoPath && (
                            <div className="chapter-menu" role="menu" onClick={(event) => event.stopPropagation()}>
                              {chapter.subtitleStatus !== 'ready' ? (
                                <button type="button" role="menuitem" disabled={busy} onClick={() => generateSubtitles(chapter)}>
                                  {chapter.subtitleStatus === 'working' ? 'Generating subtitles…' : chapter.subtitleStatus === 'failed' ? 'Retry subtitle generation' : 'Generate subtitles'}
                                </button>
                              ) : (
                                <>
                                  <button type="button" role="menuitem" disabled={busy} onClick={() => analyzeChapter(chapter)}>
                                    {chapter.analysisStatus === 'working' ? 'Generating AI suggestions…' : chapter.analysisStatus === 'ready' ? 'Regenerate AI suggestions' : 'Generate AI suggestions'}
                                  </button>
                                  <button type="button" role="menuitem" onClick={() => openChapterSubtitle(chapter)}>Open SRT file</button>
                                </>
                              )}
                              <button type="button" role="menuitem" onClick={() => openChapterFolder(chapter)}>Open containing folder</button>
                              <button type="button" role="menuitem" onClick={() => copyVideoPath(chapter)}>Copy video path</button>
                            </div>
                          )}
                        </div>
                      </div>
                      {expandedChapters[chapter.videoPath] && (
                        <ChapterDetails
                          chapter={chapter}
                          dirty={!!dirtyCaptions[chapter.videoPath]}
                          saving={savingCaptionsPath === chapter.videoPath}
                          onCaptionChange={(index, caption) => updateCaption(chapter.videoPath, index, caption)}
                          onSaveCaptions={() => saveCaptions(chapter)}
                        />
                      )}
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
              ) : error ? <p className="error-message" role="alert">{error}</p> : null}

              {error && chapters.length > 0 ? <p className="error-message" role="alert">{error}</p> : null}
              {outputDir ? <p className="output-location">Saved in <span>{outputDir}</span></p> : null}
            </section>
          )}
        </main>
      </div>

      {settingsOpen && (
        <div className="settings-backdrop" onClick={() => setSettingsOpen(false)}>
          <section
            className="settings-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="settings-title"
            onClick={(event) => event.stopPropagation()}
          >
            <header className="settings-header">
              <div>
                <p className="eyebrow">ACLIP</p>
                <h2 id="settings-title">Settings</h2>
              </div>
              <button className="close-button" type="button" onClick={() => setSettingsOpen(false)} aria-label="Close settings">×</button>
            </header>

            <div className="settings-layout">
              <nav className="settings-tabs" aria-label="Settings sections">
                <SettingsTabButton active={settingsTab === 'general'} onClick={() => setSettingsTab('general')} icon="general">General</SettingsTabButton>
                <SettingsTabButton active={settingsTab === 'ai'} onClick={() => setSettingsTab('ai')} icon="ai">AI</SettingsTabButton>
                <SettingsTabButton active={settingsTab === 'notifications'} onClick={() => setSettingsTab('notifications')} icon="notifications">Notifications</SettingsTabButton>
              </nav>

              <div className="settings-content">
                {settingsBusy && !settingsDraft ? <p className="settings-loading">Loading settings…</p> : null}
                {settingsTab === 'general' && settingsDraft && (
                  <div className="settings-section">
                    <div className="settings-section-heading">
                      <h3>Downloads</h3>
                      <p>Choose where videos, chapters, and SRT files are saved.</p>
                    </div>
                    <label className="settings-label" htmlFor="download-directory">Download folder</label>
                    <input
                      id="download-directory"
                      className="settings-input"
                      value={settingsDraft.downloadDirectory}
                      onChange={(event) => updateDraft('downloadDirectory', event.target.value)}
                      spellCheck={false}
                    />
                    <button className="settings-primary" type="button" onClick={saveSettings} disabled={settingsBusy}>Save general settings</button>
                  </div>
                )}

                {settingsTab === 'ai' && settingsDraft && (
                  <div className="settings-section">
                    <div className="settings-section-heading">
                      <h3>OpenRouter</h3>
                      <p>Analyze generated subtitles for hook-led 30–45 second clip ideas.</p>
                    </div>
                    <label className="settings-label" htmlFor="openrouter-model">Model ID</label>
                    <input
                      id="openrouter-model"
                      className="settings-input"
                      value={settingsDraft.openRouterModel}
                      onChange={(event) => updateDraft('openRouterModel', event.target.value)}
                      placeholder="openai/gpt-4.1-mini"
                      spellCheck={false}
                    />
                    <button className="settings-secondary" type="button" onClick={saveSettings} disabled={settingsBusy}>Save model</button>

                    <div className="settings-divider" />
                    <div className="key-status">
                      <span className={`key-status-dot${settings?.openRouterConfigured ? ' is-configured' : ''}`} />
                      {settings?.openRouterConfigured ? 'API key saved securely' : 'No API key saved'}
                    </div>
                    <label className="settings-label" htmlFor="openrouter-key">OpenRouter API key</label>
                    <input
                      id="openrouter-key"
                      className="settings-input"
                      type="password"
                      value={apiToken}
                      onChange={(event) => setApiToken(event.target.value)}
                      placeholder={settings?.openRouterConfigured ? 'Enter a new key to replace it' : 'Paste your API key'}
                      autoComplete="new-password"
                    />
                    <div className="settings-actions">
                      <button className="settings-primary" type="button" onClick={saveApiToken} disabled={settingsBusy || !apiToken.trim()}>
                        Save API key
                      </button>
                      {settings?.openRouterConfigured ? (
                        <button className="settings-danger" type="button" onClick={clearApiToken} disabled={settingsBusy}>Remove</button>
                      ) : null}
                    </div>
                    <p className="settings-note">The key is stored in Windows Credential Manager and is never sent to the frontend after saving. Subtitle text is sent to OpenRouter for analysis; provider usage charges may apply.</p>
                  </div>
                )}

                {settingsTab === 'notifications' && settingsDraft && (
                  <div className="settings-section">
                    <div className="settings-section-heading">
                      <h3>Background notifications</h3>
                      <p>Show Windows notifications for finished chapters, subtitles, and AI analysis when aclip is not focused.</p>
                    </div>
                    <label className="toggle-row">
                      <span>
                        <strong>Notify when a step completes</strong>
                        <small>Notifications stay quiet while aclip is focused.</small>
                      </span>
                      <input
                        type="checkbox"
                        checked={settingsDraft.notificationsEnabled}
                        onChange={(event) => updateDraft('notificationsEnabled', event.target.checked)}
                      />
                    </label>
                    <button className="settings-primary" type="button" onClick={saveSettings} disabled={settingsBusy}>Save notification settings</button>
                  </div>
                )}

                {settingsMessage ? <p className="settings-success" role="status">{settingsMessage}</p> : null}
                {settingsError ? <p className="settings-error" role="alert">{settingsError}</p> : null}
              </div>
            </div>
          </section>
        </div>
      )}
    </>
  )
}

function ChapterDetails({
  chapter,
  dirty,
  saving,
  onCaptionChange,
  onSaveCaptions,
}: {
  chapter: DownloadedChapter
  dirty: boolean
  saving: boolean
  onCaptionChange: (clipIndex: number, caption: string) => void
  onSaveCaptions: () => void
}) {
  if (chapter.analysisStatus === 'ready' && chapter.analysis) {
    return (
      <div className="chapter-details">
        <div className="analysis-details-header">
          <p className="analysis-summary">{chapter.analysis.summary}</p>
          <button className="caption-save-button" type="button" onClick={onSaveCaptions} disabled={saving || !dirty}>
            {saving ? 'Saving…' : dirty ? 'Save captions' : 'Captions saved'}
          </button>
        </div>
        {chapter.analysisError ? <p className="analysis-save-warning">{chapter.analysisError}</p> : null}
        <div className="clip-grid">
          {(chapter.analysis.clips ?? []).map((clip, index) => (
            <article className="clip-card" key={`${clip.title}-${index}`}>
              <div className="clip-heading">
                <div>
                  <p className="clip-kicker">CLIP {String(index + 1).padStart(2, '0')} · {formatDuration(clip.durationSeconds)}</p>
                  <h3>{clip.title}</h3>
                </div>
                <span className="potential-score" title="Editorial potential, not a view guarantee">{clip.potentialScore}<small>/100</small></span>
              </div>
              <blockquote>{clip.hook}</blockquote>
              <p className="clip-rationale">{clip.whyItCouldWork}</p>
              <div className="clip-segments">
                <h4>Stitch these moments</h4>
                <ol>
                  {(clip.segments ?? []).map((segment, segmentIndex) => (
                    <li key={`${segment.startSeconds}-${segment.endSeconds}`}>
                      <span className="segment-time">{formatDuration(segment.startSeconds)}–{formatDuration(segment.endSeconds)}</span>
                      <span>{segment.editNote || `Segment ${segmentIndex + 1}`}</span>
                    </li>
                  ))}
                </ol>
              </div>
              <label className="clip-caption">
                <strong>Caption · editable</strong>
                <textarea value={clip.caption} onChange={(event) => onCaptionChange(index, event.target.value)} rows={3} />
              </label>
              {clip.hashtags?.length ? <p className="clip-hashtags">{clip.hashtags.map((tag) => <span key={tag}>{tag}</span>)}</p> : null}
            </article>
          ))}
        </div>
      </div>
    )
  }

  if (chapter.analysisStatus === 'not_configured') {
    return (
      <div className="chapter-details chapter-empty">
        <p>Add an OpenRouter API key in Settings, then choose “Generate AI suggestions” from this chapter’s action menu.</p>
      </div>
    )
  }

  if (chapter.analysisStatus === 'failed') {
    return (
      <div className="chapter-details chapter-empty">
        <p className="analysis-error">{chapter.analysisError || 'Clip analysis failed.'}</p>
        <p>Retry AI suggestions from the chapter action menu.</p>
      </div>
    )
  }

  if (chapter.analysisStatus === 'skipped') {
    return (
      <div className="chapter-details chapter-empty">
        <p>Subtitle generation failed, so this chapter was not sent for clip analysis.</p>
      </div>
    )
  }

  if (chapter.analysisStatus === 'missing' && chapter.subtitleStatus === 'ready') {
    return (
      <div className="chapter-details chapter-empty">
        <p>Subtitles are ready. Choose “Generate AI suggestions” from the chapter action menu when you want clip ideas.</p>
      </div>
    )
  }

  if (chapter.analysisStatus === 'missing') {
    return (
      <div className="chapter-details chapter-empty">
        <p>No subtitle file yet. Choose “Generate subtitles” from the chapter action menu.</p>
      </div>
    )
  }

  return (
    <div className="chapter-details chapter-empty">
      <div className="analysis-loader"><span className="button-spinner" />{analysisStatusText(chapter.analysisStatus)}</div>
    </div>
  )
}

function ArtifactIndicator({ kind, ready }: { kind: 'srt' | 'ai'; ready: boolean }) {
  return (
    <span className={`artifact-indicator artifact-indicator--${kind}${ready ? ' is-ready' : ''}`} title={ready ? `${kind.toUpperCase()} generated` : `${kind.toUpperCase()} not generated`}>
      <svg viewBox="0 0 16 16" aria-hidden="true">
        {ready ? <path d="m3.2 8.2 3 3 6.6-6.5" /> : <circle cx="8" cy="8" r="5.2" />}
      </svg>
      {kind.toUpperCase()}
    </span>
  )
}

function SettingsTabButton({
  active,
  onClick,
  icon,
  children,
}: {
  active: boolean
  onClick: () => void
  icon: SettingsTab
  children: string
}) {
  return (
    <button className={`settings-tab${active ? ' is-active' : ''}`} type="button" onClick={onClick} aria-current={active ? 'page' : undefined}>
      <span className={`settings-tab-icon settings-tab-icon--${icon}`} aria-hidden="true" />
      {children}
    </button>
  )
}

function analysisStatusText(status: string) {
  if (status === 'working') return 'Analyzing transcript with OpenRouter…'
  if (status === 'waiting') return 'Clip analysis will begin after subtitles are generated.'
  return 'Preparing chapter analysis…'
}

function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return 'Length unavailable'
  const total = Math.round(seconds)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const remaining = total % 60
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, '0')}:${String(remaining).padStart(2, '0')}`
    : `${minutes}:${String(remaining).padStart(2, '0')}`
}

function formatFileSize(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return 'Size unavailable'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = bytes
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  return `${unitIndex === 0 ? size : size.toFixed(1)} ${units[unitIndex]}`
}

function errorText(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason)
}

export default App
