// The folder page: in-place sorting and filtering, relative dates, a
// collapsible breadcrumb and copy-link buttons. The page's rows are the
// model; the folder's JSON listing is fetched only to filter a truncated
// folder and after uploads (upload.js), never on a timer.

const MAX = 2000; // rows a page shows, like the server
const ICONS = {folder: 'folder', image: 'file-image', video: 'file-play', audio: 'file-headphone', pdf: 'file-text', doc: 'file-text', sheet: 'file-spreadsheet', slides: 'presentation', archive: 'file-archive', code: 'file-code', cert: 'file-key'};

// shown escapes names from the JSON listing like the server's view-model:
// control characters and the marks that reorder text become visible \uXXXX.
const shown = s => s.replace(/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g,
	c => '\\u' + c.charCodeAt(0).toString(16).toUpperCase().padStart(4, '0'));

// splitExt keeps an extension apart, so truncating a name keeps it.
const splitExt = (n, dir) => {
	const t = /\.tar\.(gz|bz2|xz|zst|lz|lz4|lzma|br|z)$/i.exec(n), i = n.lastIndexOf('.');
	if (dir) return [n, ''];
	if (t && t.index) return [n.slice(0, t.index), t[0]];
	if (i <= 0 || n.length - i > 17 || /\s/.test(n.slice(i))) return [n, ''];
	return [n.slice(0, i), n.slice(i)];
};

const count = n => n.toLocaleString('en-US');
const plural = (n, one, many) => n === 1 ? '1 ' + one : count(n) + ' ' + many;
const human = n => {
	let d = 1, e = -1;
	while (n / d >= 1024 && e < 5) d *= 1024, e++;
	return e < 0 ? n + ' B' : (n / d).toFixed(1) + ' ' + 'KMGTPE'[e] + 'B';
};

const MONTHS = 'Jan Feb Mar Apr May Jun Jul Aug Sep Oct Nov Dec'.split(' ');
const DAYS = 'Sunday Monday Tuesday Wednesday Thursday Friday Saturday'.split(' ');
const midnight = t => new Date(t).setHours(0, 0, 0, 0);
const ago = (t, now) => {
	const s = (now - t) / 1e3, d = new Date(t), days = Math.round((midnight(now) - midnight(t)) / 864e5);
	if (Math.abs(s) < 60) return 'just now';
	if (s > 0 && s < 3600) return Math.floor(s / 60) + ' min ago';
	if (s > 0 && s < 86400) return Math.floor(s / 3600) + ' h ago';
	if (days === 1) return 'Yesterday';
	if (days > 1 && days < 7) return DAYS[d.getDay()];
	return d.getDate() + ' ' + MONTHS[d.getMonth()] + (d.getFullYear() === new Date(now).getFullYear() ? '' : ' ' + d.getFullYear());
};
// stamp shows a time relative to now, with the local date and time as its title.
const stamp = (t, now) => {
	const d = Date.parse(t.getAttribute('datetime'));
	t.textContent = ago(d, now || Date.now());
	t.title = new Date(d).toLocaleString(undefined, {dateStyle: 'medium', timeStyle: 'short'});
};

const mobile = () => matchMedia('(max-width: 640px)').matches;
const say = text => {
	const s = $('[data-say]');
	s.textContent = '';
	setTimeout(() => s.textContent = text, 60);
};

let model = [], full, total, by = 'name', desc = false, path, folder, upload;
let table, tpl, notice, noMatch, fq, sel, toolbar;

// A model entry is one row: from the page, or from the JSON listing.
const fromRow = tr => {
	const a = $('a', tr), kind = tr.className.slice(2), iso = $('time', tr.cells[2]).getAttribute('datetime');
	return {tr, kind, name: a.title, href: a.getAttribute('href'), dir: kind === 'folder', size: +tr.dataset.size || 0, iso, mod: Date.parse(iso)};
};
const fromJSON = e => ({raw: e.name, kind: e.kind, name: shown(e.name), href: e.href, dir: e.type === 'folder', size: e.size || 0, iso: e.modified, mod: Date.parse(e.modified)});

const linkButton = m => el('button', {class: 'ib', type: 'button', 'data-copy': m.href, 'aria-label': 'Copy link to ' + m.name}, icon('link'));

