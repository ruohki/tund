// Uploads on folder pages of shares that allow them: files from the picker or
// dropped on the page go into a queue, one PUT each, two at a time, with
// progress, speed and time left. Once the queue is done the rows are
// reloaded once (list.js). Names starting with a dot are skipped and
// dropped folders refused, like the server would.

const KINDS = {};
for (const [k, v] of Object.entries({
	image: 'png jpg jpeg gif webp avif heic heif bmp tif tiff svg ico psd cr2 nef arw dng',
	video: 'mp4 m4v mov mkv webm avi wmv flv mpg mpeg 3gp',
	audio: 'mp3 wav flac aac ogg oga opus m4a aif aiff wma mid midi',
	pdf: 'pdf',
	doc: 'doc docx odt rtf pages txt md markdown rst tex epub log',
	sheet: 'xls xlsx xlsm ods csv tsv numbers',
	slides: 'ppt pptx odp key',
	archive: 'zip tar gz tgz bz2 tbz2 xz txz zst 7z rar dmg iso pkg deb rpm apk jar war whl',
	code: 'js mjs cjs ts tsx jsx go py rb rs java kt swift c h cc cpp hpp cs php sh bash zsh fish ps1 bat sql json yaml yml toml ini xml html htm css scss vue svelte lua pl r dart ex exs erl hs scala ipynb',
	cert: 'pub crt cer asc sig gpg',
})) for (const x of v.split(' ')) KINDS[x] = k;
const kindOf = n => {
	n = n.toLowerCase();
	if (n === 'dockerfile' || n === 'makefile') return 'code';
	if (/\.tar\.(gz|bz2|xz|zst)$/.test(n)) return 'archive';
	return KINDS[n.slice(n.lastIndexOf('.') + 1)] || 'other';
};

const Q = [], saved = new Set(); // the queue, and the names saved since the last reload of the rows
let running = 0, paused = false, moved = 0, rate = 0, rateAt = 0, rateMoved = 0, ticker, hideTimer, refreshTimer;
let qEl, qTitle, qSub, qBar, qCancel, qFold, qClose, qList, dropEl, depth = 0;
// An upload can be canceled while it waits or sends. Once the server has the
// whole file ('save') it may already keep it, so it can't be canceled any more.
const pending = it => it.state === 'wait' || it.state === 'up';
const active = it => pending(it) || it.state === 'save';
const width = (b, f) => b.firstChild.style.width = Math.min(100, Math.max(0, f * 100)) + '%';
// nameParts fill a name box, which is always dir=ltr: only the stem is
// isolated (dir=auto), so it reads right to left when its script does, and
// the real extension still comes last, on the right.
const nameParts = n => {
	const [base, ext] = splitExt(n);
	return [el('span', {class: 'base', dir: 'auto', text: base}), el('span', {class: 'ext', dir: 'ltr', text: ext})];
};

const fold = min => {
	qEl.classList.toggle('min', min);
	qFold.setAttribute('aria-label', min ? 'Expand uploads' : 'Collapse uploads');
	qFold.setAttribute('aria-expanded', !min);
};
// The queue is a bottom sheet on phones: toasts sit above it.
const queueHeight = () => H.style.setProperty('--queue-h', qEl && !qEl.hidden && mobile() ? qEl.offsetHeight + 'px' : '0px');
const panel = () => {
	clearTimeout(hideTimer);
	if (qEl) return qEl.hidden = false;
	const button = (label, name) => el('button', {class: 'ib', type: 'button', 'aria-label': label}, icon(name));
	const t = el('div', {class: 'q-t'}, qTitle = el('strong'), qSub = el('span', {class: 'q-sub'}));
	qCancel = el('button', {class: 'btn ghost', type: 'button'}, 'Cancel all');
	qEl = el('aside', {class: 'queue', 'aria-label': 'Uploads'}, el('div', {class: 'q-head'}, t, qCancel, qFold = button('Collapse uploads', 'chevron-down'), qClose = button('Close', 'x')),
		qBar = el('div', {class: 'bar'}, el('span')), qList = el('ul', {class: 'q-list'}));
	qCancel.addEventListener('click', () => {
		for (const it of Q) if (it.state === 'wait') end(it, 'cancel');
		for (const it of Q) if (it.state === 'up') it.xhr.abort();
	});
	for (const e of [t, qFold]) e.addEventListener('click', () => fold(!qEl.classList.contains('min')));
	qClose.addEventListener('click', close);
	fold(mobile());
	D.body.append(qEl);
	new ResizeObserver(queueHeight).observe(qEl);
};
const close = () => {
	if (Q.some(active)) return;
	Q.length = 0;
	qList.replaceChildren();
	qEl.hidden = true;
	queueHeight();
};

