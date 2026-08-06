/* Frame-indexed composition API — the model Remotion proved out, native.
 *
 * The engine renders by seeking time, and anything that *animates itself*
 * (CSS keyframes, WAAPI, GSAP) has to be paused and dragged to the right
 * moment — which is where drift bugs live. This API inverts that: a scene
 * declares what it looks like AT a given frame, so a render is a pure
 * function of frame number and drift is impossible by construction.
 *
 *   itan.frame(({frame, fps, durationInFrames, progress}) => {
 *     title.style.opacity = interpolate(frame, [0, 15], [0, 1]);
 *   });
 */
(() => {
  const easings = {
    linear: t => t,
    in: t => t * t * t,
    out: t => 1 - Math.pow(1 - t, 3),
    inOut: t => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2),
    back: t => 1 + 2.70158 * Math.pow(t - 1, 3) + 1.70158 * Math.pow(t - 1, 2),
  };

  /* interpolate(frame, [inMin,inMax], [outMin,outMax], {easing, clamp}) */
  window.interpolate = (input, inRange, outRange, opts) => {
    opts = opts || {};
    const [i0, i1] = inRange, [o0, o1] = outRange;
    let t = i1 === i0 ? 1 : (input - i0) / (i1 - i0);
    if (opts.clamp !== false) t = Math.max(0, Math.min(1, t));
    const ease = typeof opts.easing === 'function'
      ? opts.easing
      : (easings[opts.easing] || easings.out);
    return o0 + (o1 - o0) * ease(t);
  };
  window.Easing = easings;

  /* spring({frame, fps, config}) — damped harmonic motion, closed form so it
     is exact at any frame without stepping a simulation. */
  window.spring = (arg) => {
    const { frame = 0, fps = 30, config = {} } = arg || {};
    const damping = config.damping ?? 10;
    const stiffness = config.stiffness ?? 100;
    const mass = config.mass ?? 1;
    const t = Math.max(0, frame) / fps;
    const w0 = Math.sqrt(stiffness / mass);
    const zeta = damping / (2 * Math.sqrt(stiffness * mass));
    if (zeta < 1) {
      const wd = w0 * Math.sqrt(1 - zeta * zeta);
      return 1 - Math.exp(-zeta * w0 * t) *
        (Math.cos(wd * t) + ((zeta * w0) / wd) * Math.sin(wd * t));
    }
    return 1 - Math.exp(-w0 * t) * (1 + w0 * t);
  };

  /* Seq(fromFrame, durationInFrames) — is this element's moment now? */
  window.Seq = (from, dur) => ({
    contains: f => f >= from && (dur === undefined || f < from + dur),
    local: f => f - from,
  });

  const callbacks = [];
  window.itan = {
    frame: fn => { callbacks.push(fn); },
    fps: 30,
    durationInFrames: 0,
  };

  /* Called by the renderer for every captured frame. */
  window.__itanApplyFrame = (frame, fps, durationInFrames) => {
    window.itan.fps = fps;
    window.itan.durationInFrames = durationInFrames;
    const ctx = {
      frame,
      fps,
      durationInFrames,
      progress: durationInFrames ? frame / durationInFrames : 0,
    };
    for (const fn of callbacks) {
      try { fn(ctx); } catch (e) { /* a broken scene must not stall the render */ }
    }
    return callbacks.length;
  };
})();
