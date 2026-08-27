package canvas

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const depthProbe = `<!DOCTYPE html><html><head><style>
  body{margin:0;width:1920px;height:1080px;background:#08080a;overflow:hidden;
       display:grid;grid-template-columns:1fr 1fr;grid-template-rows:1fr 1fr}
  .cell{position:relative;display:flex;align-items:center;justify-content:center}
  .tag{position:absolute;top:18px;left:24px;color:#6a6a76;
       font:500 20px 'IBM Plex Mono',monospace}

  /* 1: CSS 3D — perspective, rotateY, preserve-3d */
  .stage{perspective:1200px}
  .card{width:340px;height:220px;border-radius:20px;transform-style:preserve-3d;
        background:linear-gradient(135deg,#E86C1A,#8a2f0a);
        box-shadow:0 40px 80px rgba(232,108,26,.35), 0 8px 24px rgba(0,0,0,.6);
        transform:rotateY(-32deg) rotateX(10deg)}

  /* 2: depth without 3D — gradient, shadow, blur, grain */
  .glass{width:340px;height:220px;border-radius:20px;
         background:rgba(255,255,255,.06);backdrop-filter:blur(18px);
         border:1px solid rgba(255,255,255,.14);
         box-shadow:0 30px 60px rgba(0,0,0,.55), inset 0 1px 0 rgba(255,255,255,.25)}
  .glow{position:absolute;width:420px;height:420px;border-radius:50%;
        background:radial-gradient(circle,rgba(232,108,26,.55),transparent 65%);
        filter:blur(40px)}

  canvas{border-radius:12px}
</style></head><body>
  <div class="cell stage"><span class="tag">css 3d</span><div class="card"></div></div>
  <div class="cell"><span class="tag">glass / glow / shadow</span><div class="glow"></div><div class="glass"></div></div>
  <div class="cell"><span class="tag">canvas 2d @ frame</span><canvas id="c2" width="420" height="280"></canvas></div>
  <div class="cell"><span class="tag" id="gltag">webgl</span><canvas id="c3" width="420" height="280"></canvas></div>
<script>
  // Frame-indexed canvas: a draw call per frame, no rAF, so it seeks.
  const c2 = document.getElementById('c2').getContext('2d');
  itan.frame(({frame}) => {
    c2.clearRect(0,0,420,280);
    const g = c2.createLinearGradient(0,0,420,280);
    g.addColorStop(0,'#E86C1A'); g.addColorStop(1,'#2a1206');
    c2.fillStyle = g; c2.fillRect(0,0,420,280);
    c2.strokeStyle = 'rgba(255,255,255,.85)'; c2.lineWidth = 4;
    c2.beginPath();
    for (let x = 0; x <= 420; x += 6) {
      const y = 140 + Math.sin((x / 420) * 6.28 + frame * 0.2) * interpolate(frame, [0, 20], [0, 90]);
      x ? c2.lineTo(x, y) : c2.moveTo(x, y);
    }
    c2.stroke();
  });

  // WebGL: does headless actually have a rasterizer here?
  const gl = document.getElementById('c3').getContext('webgl');
  const tag = document.getElementById('gltag');
  if (!gl) { tag.textContent = 'webgl: UNAVAILABLE'; }
  else {
    tag.textContent = 'webgl: ' + gl.getParameter(gl.VERSION);
    const vs = gl.createShader(gl.VERTEX_SHADER);
    gl.shaderSource(vs, 'attribute vec2 p; varying vec2 v; uniform float t;' +
      'void main(){v=p; float c=cos(t),s=sin(t); gl_Position=vec4(mat2(c,-s,s,c)*p,0.,1.);}');
    gl.compileShader(vs);
    const fs = gl.createShader(gl.FRAGMENT_SHADER);
    gl.shaderSource(fs, 'precision mediump float; varying vec2 v;' +
      'void main(){gl_FragColor=vec4(0.91,0.42,0.10,1.0)*(0.4+0.6*v.y);}');
    gl.compileShader(fs);
    const pr = gl.createProgram();
    gl.attachShader(pr, vs); gl.attachShader(pr, fs); gl.linkProgram(pr); gl.useProgram(pr);
    const buf = gl.createBuffer();
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([0,.8,-.8,-.6,.8,-.6]), gl.STATIC_DRAW);
    const loc = gl.getAttribLocation(pr, 'p');
    gl.enableVertexAttribArray(loc); gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
    const tl = gl.getUniformLocation(pr, 't');
    itan.frame(({frame}) => {
      gl.clearColor(0.06,0.06,0.07,1); gl.clear(gl.COLOR_BUFFER_BIT);
      gl.uniform1f(tl, frame * 0.05);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
    });
  }
</script></body></html>`

