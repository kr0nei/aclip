# aclip

A Wails v3 Windows desktop app for downloading YouTube videos, splitting them by chapter, generating SRT subtitles with AutoSubs, and finding short-form clip candidates through OpenRouter.

## Requirements

- Wails v3 CLI
- Node.js and npm
- `yt-dlp.exe` and `autosubs.exe` available on `PATH`
- `ffprobe.exe` on `PATH` for chapter duration, resolution, and codec metadata

## Run in development

```powershell
wails3 dev
```

Downloads are saved under `%USERPROFILE%\Downloads\aclip\` by default. Use each chapter's action menu to generate its SRT, then explicitly request AI clip suggestions. Edited captions and suggestions are stored together in `clip-suggestions.json` beside the chapter videos. Use **Open an existing video folder** to reload that folder later.

Set an OpenRouter API key in Settings before requesting AI suggestions. The key is stored in Windows Credential Manager; subtitle text is sent to the selected OpenRouter model and may incur usage charges.

## Build for Windows

```powershell
wails3 build
```