const left = s => s < 60 ? Math.max(1, Math.round(s)) + ' s' : s < 3600 ? 'about ' + Math.round(s / 60) + ' min' : 'about ' + Math.round(s / 3600) + ' h';
const sum = (list, f) => list.reduce((s, i) => s + f(i), 0);

// head sums the queue up in the panel's header and its bar.
const head = () => {
	const busy = Q.some(active), n = Q.filter(i => i.state !== 'skip').length, skipped = Q.length - n;
	const ok = Q.filter(i => i.state === 'ok'), bad = Q.filter(i => i.state === 'fail').length;
	const planned = Q.filter(i => active(i) || i.state === 'ok'), size = sum(planned, i => i.size);
	const sent = sum(planned, i => i.state === 'ok' ? i.size : i.sent);
	if (busy) {
		qTitle.textContent = 'Uploading ' + Math.min(n, Q.filter(i => !active(i) && i.state !== 'skip').length + 1) + ' of ' + n;
		qSub.textContent = human(sent) + ' of ' + human(size) + (rate > 0 ? ' · ' + left((size - sent) / rate) + ' left' : '');
	} else {
		const parts = [bad && bad + ' failed', skipped && skipped + ' skipped'].filter(Boolean);
		qTitle.textContent = [ok.length ? parts.length ? ok.length + ' uploaded' : plural(ok.length, 'file', 'files') + ' uploaded' : '', ...parts].filter(Boolean).join(', ') || 'Uploads canceled';
		qSub.textContent = ok.length ? human(sum(ok, i => i.size)) : '';
	}
	width(qBar, busy ? sent / (size || 1) : 1);
	qBar.className = busy ? 'bar' : bad ? 'bar err' : ok.length && !skipped ? 'bar ok' : 'bar';
	qCancel.hidden = !Q.some(pending);
	qClose.hidden = busy;
};

// draw shows an item's state.
const draw = it => {
	const s = it.state, again = s === 'fail' || s === 'cancel', ok = s === 'ok';
	const [ic, cls, text] = s === 'wait' ? [null, '', 'Waiting'] :
		s === 'up' ? [null, '', human(it.sent) + ' of ' + human(it.size) + (it.ticks ? ' · ' + human(Math.round(it.speed)) + '/s' : '')] :
		s === 'save' ? ['loader-circle', '', 'Saving…'] :
		ok ? ['check', 'ok', it.renamed ? ['Saved as ', el('b', {dir: 'ltr'}, ...nameParts(shown(it.saved)))] : 'Saved'] :
		s === 'fail' ? ['triangle-alert', 'err', 'Failed: ' + it.why] :
		s === 'cancel' ? [null, '', 'Canceled'] : ['triangle-alert', 'err', "Skipped: names starting with a dot can't be uploaded"];
	it.status.className = 'q-status ' + cls;
	it.status.replaceChildren(...(ic ? [icon(ic, s === 'save' ? 'sm spin' : 'sm')] : []), el('span', {}, ...[].concat(text)));
	it.bar.hidden = s !== 'up' && s !== 'save';
	width(it.bar, s === 'up' ? it.sent / (it.size || 1) : 1);
	it.button.hidden = ok || s === 'skip' || s === 'save';
	it.button.replaceChildren(icon(again ? 'rotate-ccw' : 'x'));
	it.button.setAttribute('aria-label', (again ? 'Retry upload of ' : 'Cancel upload of ') + shown(it.name));
};