// row returns an entry's table row, built from the page's row template.
const row = m => {
	if (m.tr) return m.tr;
	const tr = m.tr = tpl.content.firstElementChild.cloneNode(true), [cell, size, mod, act] = tr.cells, a = $('a', cell);
	const [base, ext] = splitExt(m.name, m.dir), sub = $('.sub', a), t = el('time', {datetime: m.iso});
	tr.className = 'k-' + m.kind;
	a.href = m.href;
	a.title = m.name;
	$('use', a).setAttribute('href', '#i-' + (ICONS[m.kind] || 'file'));
	$('.base', a).textContent = base;
	$('.ext', a).textContent = ext;
	sub.append(m.dir ? '' : human(m.size) + ' · ', t);
	mod.firstChild.setAttribute('datetime', m.iso);
	stamp(t);
	stamp(mod.firstChild);
	size.textContent = m.dir ? '—' : human(m.size);
	if (m.dir) {
		size.setAttribute('aria-label', 'Folder');
		act.replaceChildren();
	} else {
		act.firstChild.href = m.href + '?download';
		act.firstChild.setAttribute('aria-label', 'Download ' + m.name);
		act.prepend(linkButton(m));
	}
	return tr;
};

const coll = new Intl.Collator(undefined, {numeric: true, sensitivity: 'base'});
const sort = () => model.sort((a, b) => {
	if (a.dir !== b.dir) return a.dir ? -1 : 1;
	const c = (by === 'size' ? a.size - b.size : by === 'modified' ? a.mod - b.mod : 0) ||
		coll.compare(a.name, b.name) || (a.name > b.name) - (a.name < b.name);
	return desc ? -c : c;
});

const query = () => fq.value.trim().toLowerCase();
const hits = () => {
	const q = query();
	return q ? model.filter(m => (m.lc ||= m.name.toLowerCase()).includes(q)) : model;
};

// render shows the sorted model, filtered, at most MAX rows.
const render = () => {
	const list = hits(), f = D.createDocumentFragment(), empty = !model.length;
	for (const m of list.slice(0, MAX)) f.append(row(m));
	if (!full && !query() && notice) f.append(notice);
	else if (list.length > MAX) f.append(el('tr', {class: 'notice'}, el('td', {colspan: 4, text: 'Showing the first ' + count(MAX) + ' of ' + count(list.length) + ' items.'})));
	table.tBodies[0].replaceChildren(f);
	table.hidden = !list.length;
	noMatch.hidden = empty || list.length > 0;
	$('p', noMatch).textContent = 'Nothing in this folder matches “' + fq.value.trim() + '”.';
	for (const e of $$('[data-empty]')) e.hidden = !empty;
	for (const e of [toolbar, $('.actions')]) if (e) e.hidden = empty;
	const all = plural(full ? model.length : total, 'item', 'items');
	$('.count').textContent = query() ? count(list.length) + ' of ' + all : all;
};

let sayTimer;
const filtered = () => {
	if (!full) fetchAll();
	render();
	clearTimeout(sayTimer);
	sayTimer = setTimeout(() => say($('.count').textContent), 300);
};

const setSort = (b, d) => {
	by = b;
	desc = d;
	const order = d ? 'desc' : 'asc';
	for (const th of $$('th[aria-sort]', table)) {
		const a = $('a', th), k = new URL(a.href).searchParams.get('sort'), on = k === by;
		th.setAttribute('aria-sort', on ? (d ? 'descending' : 'ascending') : 'none');
		a.href = path + '?sort=' + k + '&order=' + (on && !d ? 'desc' : 'asc');
	}
	sel.value = by + ':' + order;
	history.replaceState(null, '', path + '?sort=' + by + '&order=' + order);
	sort();
	render();
};

// session tells the visitor that the edge no longer knows them; upload.js
// also stops its queue. An answer that gets through again (after a sign-in
// in another tab) takes the notice back.
const session = () => {
	if ($('[data-session]')) return;
	const b = el('button', {class: 'btn', type: 'button'}, 'Reload');
	b.addEventListener('click', () => location.reload());
	$('main').prepend(el('div', {class: 'banner err', role: 'alert', 'data-session': true}, icon('triangle-alert'), el('span', {text: 'Your session ended. Reload the page to sign in again.'}), b));
};
const resumed = () => {
	const b = $('[data-session]');
	if (b) b.remove();
};

// listing fetches the JSON listing: every entry, not just the rows shown.
const listing = async () => {
	try {
		const r = await fetch(path + '?format=json', {headers: {Accept: 'application/json'}, cache: 'no-store'});
		if (r.status === 401) session();
		else if (r.ok) resumed();
		return r.ok ? await r.json() : null;
	} catch {
		return null;
	}
};
// load replaces the rows with a JSON listing and highlights the fresh names.
// A row's link or button that had the focus gets it back in the new row
// (found by href), so keyboard users keep their place.
const load = (j, fresh) => {
	const f = D.activeElement, tr = f && f.closest('tbody tr'), href = tr && $('a', tr).getAttribute('href'), at = tr ? $$('a,button', tr).indexOf(f) : -1;
	model = j.entries.map(fromJSON);
	full = true;
	sort();
	render();
	if (href) {
		const m = model.find(m => m.href === href && m.tr);
		if (m) ($$('a,button', m.tr)[at] || $('a', m.tr)).focus();
		else $('#files').focus(); // the row is gone: stay in the list
	}
	const dirs = model.filter(m => m.dir).length, files = model.length - dirs;
	$('.meta').textContent = [dirs && plural(dirs, 'folder', 'folders'), files && plural(files, 'file', 'files')].filter(Boolean).join(' · ') || 'Empty folder';
	for (const m of model) {
		if (!fresh || !fresh.has(m.raw)) continue;
		const tr = row(m);
		tr.classList.add('fresh');
		setTimeout(() => tr.classList.remove('fresh'), 2000);
	}
};
let all;
const fetchAll = () => all ||= listing().then(j => j && load(j)).catch(basic);

