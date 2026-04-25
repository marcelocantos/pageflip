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

function ensureSlideCard(slideID, imageURL) {
  if (slides.has(slideID)) return slides.get(slideID);
  const card = document.createElement('section');
  card.className = 'slide-card';
  card.dataset.slideId = slideID;

  const imgWrap = document.createElement('a');
  imgWrap.className = 'slide-image';
  imgWrap.target = '_blank';
  if (imageURL) {
    imgWrap.href = imageURL;
    const img = document.createElement('img');
    img.alt = `slide ${slideID}`;
    img.src = imageURL;
    imgWrap.appendChild(img);
    // Floating large-preview that appears on hover. Same src as the
    // thumbnail (browser cache-hits the second decode), shown via
    // CSS `.slide-image:hover .preview`.
    const preview = document.createElement('img');
    preview.className = 'preview';
    preview.alt = '';
    preview.src = imageURL;
    imgWrap.appendChild(preview);
  }

  const analysis = document.createElement('div');
  analysis.className = 'analysis';

  card.appendChild(imgWrap);
  card.appendChild(analysis);
  slidesEl.appendChild(card);

  const entry = { card, analysis, blocks: {} };
  slides.set(slideID, entry);
  return entry;
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

// Auto-scroll: track whether the page was at the bottom (or
// hadn't filled the viewport yet) just before each content arrival.
// When new content lands, only auto-scroll if the user wasn't
// reading scrollback above the fold.
function isPinnedToBottom() {
  const slack = 80; // px tolerance for "near bottom"
  const docH = document.documentElement.scrollHeight;
  const viewBottom = window.innerHeight + window.scrollY;
  return docH <= window.innerHeight + slack || (docH - viewBottom) <= slack;
}
function scrollToBottomIfPinned(wasPinned) {
  if (wasPinned) {
    window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'auto' });
  }
}

evt.addEventListener('slide', (e) => {
  const wasPinned = isPinnedToBottom();
  const d = JSON.parse(e.data);
  ensureSlideCard(d.slide_id, d.image_url || '');
  frames += 1;
  frameCounterEl.textContent = `frames: ${frames}`;
  lastSlideEl.textContent = `last: ${d.slide_id}`;
  lastSlideAt = Date.now();
  ageEl.textContent = `age: 0s`;
  scrollToBottomIfPinned(wasPinned);
});

evt.addEventListener('specialist', (e) => {
  const wasPinned = isPinnedToBottom();
  const d = JSON.parse(e.data);
  const entry = ensureSlideCard(d.slide_id, '');
  const b = ensureBlock(entry, d.role);
  b.content.innerHTML = d.html;
  b.block.classList.add('streaming');
  scrollToBottomIfPinned(wasPinned);
});

evt.addEventListener('turn-done', (e) => {
  const wasPinned = isPinnedToBottom();
  const d = JSON.parse(e.data);
  const entry = slides.get(d.slide_id);
  if (!entry) return;
  const b = ensureBlock(entry, d.role);
  b.content.innerHTML = d.html;
  b.block.classList.remove('streaming');
  scrollToBottomIfPinned(wasPinned);
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