const add = files => {
	if (!files.length) return;
	panel();
	let n = 0;
	for (const file of files) {
		// The server keeps what follows the last slash or backslash; a URL
		// segment can't hold either.
		const name = file.name.slice(Math.max(file.name.lastIndexOf('/'), file.name.lastIndexOf('\\')) + 1);
		const it = {file, name, size: file.size, sent: 0, state: name[0] === '.' ? 'skip' : 'wait'}, kind = kindOf(name);
		it.status = el('div');
		it.bar = el('div', {class: 'bar', role: 'progressbar', 'aria-label': 'Upload of ' + shown(name), 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-valuenow': 0}, el('span'));
		it.button = el('button', {class: 'ib', type: 'button'});
		it.button.addEventListener('click', () => it.state === 'fail' || it.state === 'cancel' ? retry(it) : pending(it) && cancel(it));
		qList.append(el('li', {class: 'q-item k-' + kind}, icon(ICONS[kind] || 'file'),
			el('div', {class: 'q-main'}, el('div', {class: 'q-name nm', dir: 'ltr', title: shown(name)}, ...nameParts(shown(name))), it.status, it.bar),
			it.button));
		Q.push(it);
		draw(it);
		if (it.state === 'wait') n++;
	}
	if (n) {
		paused = false; // new files resume a queue a 401 stopped (see stop)
		say('Uploading ' + plural(n, 'file', 'files') + ' to ' + folder);
	}
	pump();
	head();
};

const pump = () => {
	for (let it; !paused && running < 2 && (it = Q.find(i => i.state === 'wait'));) send(it);
	if (!ticker && Q.some(active)) ticker = setInterval(tick, 1000);
};

const send = it => {
	const x = it.xhr = new XMLHttpRequest();
	running++;
	Object.assign(it, {state: 'up', sent: 0, last: 0, at: performance.now(), speed: 0, ticks: 0, why: ''});
	x.open('PUT', path + encodeURIComponent(it.name));
	x.setRequestHeader('Accept', 'application/json');
	x.upload.addEventListener('progress', e => {
		moved += e.loaded - it.sent;
		it.sent = e.loaded;
		width(it.bar, it.sent / (it.size || 1));
		head();
	});
	x.upload.addEventListener('load', () => {
		it.state = 'save';
		draw(it);
		head();
	});
	x.addEventListener('load', () => {
		let j = null;
		try {
			j = JSON.parse(x.responseText);
		} catch {}
		if (x.status < 200 || x.status > 299) return end(it, 'fail', reason(x, j));
		resumed();
		it.saved = j && j.name || it.name;
		// Renamed: the name was taken, or the server cleaned it up. Its names
		// are NFC: a name that only changed its Unicode form is the same.
		it.renamed = !!(j && (j.renamed || j.name && j.name !== it.name.normalize('NFC')));
		saved.add(it.saved);
		end(it, 'ok');
	});
	x.addEventListener('error', () => end(it, 'fail', 'connection lost'));
	x.addEventListener('abort', () => end(it, 'cancel'));
	x.send(it.file);
	draw(it);
};

// reason says why an upload failed: the edge's error, the server's own
// words, or the status.
const reason = (x, j) => {
	const e = x.getResponseHeader('X-Tund-Error');
	if (x.status === 401) {
		stop();
		return 'your session ended';
	}
	if (e === 'offline' || e === 'notfound') return 'the share went offline';
	if (e === 'badgateway') return "the owner's computer didn't answer";
	if (j && j.error) return String(j.error).replace(/\.$/, '').replace(/^[A-Z](?=[a-z ])/, c => c.toLowerCase());
	return {403: 'not allowed', 404: 'this folder no longer exists', 405: 'uploads are turned off for this share', 507: "the owner's disk is full", 509: 'the share used up its monthly transfer quota'}[x.status] || 'error ' + x.status;
};
// stop ends the queue when the session ended: what waits fails too (Retry or
// new files resume it, after signing in again in another tab).
const stop = () => {
	paused = true;
	session();
	for (const it of Q) if (it.state === 'wait') end(it, 'fail', 'your session ended');
};

const end = (it, state, why) => {
	if (!active(it)) return;
	if (it.state !== 'wait') running--;
	Object.assign(it, {state, why: why || '', xhr: null});
	draw(it);
	pump();
	head();
	if (!Q.some(active)) drained();
};
const cancel = it => it.xhr ? it.xhr.abort() : end(it, 'cancel');
const retry = it => {
	panel();
	paused = false;
	Object.assign(it, {state: 'wait', sent: 0});
	draw(it);
	pump();
	head();
};

// tick runs every second while uploads run: speeds (moving averages over
// about 3 s), the time left and the progress bars' values.
const tick = () => {
	const now = performance.now(), a = 1 - Math.exp(-1 / 3);
	for (const it of Q) {
		if (it.state !== 'up') continue;
		const r = (it.sent - it.last) / Math.max(1e-3, (now - it.at) / 1e3);
		it.speed = it.ticks++ ? it.speed + a * (r - it.speed) : r;
		it.last = it.sent;
		it.at = now;
		it.bar.setAttribute('aria-valuenow', Math.round(it.sent / (it.size || 1) * 100));
		draw(it);
	}
	if (rateAt) {
		const r = (moved - rateMoved) / Math.max(1e-3, (now - rateAt) / 1e3);
		rate = rate ? rate + a * (r - rate) : r;
	}
	rateAt = now;
	rateMoved = moved;
	head();
};

const drained = () => {
	clearInterval(ticker);
	ticker = 0;
	rate = rateAt = 0;
	const ok = Q.filter(i => i.state === 'ok').length, bad = Q.filter(i => i.state === 'fail');
	if (bad.length) say(plural(bad.length, 'upload', 'uploads') + ' failed: ' + bad.map(i => shown(i.name)).join(', '));
	else if (ok) say(plural(ok, 'file', 'files') + ' uploaded');
	if (ok && Q.every(i => i.state === 'ok')) hideTimer = setTimeout(close, 6000);
	clearTimeout(refreshTimer);
	if (!saved.size) return;
	refreshTimer = setTimeout(() => {
		refresh(new Set(saved)).catch(basic);
		saved.clear();
	}, 500);
};

const overlay = show => {
	if (!dropEl && !show) return;
	if (!dropEl) {
		dropEl = el('div', {class: 'drop', hidden: true, 'aria-hidden': 'true'}, el('div', {class: 'drop-in'}, icon('cloud-upload', 'xl'),
			el('p', {class: 'drop-t'}, 'Drop to upload to ', el('b', {dir: 'auto', text: folder})),
			el('p', {class: 'drop-s', text: 'Existing files are never replaced'})));
		D.body.append(dropEl);
	}
	dropEl.hidden = !show;
	if (!show) depth = 0;
};

// take queues dropped files; folders can't be uploaded, so they are skipped.
const take = dt => {
	const files = [];
	let dirs = 0;
	for (const i of Array.from(dt.items || [])) {
		const entry = i.kind === 'file' && i.webkitGetAsEntry && i.webkitGetAsEntry(), f = i.kind === 'file' && i.getAsFile();
		if (entry && entry.isDirectory) dirs++;
		else if (f) files.push(f);
	}
	if (!dt.items) files.push(...dt.files);
	if (dirs) toast("Folders can't be uploaded. Drop the files inside, or zip the folder first.");
	add(files);
};

ready.push(() => {
	const pick = $('[data-pick-input]');
	for (const b of $$('[data-pick]')) b.addEventListener('click', () => pick.click());
	pick.addEventListener('change', () => {
		add(Array.from(pick.files));
		pick.value = '';
	});
	addEventListener('dragenter', e => {
		if (!dragsFiles(e)) return;
		e.preventDefault();
		if (!depth++) overlay(true);
	});
	addEventListener('dragover', e => {
		if (dragsFiles(e)) e.preventDefault();
	});
	addEventListener('dragleave', e => {
		if (dragsFiles(e) && depth && !--depth) overlay(false);
	});
	addEventListener('drop', e => {
		if (!dragsFiles(e)) return;
		e.preventDefault();
		overlay(false);
		take(e.dataTransfer);
	});
	D.addEventListener('keydown', e => {
		if (e.key === 'Escape') overlay(false);
	});
	addEventListener('beforeunload', e => {
		if (!Q.some(active)) return;
		e.preventDefault();
		e.returnValue = '';
	});
});
