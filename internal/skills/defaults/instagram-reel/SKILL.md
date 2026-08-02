---
name: instagram-reel
description: Format and pace a video as an Instagram Reel (9:16, ≤90s, hook-first, captions).
triggers: instagram, insta, reel, reels, ig
---

# Instagram Reel

Turn the working video into a strong Instagram Reel.

## Target format
- Aspect 9:16 (1080×1920 class). If the source is landscape, prefer `crop`
  with aspect `9:16` when the subject is centered; otherwise `expand_frame`
  with aspect `9:16` (blur fill) to avoid cutting the subject.
- Duration ≤ 90s; ideal 15–45s. `trim` to the strongest continuous segment.
- Keep fps ≥ 24.

## Editing playbook
1. `probe` the source if metadata is unknown.
2. Hook first: the first 1.5s must show motion or the subject — trim any
   slow intro.
3. Convert to 9:16 (crop or expand as above).
4. If there is speech, `transcribe` and burn 2–5 word caption chunks with
   `overlay_text` (position=center, size≈54) synced to what is said — Reels
   are mostly watched muted.
5. If the user asks for music, `mix_audio` at volume ≤ 0.3 under speech.
6. `export` when the user confirms.

## Style notes
- Captions: white text, bold look (the default border), center or lower third.
- Prefer punchy cuts over slow fades. Avoid letterboxing bars — always fill.
