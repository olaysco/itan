---
name: motion-design
description: Craft rules for compose graphics — typography, easing, layout, and kinetic type that don't look like defaults.
triggers: intro, outro, title card, end screen, explainer, motion design, animated caption, launch video, lower third, graphic
---

# Motion design

Rules for every `compose` composition. Defaults look cheap; these are the
overrides that make graphics look intentional.

## Typography (the #1 taste signal)
- Use the embedded faces, never system-ui: `'Bricolage Grotesque'` for
  display/headlines (variable, weights 200–800), `'IBM Plex Mono'` for
  labels, numbers, code, and captions.
- One modular scale per video, e.g. 96 / 56 / 28 / 18 / 13px at 1280×720.
  Two sizes per scene maximum.
- Display type: weight 700–800, `letter-spacing:-0.02em`, `line-height:1.05`.
  Labels: mono, 11–13px, uppercase, `letter-spacing:0.14em`, muted color.
- Never center-align paragraphs. Headlines may be centered OR left-anchored
  on a grid line — pick one per video and keep it.

## Easing & motion
- Define once, use everywhere:
  `--out: cubic-bezier(0.2, 0.8, 0.2, 1);` (entrances)
  `--spring: cubic-bezier(0.34, 1.56, 0.64, 1);` (emphasis, small elements only)
- Ban `ease` and `linear` for movement. Entrances 500–800ms; emphasis
  200–350ms.
- Stagger siblings by 60–120ms (`animation-delay`). Everything arriving at
  once reads as a slide, not motion design.
- Motion must have a direction that means something: content enters from
  where it "comes from" (replies from the left, user messages from the
  right, counters count up).

## Kinetic type patterns (all seek-safe: CSS animations only)
- Word-stagger: wrap each word in a span, `animation:up .6s var(--out) both`
  with incremental delays.
- Mask reveal: parent `overflow:hidden`, child translates up from 100%.
- Typewriter: mono text in a span with `width` animated via
  `steps(N)` + `overflow:hidden;white-space:nowrap`, caret as a border-right
  with a blink animation.
- Count-up numbers: pre-render the final number, reveal digits with a short
  mask; do not fake randomness.

## Layout & color
- Compose on a 12-col grid with ≥64px margins at 1280×720. Asymmetry beats
  centering: put the subject on a third.
- One idea per scene. If a scene needs three sentences, it is three scenes.
- Dark scenes: never pure #000 for panels — use #131316/#1A1A1E layers with
  1px borders (#2C2C32). One accent color per scene, two per video max.
- Text contrast ≥ 7:1 for body, 4.5:1 for large display.
- Subtle depth: a radial gradient tint or 1px hairline, not drop shadows.

## Scene rhythm
- Hook scene ≤ 3s. No scene over 6s without internal motion.
- End every scene with 400–600ms of rest before the cut (nothing entering).
- Use concat transitions sparingly: `fade` 0.4–0.6s or `fadeblack` for
  chapter breaks; never wipe/slide unless the content literally moves.

## Checklist before rendering
1. Fonts: Bricolage/Plex Mono declared? No system-ui leaking in?
2. Two type sizes max per scene? Labels in tracked-out mono caps?
3. All movement on --out/--spring with stagger?
4. Anything centered that shouldn't be?
5. Rest beat at scene end?