// TestDepthCapabilities renders the techniques that give a composition
// depth and proves each one reaches the frame. It exists because "why is
// everything flat" deserved an answer from a rendered frame rather than an
// opinion: the engine was never the constraint, so this guards that the
// techniques the guidance now recommends actually work.
func TestDepthCapabilities(t *testing.T) {
	chromeOrSkip(t)
	dir := t.TempDir()
	if keep := os.Getenv("ITAN_KEEP"); keep != "" {
		dir = keep
	}
	out := filepath.Join(dir, "depth.mp4")
	if err := Render(context.Background(), Opts{
		HTML: depthProbe, Width: 1920, Height: 1080, FPS: 24, Duration: 0.6, OutPath: out,
	}); err != nil {
		t.Fatalf("render: %v", err)
	}

	frame := filepath.Join(dir, "depth.png")
	if raw, err := exec.Command("ffmpeg", "-y", "-ss", "0.5", "-i", out,
		"-frames:v", "1", frame).CombinedOutput(); err != nil {
		t.Fatalf("extract: %v\n%s", err, raw)
	}

	// Each quadrant holds one technique. An empty quadrant means that
	// technique silently produced nothing — the failure mode worth catching,
	// since a flat black square looks like a design choice.
	quads := []struct {
		name       string
		x0, y0     int
		x1, y1     int
		minContent float64
	}{
		{"css 3d + layered shadow", 0, 0, 960, 540, 1.0},
		{"glass / glow / blur", 960, 0, 1920, 540, 0.5},
		{"canvas 2d at frame", 0, 540, 960, 1080, 1.0},
		{"webgl shader at frame", 960, 540, 1920, 1080, 1.0},
	}
	img := decodePNG(t, frame)
	for _, q := range quads {
		var lit float64
		var n int
		for y := q.y0; y < q.y1; y++ {
			for x := q.x0; x < q.x1; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				// The ground is #08080a; anything meaningfully above it is drawn.
				if int(r>>8)+int(g>>8)+int(b>>8) > 60 {
					lit++
				}
				n++
			}
		}
		pct := 100 * lit / float64(n)
		if pct < q.minContent {
			t.Errorf("%s: only %.2f%% of its quadrant is drawn — the technique produced nothing", q.name, pct)
		} else {
			t.Logf("%-24s %.1f%% of quadrant drawn", q.name, pct)
		}
	}
}

// A canvas driven by itan.frame must redraw per frame — the thing a rAF
// canvas loop cannot do under a seeking renderer. Without the frame API,
// canvas and WebGL are unusable here, which is a large part of why every
// composition ended up as flat DOM.
func TestCanvasRedrawsPerFrame(t *testing.T) {
	chromeOrSkip(t)
	// Sample the wave's height at a fixed x; it must move between frames and
	// must be the same value whenever that frame is revisited.
	// The stroke is near-white over an orange gradient, so the bluest pixel
	// in the column is the wave — no threshold to tune.
	const readWave = `(() => {
	  const c = document.getElementById('c2').getContext('2d');
	  const d = c.getImageData(210, 0, 1, 280).data;
	  let best = -1, bestB = -1;
	  for (let y = 0; y < 280; y++) {
	    const b = d[y * 4 + 2];
	    if (b > bestB) { bestB = b; best = y; }
	  }
	  return best;
	})()`
	got := probe(t, depthProbe, 24, 15, []int{0, 6, 12, 6, 0}, readWave)
	if got[0] == got[1] || got[1] == got[2] {
		t.Errorf("canvas did not redraw between frames: %v", got)
	}
	if got[1] != got[3] || got[0] != got[4] {
		t.Errorf("canvas draw depends on seek history, not frame number: %v", got)
	}
	t.Logf("canvas wave sampled at frames 0,6,12,6,0: %v", got)
}
