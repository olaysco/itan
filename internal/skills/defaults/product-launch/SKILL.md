---
name: product-launch
description: Turn a product URL into a launch video — fetch the real copy, capture the real UI, compose the story.
triggers: product launch, launch video, landing page, promo video, from this url, product video
tools: fetch_page, capture_page
---

# Product launch video

Turn a URL (or a described product) into a 20–30s launch video. Follow the
motion-design skill for craft; this skill is the pipeline and the honesty
rules.

## Pipeline
1. `fetch_page` the URL. Pull the product name, tagline, and 2–3 concrete
   features from `title` / `description` / `text`. If `theme_color` exists,
   consider it for the accent.
2. `capture_page` the hero (viewport, scale 3 if it will be shown large) —
   and `full_page` if the page is rich. The result's `embed_as` snippet
   drops straight into compose scenes.
3. `storyboard` the arc BEFORE composing — one scene each, durations in
   parentheses (mark_rendered as each lands):
   - Hook (2.5–3.5s): product name + tagline, kinetic type.
   - Tension (3–5s): the problem, in the page's own words.
   - Product beat (4–6s): the captured screenshot, animated — Ken Burns via
     CSS transform (`scale(1.06) translate(-2%, -1%)` over the scene, --out
     easing), inside a browser-chrome frame or clean rounded panel.
   - Features (4–6s): 2–3 numbered rows, verbatim or tightly paraphrased
     from the page.
   - CTA close (2.5–3.5s): name + the site's own call to action + URL in mono.
4. Per scene: compose → view_frames → revise until it matches the intent.
5. `concat` with `transition: fade` (0.4–0.6s).
6. If the user has a music file (or asks for sound): `add_music` — a bed at
   ~20% with fades, auto-ducked under any voiceover. Silent launch videos
   read as demos.
7. `view_strip` the assembly — judge pacing and flow as a whole, fix what
   reads wrong — then `export`.

## Honesty rules
- Every claim must come from the fetched page. Quote or tightly paraphrase;
  NEVER invent features, numbers, or testimonials.
- If the fetch fails or the page is thin, say so and ask for a one-line
  product description instead of guessing.
- Screenshots are the product's real face — never mock up fake UI.

## Screenshot treatment
- Frame captures in a panel: `border-radius:12px; border:1px solid
  var(--line); overflow:hidden` — never bleed a raw screenshot to the edges.
- Slow transforms only (≤6% scale over a scene); movement should feel like
  attention, not a slideshow.
- If the capture is full-page, pan vertically: animate `translateY` inside
  an `overflow:hidden` viewport-height frame.
