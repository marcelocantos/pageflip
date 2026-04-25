// meetcat web frontend.
//
// Connects to /events (SSE), receives meeting events, and updates
// the DOM. The server renders all markdown to HTML before pushing,
// so this script is just an event-routed DOM updater — no markdown
// parsing here.
//
// State is keyed by slide_id; each (slide, role) pair owns one
// .specialist-block whose .content innerHTML is replaced on every
// chunk and on turn-done. New slides append to #slides; each one
// gets its own card with the slide PNG embedded inline.

const ROLE_EMOJI = {
  skeptic: '🔍',
  constructive: '💡',
  neutral: '😶',
  dejargoniser: '📖',
  contradictions: '🛑',
};

const slidesEl = document.getElementById('slides');
const systemLogEl = document.getElementById('system-log');
const frameCounterEl = document.getElementById('frame-counter');
const lastSlideEl = document.getElementById('last-slide');
const ageEl = document.getElementById('age');
const meetingIdEl = document.getElementById('meeting-id');

let frames = 0;
let lastSlideAt = null;

// Set the meeting label.
fetch('/meeting').then(r => r.json()).then(j => {
  if (j.meeting_id) meetingIdEl.textContent = j.meeting_id;
});

// Tick once per second to refresh the age field. Whole-second
// resolution mirrors the previous TUI behaviour.
setInterval(() => {
  if (lastSlideAt == null) return;
  const secs = Math.floor((Date.now() - lastSlideAt) / 1000);
  ageEl.textContent = `age: ${secs}s`;
}, 1000);

// Slide state: map<slide_id, { card, blocks: { role: { block, content } } }>
const slides = new Map();

function ensureSlideCard(slideID, header) {
  if (slides.has(slideID)) return slides.get(slideID);
  const card = document.createElement('section');
  card.className = 'slide-card';
  card.dataset.slideId = slideID;

  const imgWrap = document.createElement('div');
  imgWrap.className = 'slide-image';
  const img = document.createElement('img');
  img.alt = `slide ${slideID}`;
  img.src = `/slides/${encodeSlidePath(header)}`;
  imgWrap.appendChild(img);

  const analysis = document.createElement('div');
  analysis.className = 'analysis';

  const meta = document.createElement('div');
  meta.className = 'slide-meta';
  meta.innerHTML = formatMeta(header, slideID);
  analysis.appendChild(meta);

  card.appendChild(imgWrap);
  card.appendChild(analysis);
  slidesEl.appendChild(card);

  const entry = { card, analysis, blocks: {} };
  slides.set(slideID, entry);
  return entry;
}

// The header line emitted by the server (slideSectionHeader in
// colors.go) carries the absolute slide PNG path. Pull it out and
// turn it into a /slides/... URL relative to work_dir. We rely on
// the path landing inside work_dir so the server's pathInside guard
// passes; if it doesn't, the image just won't load and the rest of
// the analysis still renders.
function encodeSlidePath(header) {
  const m = header.match(/\s(\/[^\s]+\.png)/);
  if (!m) return '';
  // The server's /slides/ handler joins the request path under
  // work_dir.Abs. We strip the leading '/' so the join treats it
  // as a child path; if work_dir was set to "." (the default),
  // the path will resolve correctly only if the cwd contains the
  // pageflip-<ts> dir. The work_dir/slide_path mismatch is a
  // known limitation — for v1 we just URL-encode and let the
  // server reject if it escapes.
  return encodeURIComponent(m[1]);
}

function formatMeta(header, slideID) {
  // Header is the unformatted text plus ANSI escapes from the
  // section line. Strip ANSI for display and bold the slide_id.
  const plain = stripAnsi(header);
  const escaped = htmlEscape(plain).replace(
    new RegExp(`\\b${escapeRegex(slideID)}\\b`),
    `<span class="slide-id">${slideID}</span>`,
  );
  return escaped;
}

function ensureBlock(entry, role) {
  if (entry.blocks[role]) return entry.blocks[role];
  const block = document.createElement('div');
  block.className = `specialist-block ${role} streaming`;
  const emoji = document.createElement('div');
  emoji.className = 'role-emoji';
  emoji.textContent = ROLE_EMOJI[role] || '·';
  emoji.title = role;
  const content = document.createElement('div');
  content.className = 'content';
  block.appendChild(emoji);
  block.appendChild(content);
  entry.analysis.appendChild(block);
  entry.blocks[role] = { block, content };
  return entry.blocks[role];
}

const evt = new EventSource('/events');

evt.addEventListener('slide', (e) => {
  const d = JSON.parse(e.data);
  ensureSlideCard(d.slide_id, d.header);
  frames += 1;
  frameCounterEl.textContent = `frames: ${frames}`;
  lastSlideEl.textContent = `last: ${d.slide_id}`;
  lastSlideAt = Date.now();
  ageEl.textContent = `age: 0s`;
});

evt.addEventListener('specialist', (e) => {
  const d = JSON.parse(e.data);
  const entry = ensureSlideCard(d.slide_id, '');
  const b = ensureBlock(entry, d.role);
  b.content.innerHTML = d.html;
  b.block.classList.add('streaming');
});

evt.addEventListener('turn-done', (e) => {
  const d = JSON.parse(e.data);
  const entry = slides.get(d.slide_id);
  if (!entry) return;
  const b = ensureBlock(entry, d.role);
  b.content.innerHTML = d.html;
  b.block.classList.remove('streaming');
});

evt.addEventListener('state', (e) => {
  const d = JSON.parse(e.data);
  const el = document.querySelector(`.specialist[data-role="${d.role}"]`);
  if (!el) return;
  el.classList.remove('booting', 'ready', 'stopped');
  el.classList.add(d.state);
  el.title = `${d.role} — ${d.state}`;
  if (d.state === 'stopped' && typeof d.turns === 'number') {
    let turns = el.querySelector('.turns');
    if (!turns) {
      turns = document.createElement('span');
      turns.className = 'turns';
      el.appendChild(turns);
    }
    turns.textContent = `${d.turns}`;
  }
});

evt.addEventListener('system', (e) => {
  const d = JSON.parse(e.data);
  const line = document.createElement('div');
  line.className = 'line';
  line.textContent = d.text || '';
  if (d.role) line.classList.add(d.role);
  systemLogEl.appendChild(line);
  systemLogEl.scrollTop = systemLogEl.scrollHeight;
});

evt.onerror = () => {
  // Browser will auto-reconnect; nothing to do here. If meetcat
  // exited, the user's "Disconnected" cue is the missing tick on
  // the age field — the value will keep growing.
};

// --- helpers -------------------------------------------------------

function stripAnsi(s) {
  return s.replace(/\x1b\[[0-9;]*[a-zA-Z]/g, '').replace(/\x1b\][^\x07]*\x07/g, '');
}
function htmlEscape(s) {
  return s.replace(/[&<>]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
}
function escapeRegex(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
