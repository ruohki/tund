// Every page of a file share works without scripts; this only enhances it.
// When it fails, the page switches back to that basic mode (the error
// boundary): on uncaught errors from the page's own origin, and at the end of
// its promise chains (.catch(basic)). Errors of browser extensions and other
// scripts come from another origin or none, and leave the page alone.
// page.go wraps this file and the page's own script in one strict function
// that runs in <head>: they define what to do and push it onto ready.
// The DOM is built with createElement and textContent only, never from HTML
// strings (Trusted Types would refuse those).
const D = document, H = D.documentElement, ready = [];
H.className = 'js';
let broken = false;
const basic = () => {
	broken = true;
	H.className = 'nojs';
	const b = D.getElementById('basic');
	if (b) b.hidden = false;
};
addEventListener('error', e => {
	if (e.filename && e.filename.startsWith(location.origin + '/')) basic();
});
D.addEventListener('DOMContentLoaded', () => {
	if (broken) return basic();
	try {
		for (const f of ready) f();
	} catch {
		basic();
	}
});

const $ = (s, r = D) => r.querySelector(s);
const $$ = (s, r = D) => [...r.querySelectorAll(s)];

// el builds an element: props become attributes (text sets the text), and
// children are nodes or strings.
const el = (tag, props, ...kids) => {
	const e = D.createElement(tag);
	for (const [k, v] of Object.entries(props || {})) {
		if (v == null || v === false) continue;
		if (k === 'text') e.textContent = v;
		else e.setAttribute(k, v === true ? '' : v);
	}
	e.append(...kids.filter(k => k != null && k !== false));
	return e;
};

// icon is a symbol of the page's sprite.
const icon = (name, cls) => {
	const s = D.createElementNS('http://www.w3.org/2000/svg', 'svg'), u = D.createElementNS(s.namespaceURI, 'use');
	s.setAttribute('class', cls ? 'i ' + cls : 'i');
	s.setAttribute('aria-hidden', 'true');
	u.setAttribute('href', '#i-' + name);
	s.append(u);
	return s;
};

// toast shows a short message; errors stay until closed, at most 3 at once.
const toast = (msg, err) => {
	const box = $('.toasts');
	if (!box) return;
	const t = el('div', {class: 'toast', role: err ? 'alert' : null}, icon(err ? 'triangle-alert' : 'check'), el('span', {text: msg}));
	if (err) {
		const x = el('button', {class: 'ib', type: 'button', 'aria-label': 'Close'}, icon('x'));
		x.addEventListener('click', () => t.remove());
		t.append(x);
	} else {
		setTimeout(() => {
			t.classList.add('out');
			setTimeout(() => t.remove(), 150);
		}, 4000);
	}
	box.append(t);
	while (box.children.length > 3) box.firstElementChild.remove();
};

// Copy buttons: data-copy holds a link's path, data-cmd sits next to a
// command block (its # comment lines are left out).
const copy = (b, text, what) => {
	const done = ok => {
		if (!ok) return toast("Couldn't copy. Your browser blocked the clipboard.", true);
		toast(what + ' copied');
		const u = $('use', b), h = u.dataset.i || (u.dataset.i = u.getAttribute('href'));
		u.setAttribute('href', '#i-check');
		setTimeout(() => u.setAttribute('href', h), 1500);
	};
	if (navigator.clipboard) navigator.clipboard.writeText(text).then(() => done(true), () => done(false)).catch(basic);
	else done(false);
};
D.addEventListener('click', e => {
	const b = e.target.closest('[data-copy],[data-cmd]');
	if (!b) return;
	if (b.dataset.copy) return copy(b, location.origin + b.dataset.copy, 'Link');
	const lines = $('pre', b.parentNode).textContent.split('\n');
	copy(b, lines.filter(l => !l.startsWith('#')).join('\n').trim(), 'Command');
});

// An image preview that can't be shown gives way to the file's icon.
ready.push(() => {
	const img = $('img.preview');
	if (!img) return;
	const fail = () => {
		img.hidden = true;
		$('.tile').hidden = false;
	};
	img.addEventListener('error', fail);
	if (img.complete && !img.naturalWidth) fail();
});
