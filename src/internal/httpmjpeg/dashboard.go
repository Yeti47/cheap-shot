package httpmjpeg

import (
	"encoding/json"
	"html/template"
	"net/http"
	"sort"

	"github.com/Yeti47/cheap-shot/src/internal/frames"
)

// dashboardHandler serves a self-contained live-monitoring page: one tile per
// camera with its MJPEG feed, plus a status badge (connected / fps / last-frame
// age / error) that polls /healthz, and MJPEG auto-reconnect on stream drop.
// No external resources -- it works on an isolated LAN with no internet.
func dashboardHandler(hubs map[string]*frames.Hub) http.HandlerFunc {
	names := make([]string, 0, len(hubs))
	for name := range hubs {
		names = append(names, name)
	}
	sort.Strings(names)
	namesJSON, _ := json.Marshal(names)

	page := struct {
		Names     []string
		NamesJSON template.JS
	}{names, template.JS(namesJSON)}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := dashboardTmpl.Execute(w, page); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

var dashboardTmpl = template.Must(template.New("dashboard").Parse(dashboardHTML))

const dashboardHTML = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>cheap-shot</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; font: 14px/1.4 system-ui, sans-serif; background: #0f1115; color: #e6e8ec; }
  header { display: flex; align-items: center; gap: .6rem; padding: .8rem 1rem;
           border-bottom: 1px solid #23262d; position: sticky; top: 0; background: #0f1115; }
  header h1 { font-size: 1rem; margin: 0; font-weight: 600; letter-spacing: .02em; }
  header .sub { color: #8b909a; font-size: .8rem; }
  #overall { margin-left: auto; font-size: .8rem; color: #8b909a; }
  .grid { display: grid; gap: 1rem; padding: 1rem;
          grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); }
  .tile { background: #171a20; border: 1px solid #23262d; border-radius: 10px; overflow: hidden; }
  .feed { position: relative; aspect-ratio: 4 / 3; background: #000;
          display: flex; align-items: center; justify-content: center; }
  .feed img { width: 100%; height: 100%; object-fit: contain; display: block; }
  .feed .placeholder { position: absolute; color: #5a606b; font-size: .85rem; }
  .bar { display: flex; align-items: center; gap: .5rem; padding: .55rem .7rem; }
  .name { font-weight: 600; }
  .dot { width: 9px; height: 9px; border-radius: 50%; background: #5a606b; flex: none;
         box-shadow: 0 0 0 3px rgba(255,255,255,.04); }
  .dot.ok   { background: #3fb950; }
  .dot.warn { background: #d29922; }
  .dot.bad  { background: #f85149; }
  .stats { margin-left: auto; color: #8b909a; font-size: .8rem; font-variant-numeric: tabular-nums; }
  .stats a { color: #58a6ff; text-decoration: none; }
  .err { padding: 0 .7rem .55rem; color: #f85149; font-size: .78rem; word-break: break-word; }
  .err:empty { display: none; }
</style></head>
<body>
<header>
  <h1>cheap-shot</h1>
  <span class="sub">live camera monitor</span>
  <span id="overall">connecting…</span>
</header>
<div class="grid">
  {{range .Names}}
  <div class="tile" id="tile-{{.}}">
    <div class="feed">
      <span class="placeholder">waiting for stream…</span>
      <img class="stream" data-cam="{{.}}" alt="{{.}}"
           src="/{{.}}/stream.mjpeg" onload="this.previousElementSibling.style.display='none'">
    </div>
    <div class="bar">
      <span class="dot" id="dot-{{.}}"></span>
      <span class="name">{{.}}</span>
      <span class="stats"><span id="stats-{{.}}">—</span> · <a href="/{{.}}/snapshot.jpg" target="_blank">snap</a></span>
    </div>
    <div class="err" id="err-{{.}}"></div>
  </div>
  {{end}}
</div>
<script>
const CAMS = {{.NamesJSON}};

function ago(ts) {
  if (!ts) return "no frames";
  const d = (Date.now() - new Date(ts).getTime()) / 1000;
  if (d < 0) return "just now";
  if (d < 60) return d.toFixed(0) + "s ago";
  if (d < 3600) return (d/60).toFixed(0) + "m ago";
  return (d/3600).toFixed(1) + "h ago";
}

function apply(name, s) {
  const dot = document.getElementById("dot-" + name);
  const stats = document.getElementById("stats-" + name);
  const err = document.getElementById("err-" + name);
  if (!s) { dot.className = "dot bad"; stats.textContent = "unknown"; err.textContent = ""; return; }
  const live = s.connected && s.has_frame;
  dot.className = "dot " + (live ? "ok" : s.connected ? "warn" : "bad");
  const fps = s.fps ? s.fps.toFixed(1) + " fps" : (s.connected ? "no frames yet" : "offline");
  stats.textContent = fps + " · " + ago(s.last_frame_at);
  err.textContent = s.error || "";
}

async function poll() {
  const overall = document.getElementById("overall");
  try {
    const r = await fetch("/healthz", { cache: "no-store" });
    const data = await r.json();
    let up = 0;
    for (const name of CAMS) { apply(name, data[name]); if (data[name] && data[name].connected && data[name].has_frame) up++; }
    overall.textContent = up + "/" + CAMS.length + " streaming";
    overall.style.color = up === CAMS.length ? "#3fb950" : up ? "#d29922" : "#f85149";
  } catch (e) {
    for (const name of CAMS) apply(name, null);
    overall.textContent = "bridge unreachable";
    overall.style.color = "#f85149";
  }
}

// MJPEG auto-reconnect: if a stream drops, re-request it with a cache-buster.
for (const img of document.querySelectorAll("img.stream")) {
  img.addEventListener("error", () => {
    const ph = img.previousElementSibling;
    if (ph) { ph.textContent = "reconnecting…"; ph.style.display = ""; }
    setTimeout(() => { img.src = "/" + img.dataset.cam + "/stream.mjpeg?t=" + Date.now(); }, 2000);
  });
}

poll();
setInterval(poll, 2000);
</script>
</body></html>`
