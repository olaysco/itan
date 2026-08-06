---
name: motion-design
description: Craft rules for compose graphics — write a per-project style brief, then execute it with discipline.
triggers: intro, outro, title card, end screen, explainer, motion design, animated caption, launch video, lower third, graphic
---

# Motion design

Two layers. The CRAFT rules are universal — never break them. The STYLE is
decided fresh per project — two different users' videos must never look
like the same designer's work.

## The arc of a narrated piece

`storyboard` (the script: intent, say, visual, duration) → `voice_scenes`
(synthesizes each `say` and retimes every scene to the real read — a line you
guessed at 4s may take 6.2s) → per scene `find_media` and/or `compose` →
`assemble` (joins the scenes and lays the narration back on at the offsets
the cut actually produced, over a ducked bed) → `view_strip` to judge it →
`export`. Compose to the durations `voice_scenes` returns, never to the
guesses. Any finished scene is addressable as `scene 3` in any tool.

Not every piece is narrated. A silent title card needs none of this — go
straight to compose.

## Find the picture before inventing one

`find_media` searches Pixabay for footage and stills and adds them as
project assets. A real photograph of the subject beats a rectangle standing
in for it. Write a visual query (subject + setting + treatment), not the
narration line, and LOOK at the result with `view_frames` before building
on it — tags lie. Stock is a ground to compose *over*, not the whole scene:
put type, product UI, and the brand on top of it.

## First: write the style brief

Before composing scene 1, decide — and state in one short paragraph of your
reply — this project's style brief. Then hold it for every scene.

- **Type pairing**: a display face + a support face. 'Bricolage Grotesque'
  (variable 200–800) and 'IBM Plex Mono' are embedded and always safe;
  files in ~/.itan/fonts add more. Vary the *personality* per project even
  with the same faces: weight, case, scale, tightness — editorial, techy,
  warm, and brutalist all live inside one variable font.
- **Palette**: derive it from the material, never from habit — the
  footage's dominant tones (view_frames and look), the product's brand
  color (fetch_page theme_color, or read it off the capture_page
  screenshot), or what the user names. One accent; two max. Dark, light,
  or tinted grounds are all valid — pick what fits the content.
- **Easing personality**: calm (power2/power3 outs, longer entrances) vs
  energetic (back/spring emphasis, tighter staggers). Match the subject,
  not a default.
- **Layout anchor**: centered OR left-anchored on a grid line — one choice,
  kept for the whole video.

## Craft (non-negotiable, any style)

- Compose every scene at the delivery size — 1920x1080 unless the user
  says otherwise — and keep all of a video's scenes the same size. `concat`
  joins on the largest clip's canvas and letterboxes the rest, so a scene
  authored smaller arrives padded, not filled.
- Size type against the canvas, not against a remembered pixel value: at
  1080p, display ≈120–160px, subhead ≈52–64px, label ≈18–22px. Scale those
  proportionally for any other canvas.
- Two type sizes per scene max. Labels: support face, tracked-out caps.
  Display: letter-spacing −0.01 to −0.03em, line-height ≈1.05. Never
  center-align paragraphs.
- Use the project's real assets. A logo, screenshot, or capture_page result
  embeds directly: `<img src="file:///absolute/path/logo.svg">` — local
  files are allowed and render offline. A launch video without the product's
  own mark is a draft, not a deliverable.
- Entrances 500–800ms; emphasis 200–350ms; sibling stagger 60–120ms. Ban
  `ease` and `linear` for movement. Define eases once, reuse everywhere.
- Motion has a direction that means something: content enters from where it
  "comes from"; counters count up.
- One idea per scene — a scene needing three sentences is three scenes.
  Hook ≤3s; no scene over 6s without internal motion; end every scene with
  400–600ms of rest before the cut.
- Contrast ≥7:1 for body, 4.5:1 for large display.
- Concat transitions sparingly: `fade` 0.4–0.6s, `fadeblack` for chapter
  breaks; never wipe/slide unless the content literally moves.

## Depth — flat is a choice, not the default

Every technique below is verified to render in this engine. Reaching for
none of them is what makes a video look like a slide deck.

- **Build in three layers**: a ground (gradient or vignette, never one flat
  fill), the content, and atmosphere in front (glow, blur, grain). Move them
  at different rates and the frame has space in it.
- **Pick a light direction and keep it.** Shadow offset, gradient angle, and
  the inset top highlight all agree, in every scene. Two layered shadows
  read as real: a tight contact shadow plus a wide soft one.
- **CSS 3D** works fully: `perspective:1200px` on the parent,
  `transform-style:preserve-3d`, then `rotateY/rotateX/translateZ` on
  children. Device mockups, receding planes, card flips, cover flow.
- **Atmosphere**: `backdrop-filter:blur()` for glass, a large
  `radial-gradient` under `filter:blur(40px)` for a light source, depth of
  field by blurring what is not the subject, and 3–5% grain (a tiled SVG
  `feTurbulence`) to stop dark gradients banding.
- **Canvas 2D and WebGL** both render, and are deterministic when — and only
  when — you draw from `itan.frame` instead of `requestAnimationFrame`:
  `itan.frame(({frame}) => draw(ctx, frame))`. That is the door to particle
  fields, shader gradients, waveforms, and generative grounds.
- Hairlines still earn their place: 1px borders define an edge, shadows and
  glows define space. Use both — they are not alternatives.
- Depth is not decoration. If a layer does not help the viewer understand
  what is in front of what, delete it.

## Pattern vocabulary (pick what fits the brief — not all of it)

- Word- or character-stagger reveals (CSS spans, or GSAP SplitText).
- Mask reveal: parent `overflow:hidden`, child translates up from 100%.
- Typewriter: mono text, `width` via `steps(N)`, caret as blinking border.
- Count-up numbers: pre-render the final number, reveal digits with a short
  mask — never fake randomness.
- Frame-indexed motion (always available, nothing to import): write what a
  scene looks like AT a frame and the renderer does the rest —
  `itan.frame(({frame, fps}) => { el.style.opacity =
  interpolate(frame, [0, 15], [0, 1], {easing: 'out'}); })`. Also
  `spring({frame, fps, config:{damping, stiffness}})` and
  `Seq(fromFrame, durationInFrames)`. Exact at every frame and immune to
  timing drift; reach for it when motion needs to be precise, or when a
  value has to be computed rather than tweened.
- GSAP (bundled — reference `gsap` and it is injected, with SplitText):
  build ONE `gsap.timeline()` per scene with position labels for
  choreographed sequences, per-character text, motion paths. Deterministic
  under the renderer. No `Math.random()`, no wall-clock.

## Checklist before rendering

1. Style brief stated and followed — would this video look different from
   the last project's?
2. Every scene composed at the delivery size, type sized for that canvas?
3. The project's own logo/screenshots actually used where they belong?
4. Does the frame have depth — ground, content, atmosphere — or is it flat?
5. Fonts declared, no system-ui leaking in?
6. Two type sizes per scene; labels tracked-out caps?
7. All movement on the brief's eases, with stagger?
8. Rest beat at scene end?

## Make it yours

A project can replace this skill entirely: `.itan/skills/motion-design/SKILL.md`
in the project (or `~/.itan/skills/` for every project) — put your brand's
faces, palette, and rules there and they win over these defaults.
