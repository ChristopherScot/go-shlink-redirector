package main

// The HTML this service serves: the create form shown when a slug has no
// short URL yet, and the root page. Kept out of server.go so the routing
// is readable without scrolling past a stylesheet.

const createTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Create go/{{.Slug}}</title>
<style>
:root {
  --bg: #ffffff;
  --fg: #1c1917;
  --muted: #78716c;
  --border: #d6d3d1;
  --accent: #c2410c;
  --accent-hover: #9a3412;
  --err: #dc2626;
  --code-bg: rgba(194,65,12,.10);
  color-scheme: light dark;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #1c1410;
    --fg: #f5f5f4;
    --muted: #a8a29e;
    --border: #44403c;
    --accent: #c2410c;
    --accent-hover: #9a3412;
    --err: #f87171;
    --code-bg: rgba(194,65,12,.18);
  }
}
* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; background: var(--bg); color: var(--fg); }
body {
  font: 16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 2rem 1rem;
}
main { width: 100%; max-width: 40rem; }
h1 { font-size: 1.6rem; margin: 0 0 .35rem; font-weight: 700; }
p.hint { color: var(--muted); margin: 0 0 2rem; font-size: 1rem; }
form {
  display: flex;
  gap: .75rem;
  align-items: stretch;
  flex-wrap: wrap;
}
input[type=text] {
  flex: 1 1 20rem;
  min-width: 0;
  padding: 1rem 1.15rem;
  font: inherit;
  font-size: 1.15rem;
  border-radius: .6rem;
  border: 1.5px solid var(--border);
  background: transparent;
  color: inherit;
  transition: border-color .15s;
}
input[type=text]:focus {
  outline: none;
  border-color: var(--accent);
}
button {
  padding: 1rem 1.75rem;
  font: inherit;
  font-size: 1.05rem;
  font-weight: 600;
  border: none;
  border-radius: .6rem;
  background: var(--accent);
  color: white;
  cursor: pointer;
  transition: background .15s;
  white-space: nowrap;
}
button:hover { background: var(--accent-hover); }
.err { color: var(--err); margin: 0 0 1rem; font-weight: 500; }
code {
  background: var(--code-bg);
  padding: .15rem .4rem;
  border-radius: .3rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: .95em;
}
</style>
</head>
<body>
<main>
  <h1>go/{{.Slug}} doesn't exist yet</h1>
  <p class="hint">Paste or type a URL to point <code>go/{{.Slug}}</code> at, then press Enter.</p>
  {{if .Error}}<p class="err">{{.Error}}</p>{{end}}
  <form method="post" action="/{{.Slug}}" id="f">
    <input id="u" name="longUrl" type="text" inputmode="url" placeholder="example.com/…" required autofocus autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false">
    <button type="submit">Create</button>
  </form>
</main>
<script>
(function () {
  var input = document.getElementById('u');
  var form = document.getElementById('f');
  // Keep the input focused so a plain paste (Cmd/Ctrl+V) lands in it
  // even if the user clicked away.
  function refocus() { if (document.activeElement !== input) input.focus(); }
  refocus();
  window.addEventListener('focus', refocus);
  document.addEventListener('click', function (e) {
    if (e.target === input || e.target.closest('button')) return;
    refocus();
  });
  // If a paste happens anywhere on the page, land it in the input and
  // submit if it parses as a URL.
  document.addEventListener('paste', function (e) {
    if (document.activeElement === input) return; // let native paste do it
    var text = (e.clipboardData || window.clipboardData).getData('text');
    if (!text) return;
    e.preventDefault();
    input.value = text.trim();
    input.focus();
    // If it looks URL-ish, submit immediately.
    if (/^https?:\/\/\S+|^\S+\.\S+/.test(input.value)) form.requestSubmit();
  });
})();
</script>
</body>
</html>
`

const rootPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>go</title></head>
<body style="font: 16px/1.5 -apple-system, sans-serif; max-width: 30rem; margin: 4rem auto; padding: 0 1rem;">
<h1 style="font-size: 1.4rem;">go/</h1>
<p>Type <code>go/&lt;slug&gt;</code>. Existing slugs redirect. Unknown ones let you create.</p>
</body>
</html>
`
