---
name: tiktok
description: Format and pace a video for TikTok (9:16, fast hook, big captions, trend-friendly pacing).
triggers: tiktok, tik tok, fyp
---

# TikTok

Turn the working video into a TikTok-native clip.

## Target format
- Aspect 9:16. Landscape sources: `crop` to 9:16 if the subject stays in the
  center third, else `expand_frame` 9:16 with blur fill.
- Duration: 7–60s. Sub-15s clips loop best — if the content allows a clean
  loop, trim so the last frame flows into the first.

## Editing playbook
1. `probe` unknown sources first.
2. Brutal hook: cut everything before the first interesting frame; the video
   must earn attention in under 1 second.
3. Convert to 9:16.
4. Speech → `transcribe`, then `overlay_text` captions in short bursts
   (2–4 words, size≈58, position=center). Keep them on-beat with the audio.
5. Pacing: if the clip drags, `set_speed` 1.1–1.3 reads as energy, not chipmunk.
6. `export` when the user confirms.

## Style notes
- Text high-contrast, centered; never cover faces (use top position then).
- No black bars, ever. Fill the frame.
- Trend sounds are user-supplied files → `mix_audio` volume 0.3–0.5.
