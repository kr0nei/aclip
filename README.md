# aclip

A Wails v3 Windows desktop app for downloading YouTube videos, splitting them by chapter, and generating SRT subtitles with AutoSubs.

## Requirements

- Wails v3 CLI
- Node.js and npm
- `yt-dlp.exe` and `autosubs.exe` available on `PATH`

## Run in development

```powershell
wails3 dev
```

Downloads are saved under `%USERPROFILE%\Downloads\aclip\`.

## Build for Windows

```powershell
wails3 build
```