// refresh reloads the rows once uploads are done, with a progress bar when
// that takes a moment.
const refresh = async names => {
	const bar = el('div', {class: 'busy'}), slow = setTimeout(() => {
		$('#files').append(bar);
		table.setAttribute('aria-busy', 'true');
	}, 300);
	const j = await listing();
	clearTimeout(slow);
	bar.remove();
	table.removeAttribute('aria-busy');
	if (j) load(j, names);
};

const crumbs = () => {
	const nav = $('.crumbs'), lis = nav ? $$('li', nav) : [], n = lis.length;
	if (n < 3) return;
	const b = el('button', {type: 'button', 'aria-label': 'Show full path'}, '…');
	b.addEventListener('click', () => nav.classList.add('open'));
	lis[0].after(el('li', {class: 'ell'}, b));
	lis.forEach((li, i) => {
		if (i && i < n - 1) li.classList.add('mid');
		if (i && i < n - 2) li.classList.add('far');
	});
	nav.classList.add('deep');
	if (n > 4) nav.classList.add('long');
};

const dragsFiles = e => e.dataTransfer && [...e.dataTransfer.types].includes('Files');

ready.push(() => {
	const body = D.body;
	path = body.dataset.path;
	upload = body.hasAttribute('data-upload');
	full = !body.hasAttribute('data-truncated');
	total = +body.dataset.total;
	folder = $('.title').textContent;
	table = $('table.files');
	tpl = $('template[data-row]');
	toolbar = $('.toolbar');
	fq = $('[data-filter]');
	sel = $('[data-sort]');
	notice = $('tr.notice', table);
	model = $$('tr', table.tBodies[0]).filter(tr => tr.className.startsWith('k-')).map(fromRow);
	for (const m of model) if (!m.dir) m.tr.cells[3].prepend(linkButton(m));
	const th = $('th[aria-sort$=ding]', table);
	if (th) {
		by = new URL($('a', th).href).searchParams.get('sort');
		desc = th.getAttribute('aria-sort') === 'descending';
	}
	sel.value = by + ':' + (desc ? 'desc' : 'asc');
	const clear = el('button', {class: 'btn ghost', type: 'button'}, 'Clear filter');
	clear.addEventListener('click', () => {
		fq.value = '';
		filtered();
		fq.focus();
	});
	noMatch = el('div', {class: 'empty', hidden: true}, el('div', {class: 'empty-ic'}, icon('search', 'lg')), el('h2', {text: 'No matches'}), el('p'), clear);
	table.after(noMatch);

	for (const a of $$('thead a', table)) {
		a.addEventListener('click', e => {
			if (!full) return; // the server sorts what the page doesn't hold
			e.preventDefault();
			const p = new URL(a.href).searchParams;
			setSort(p.get('sort'), p.get('order') === 'desc');
		});
	}
	sel.addEventListener('change', () => {
		const [b, o] = sel.value.split(':');
		if (full) setSort(b, o === 'desc');
		else location.search = '?sort=' + b + '&order=' + o;
	});
	fq.addEventListener('input', filtered);
	fq.addEventListener('keydown', e => {
		if (e.key !== 'Escape') return;
		e.preventDefault();
		if (fq.value) {
			fq.value = '';
			filtered();
		} else fq.blur();
	});
	D.addEventListener('keydown', e => {
		const t = e.target;
		if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey || toolbar.hidden || t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
		e.preventDefault();
		fq.focus();
	});

	const now = Date.now();
	for (const t of $$('time', table)) stamp(t, now);
	setInterval(() => {
		const now = Date.now();
		for (const m of model) if (m.tr) for (const t of $$('time', m.tr)) stamp(t, now);
	}, 60000);
	crumbs();
	// The curl help starts open for empty folders and uploads, except on
	// phones, where it is far down the page.
	if (mobile()) for (const d of $$('details.curl')) d.open = false;
	if (upload) return; // upload.js takes dropped files

	// A file dropped on a share without uploads must not open instead.
	addEventListener('dragover', e => {
		if (dragsFiles(e)) e.preventDefault();
	});
	addEventListener('drop', e => {
		if (!dragsFiles(e)) return;
		e.preventDefault();
		toast('Uploads are turned off for this share.');
	});
});
