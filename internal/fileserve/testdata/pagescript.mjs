// Runs a folder page's own script against a small DOM built from the page,
// for TestPageScripts:
//
//	node pagescript.mjs <page.html> <listing.json> <scenario>
//
// The DOM holds what the page scripts use and no more: a tree of elements and
// text, attributes and the properties that reflect them, events, focus (which
// leaves nodes removed from the document, as in browsers) and the selectors
// the scripts use. XMLHttpRequest, fetch and timers are fakes the scenarios
// drive step by step. A scenario prints what failed and exits 1.
import fs from 'node:fs';
import vm from 'node:vm';

const [pageFile, listingFile, scenario] = process.argv.slice(2);
const html = fs.readFileSync(pageFile, 'utf8'), listing = fs.readFileSync(listingFile, 'utf8');
const ORIGIN = 'http://share.example', PAGE = ORIGIN + '/inbox/';

class Event {
	constructor(type, init = {}) {
		Object.assign(this, {bubbles: false}, init, {type, defaultPrevented: false});
	}
	preventDefault() {
		this.defaultPrevented = true;
	}
}

class Target {
	listeners = {};
	addEventListener(type, f) {
		(this.listeners[type] ||= []).push(f);
	}
	removeEventListener(type, f) {
		this.listeners[type] = (this.listeners[type] || []).filter(g => g !== f);
	}
	dispatchEvent(e) {
		e.target ||= this;
		for (const n of e.bubbles ? this.path() : [this]) for (const f of [...(n.listeners[e.type] || [])]) f.call(n, e);
		return !e.defaultPrevented;
	}
	path() {
		return [this];
	}
}

class Node extends Target {
	parentNode = null;
	childNodes = [];
	constructor(doc) {
		super();
		this.ownerDocument = doc;
	}
	path() {
		const p = [];
		for (let n = this; n; n = n.parentNode) p.push(n);
		if (p.at(-1) === this.ownerDocument) p.push(this.ownerDocument.defaultView);
		return p;
	}
	get firstChild() {
		return this.childNodes[0] || null;
	}
	get children() {
		return this.childNodes.filter(n => n instanceof Element);
	}
	get firstElementChild() {
		return this.children[0] || null;
	}
	get textContent() {
		return this.childNodes.map(n => n.textContent).join('');
	}
	set textContent(v) {
		this.replaceChildren(String(v));
	}
	get isConnected() {
		let n = this;
		while (n.parentNode) n = n.parentNode;
		return n === this.ownerDocument;
	}
	// insert takes nodes from their parents and inserts them at index at():
	// fragments give their children, strings become text.
	insert(kids, at) {
		const nodes = kids.flatMap(k => k instanceof Fragment ? [...k.childNodes] : [k instanceof Node ? k : new Text(this.ownerDocument, String(k))]);
		for (const n of nodes) n.remove();
		this.childNodes.splice(at(), 0, ...nodes);
		for (const n of nodes) n.parentNode = this;
	}
	append(...kids) {
		this.insert(kids, () => this.childNodes.length);
	}
	prepend(...kids) {
		this.insert(kids, () => 0);
	}
	after(...kids) {
		const p = this.parentNode;
		p.insert(kids, () => p.childNodes.indexOf(this) + 1);
	}
	replaceChildren(...kids) {
		for (const c of [...this.childNodes]) c.remove();
		this.append(...kids);
	}
	remove() {
		const p = this.parentNode, d = this.ownerDocument;
		if (!p) return;
		p.childNodes.splice(p.childNodes.indexOf(this), 1);
		this.parentNode = null;
		if (d.focused && !d.focused.isConnected) d.focused = null; // the focus fixup rule
	}
	querySelectorAll(s) {
		const out = [], walk = n => {
			for (const c of n.children) {
				if (matches(c, s)) out.push(c);
				walk(c);
			}
		};
		walk(this);
		return out;
	}
	querySelector(s) {
		return this.querySelectorAll(s)[0] || null;
	}
}

class Text extends Node {
	constructor(doc, data) {
		super(doc);
		this.data = data;
	}
	get textContent() {
		return this.data;
	}
	set textContent(v) {
		this.data = String(v);
	}
	cloneNode() {
		return new Text(this.ownerDocument, this.data);
	}
}

class Fragment extends Node {}

class Element extends Node {
	attrs = new Map();
	style = {setProperty(k, v) {
		this[k] = v;
	}};
	value = '';
	constructor(doc, name, ns = 'http://www.w3.org/1999/xhtml') {
		super(doc);
		this.localName = name.toLowerCase();
		this.tagName = this.localName.toUpperCase();
		this.namespaceURI = ns;
		if (this.localName === 'template') this.content = Object.assign(new Fragment(doc), {host: this});
	}
	getAttribute(k) {
		return this.attrs.has(k) ? this.attrs.get(k) : null;
	}
	setAttribute(k, v) {
		this.attrs.set(k, String(v));
	}
	hasAttribute(k) {
		return this.attrs.has(k);
	}
	removeAttribute(k) {
		this.attrs.delete(k);
	}
	get id() {
		return this.getAttribute('id') || '';
	}
	get className() {
		return this.getAttribute('class') || '';
	}
	set className(v) {
		this.setAttribute('class', v);
	}
	get classList() {
		const get = () => this.className.split(/\s+/).filter(Boolean), set = l => this.className = [...new Set(l)].join(' ');
		return {
			contains: c => get().includes(c),
			add: (...cs) => set([...get(), ...cs]),
			remove: (...cs) => set(get().filter(c => !cs.includes(c))),
			toggle: (c, on = !get().includes(c)) => (set(on ? [...get(), c] : get().filter(x => x !== c)), on),
		};
	}
	get hidden() {
		return this.hasAttribute('hidden');
	}
	set hidden(v) {
		if (v) this.setAttribute('hidden', '');
		else this.removeAttribute('hidden');
	}
	get title() {
		return this.getAttribute('title') || '';
	}
	set title(v) {
		this.setAttribute('title', v);
	}
	get href() {
		const v = this.getAttribute('href');
		return v === null ? '' : new URL(v, this.ownerDocument.defaultView.location.href).href;
	}
	set href(v) {
		this.setAttribute('href', v);
	}
	get dataset() {
		const key = k => 'data-' + k.replace(/[A-Z]/g, c => '-' + c.toLowerCase());
		return new Proxy({}, {
			get: (_, k) => typeof k === 'string' && this.hasAttribute(key(k)) ? this.getAttribute(key(k)) : undefined,
			set: (_, k, v) => (this.setAttribute(key(k), v), true),
		});
	}
	get cells() {
		return this.children.filter(c => c.localName === 'td' || c.localName === 'th');
	}
	get tBodies() {
		return this.children.filter(c => c.localName === 'tbody');
	}
	get offsetHeight() {
		return 0;
	}
	cloneNode(deep) {
		const e = new Element(this.ownerDocument, this.localName, this.namespaceURI);
		for (const [k, v] of this.attrs) e.attrs.set(k, v);
		if (deep) e.append(...this.childNodes.map(c => c.cloneNode(true)));
		return e;
	}
	closest(s) {
		for (let e = this; e instanceof Element; e = e.parentNode) if (matches(e, s)) return e;
		return null;
	}
	focus() {
		if (this.isConnected) this.ownerDocument.focused = this;
	}
	click() {
		this.dispatchEvent(new Event('click', {bubbles: true}));
	}
}

class Document extends Node {
	focused = null;
	constructor(win) {
		super(null);
		this.ownerDocument = this;
		this.defaultView = win;
	}
	get documentElement() {
		return this.firstElementChild;
	}
	get body() {
		return this.querySelector('body');
	}
	get activeElement() {
		return this.focused || this.body;
	}
	createElement(name) {
		return new Element(this, name);
	}
	createElementNS(ns, name) {
		return new Element(this, name, ns);
	}
	createDocumentFragment() {
		return new Fragment(this);
	}
	getElementById(id) {
		return this.querySelector('#' + id);
	}
}

// Selectors: lists of descendant chains of compounds made of tag, .class,
// #id, [attr], [attr=v], [attr$=v] and [attr^=v].
const parsed = new Map();
const selector = s => {
	if (!parsed.has(s)) {
		parsed.set(s, s.split(',').map(chain => chain.trim().split(/\s+/).map(c => {
			const re = /([a-z][\w-]*)|\.([\w-]+)|#([\w-]+)|\[([\w-]+)(?:([$^]?=)"?([^"\]]*)"?)?\]/y, parts = [];
			while (re.lastIndex < c.length) {
				const m = re.exec(c);
				if (!m) throw new Error('unsupported selector ' + s);
				parts.push(m);
			}
			return parts;
		})));
	}
	return parsed.get(s);
};
const compound = (e, parts) => parts.every(([, tag, cls, id, attr, op, v]) => {
	if (tag) return e.localName === tag;
	if (cls) return e.classList.contains(cls);
	if (id) return e.id === id;
	const a = e.getAttribute(attr);
	return a !== null && (!op || (op === '=' ? a === v : op === '$=' ? a.endsWith(v) : a.startsWith(v)));
});
const matches = (e, s) => selector(s).some(chain => {
	if (!compound(e, chain.at(-1))) return false;
	let n = e.parentNode;
	for (let i = chain.length - 2; i >= 0; i--) {
		while (n instanceof Element && !compound(n, chain[i])) n = n.parentNode;
		if (!(n instanceof Element)) return false;
		n = n.parentNode;
	}
	return true;
});

// parse builds the document from the page, which html/template wrote: tags
// are well formed and attribute values double quoted.
const VOID = new Set('area base br col embed hr img input link meta source track wbr'.split(' '));
const decode = s => s.replace(/&(#x[0-9a-f]+|#\d+|amp|lt|gt|quot|apos);/gi, (_, e) =>
	e[0] === '#' ? String.fromCodePoint(e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : +e.slice(1)) :
	{amp: '&', lt: '<', gt: '>', quot: '"', apos: "'"}[e.toLowerCase()]);
const parse = (doc, src) => {
	const stack = [doc], re = /<!--[\s\S]*?-->|<!doctype[^>]*>|<(script|style)\b([^>]*)>([\s\S]*?)<\/\1\s*>|<\/([a-z][\w-]*)\s*>|<([a-z][\w-]*)((?:\s+[^\s=>/]+(?:="[^"]*")?)*)\s*(\/?)>|[^<]+/gi;
	for (const [all, raw, rawAttrs, text, close, open, attrs, self] of src.matchAll(re)) {
		if (all.startsWith('<!')) continue;
		if (close) {
			const i = stack.findLastIndex(n => (n.host || n).localName === close.toLowerCase());
			if (i > 0) stack.length = i;
			continue;
		}
		if (!raw && !open) {
			stack.at(-1).append(decode(all));
			continue;
		}
		const e = doc.createElement(raw || open);
		for (const [, k, v] of (raw ? rawAttrs : attrs).matchAll(/([^\s=]+)(?:="([^"]*)")?/g)) e.setAttribute(k.toLowerCase(), decode(v || ''));
		stack.at(-1).append(e);
		if (raw) e.text = text;
		else if (!self && !VOID.has(e.localName)) stack.push(e.content || e);
	}
};

const settle = () => new Promise(r => setImmediate(r));

// load opens the page like a browser: the DOM, the page's script, then
// DOMContentLoaded. opts.listing replaces the folder's JSON listing, and
// opts.truncated marks the page as showing part of the folder.
const load = (opts = {}) => {
	const win = new Target(), xhrs = [], timers = [];
	let clock = 0, seq = 0;
	const timer = (f, ms, every) => (timers.push({f, at: clock + (ms || 0), every, id: ++seq}), seq);
	const clear = id => {
		const i = timers.findIndex(t => t.id === id);
		if (i >= 0) timers.splice(i, 1);
	};
	class XMLHttpRequest extends Target {
		upload = new Target();
		status = 0;
		responseText = '';
		aborted = false;
		constructor() {
			super();
			xhrs.push(this);
		}
		open(method, url) {
			Object.assign(this, {method, url});
		}
		setRequestHeader() {}
		getResponseHeader() {
			return null;
		}
		send(body) {
			this.body = body;
		}
		abort() {
			this.aborted = true;
			this.dispatchEvent(new Event('abort'));
		}
		// The scenarios' side: the body went out, then the server answered.
		sent() {
			this.upload.dispatchEvent(new Event('progress', {loaded: this.body.size}));
			this.upload.dispatchEvent(new Event('load'));
		}
		respond(status, body) {
			Object.assign(this, {status, responseText: JSON.stringify(body)});
			this.dispatchEvent(new Event('load'));
		}
	}
	const doc = new Document(win);
	parse(doc, html);
	if (opts.truncated) doc.body.setAttribute('data-truncated', '');
	const location = {href: PAGE, origin: ORIGIN, reload() {}};
	Object.assign(win, {
		window: win, document: doc, location, navigator: {}, XMLHttpRequest, URL, performance,
		history: {replaceState: (_, __, url) => location.href = new URL(url, location.href).href},
		matchMedia: () => ({matches: false}),
		ResizeObserver: class {
			observe() {}
		},
		fetch: async () => ({status: 200, ok: true, json: async () => JSON.parse(opts.listing || listing)}),
		setTimeout: (f, ms) => timer(f, ms), setInterval: (f, ms) => timer(f, ms, ms), clearTimeout: clear, clearInterval: clear,
		addEventListener: Target.prototype.addEventListener.bind(win),
		removeEventListener: Target.prototype.removeEventListener.bind(win),
		dispatchEvent: Target.prototype.dispatchEvent.bind(win),
	});
	vm.createContext(win);
	vm.runInContext(doc.querySelector('head script').text, win, {filename: PAGE});
	doc.dispatchEvent(new Event('DOMContentLoaded'));
	if (doc.documentElement.className !== 'js') {
		console.log('the page switched to basic mode while it loaded');
		process.exit(1);
	}
	const $ = (s, r = doc) => r.querySelector(s), $$ = (s, r = doc) => r.querySelectorAll(s);
	const p = {
		win, doc, xhrs, $, $$,
		// pick queues files as the file picker does.
		pick(...names) {
			const input = $('[data-pick-input]');
			input.files = names.map(name => ({name, size: 1000}));
			input.dispatchEvent(new Event('change'));
		},
		// item is a queued file in the upload panel: by name, or by its place.
		item: n => typeof n === 'number' ? $$('.q-item')[n] : $$('.q-item').find(li => $('.q-name', li).title === n),
		status: n => $('.q-status', p.item(n)).textContent,
		button: n => $('button', p.item(n)),
		xhr: name => xhrs.find(x => x.url === '/inbox/' + encodeURIComponent(name)),
		// basic reports whether the error boundary switched the page to basic mode.
		basic: () => doc.documentElement.className === 'nojs' && !$('#basic').hidden,
		// tick runs the timers due in the next ms milliseconds and lets the
		// promises they start settle.
		async tick(ms) {
			const end = clock + ms;
			for (let t; (t = timers.filter(t => t.at <= end).sort((a, b) => a.at - b.at || a.id - b.id)[0]);) {
				clock = t.at;
				if (t.every) t.at += t.every;
				else clear(t.id);
				t.f();
				await settle();
			}
			clock = end;
			await settle();
		},
	};
	return p;
};

const fails = [];
// want records what failed, with characters outside printable ASCII escaped
// (names may reorder the terminal's line).
const want = (ok, what) => {
	if (!ok) fails.push(String(what).replace(/[^ -~]/g, c => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0')));
};
process.on('unhandledRejection', e => fails.push('unhandled rejection: ' + e));
// box describes a name box: its dir, then its stem's and extension's dir and text.
const box = (p, e) => e && [e.getAttribute('dir'), ...['.base', '.ext'].map(s => p.$(s, e)).map(x => x && x.getAttribute('dir') + ' ' + x.textContent)];
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

const scenarios = {
	// A name box is LTR with only the stem isolated: in the upload queue, in
	// "Saved as" and in the rows rebuilt from the JSON listing.
	async 'name boxes'() {
		const p = load(), spoof = '\u05f3invoice.pdf\u05f3.exe';
		p.pick(spoof);
		const queued = box(p, p.$('.q-name', p.item(spoof)));
		want(same(queued, ['ltr', 'auto \u05f3invoice.pdf\u05f3', 'ltr .exe']), 'queue: ' + queued);
		p.xhr(spoof).sent();
		p.xhr(spoof).respond(201, {name: '\u05f3invoice.pdf\u05f3 (1).exe', renamed: true});
		const saved = box(p, p.$('b', p.$('.q-status', p.item(spoof))));
		want(same(saved, ['ltr', 'auto \u05f3invoice.pdf\u05f3 (1)', 'ltr .exe']), 'Saved as: ' + saved);
		await p.tick(1000); // the rows reload from the JSON listing
		const row = p.$$('tbody tr').find(tr => p.$('a', tr).title === spoof), rebuilt = box(p, row && p.$('.nm', row));
		want(same(rebuilt, ['ltr', 'auto \u05f3invoice.pdf\u05f3', 'ltr .exe']), 'rebuilt row: ' + rebuilt);
		for (const e of p.$$('[dir=auto]')) want(!/\.(exe|txt)$/.test(e.textContent), 'a dir=auto box holds a whole name: ' + e.textContent);
	},

	// Once a file is saving, neither its own button nor "Cancel all" cancels
	// it: the server may already keep it.
	async 'cancel while saving'() {
		const p = load(), all = () => p.$$('.q-head button').find(b => b.textContent === 'Cancel all');
		p.pick('a1.txt', 'a2.txt', 'a3.txt');
		want(p.xhrs.length === 2 && !all().hidden, 'two uploads send and Cancel all shows');
		p.xhr('a1.txt').sent();
		want(p.status('a1.txt') === 'Saving\u2026' && p.button('a1.txt').hidden, 'a1.txt offers a cancel while ' + p.status('a1.txt'));
		all().click();
		want(!p.xhr('a1.txt').aborted && p.xhr('a2.txt').aborted && p.xhrs.length === 2, 'Cancel all aborted the saving upload, or missed the sending one');
		want(p.status('a2.txt') === 'Canceled' && p.status('a3.txt') === 'Canceled', 'after Cancel all: ' + p.status('a2.txt') + ', ' + p.status('a3.txt'));
		want(all().hidden, 'Cancel all shows while the only upload left is saving');
		p.button('a1.txt').click();
		want(!p.xhr('a1.txt').aborted, "the saving file's own button aborted it");
		p.xhr('a1.txt').respond(201, {name: 'a1.txt'});
		want(p.status('a1.txt') === 'Saved', 'a1.txt: ' + p.status('a1.txt'));
		p.pick('b1.txt');
		p.xhr('b1.txt').sent();
		want(all().hidden, 'Cancel all still shows once the only upload is saving');
	},

	// After a 401 stopped the queue, files added later are sent (the visitor
	// may have signed in again in another tab) instead of waiting forever,
	// and once one is saved the session notice goes away. Another 401 stops
	// the queue again.
	async 'session ended'() {
		const p = load();
		p.pick('one.txt');
		p.xhr('one.txt').sent();
		p.xhr('one.txt').respond(401, {});
		want(p.status('one.txt') === 'Failed: your session ended' && p.$('[data-session]'), 'one.txt after a 401: ' + p.status('one.txt'));
		p.pick('two.txt');
		if (!p.xhr('two.txt')) return want(false, 'two.txt, added after the 401, is not sent: ' + p.status('two.txt'));
		p.xhr('two.txt').sent();
		p.xhr('two.txt').respond(201, {name: 'two.txt'});
		want(p.status('two.txt') === 'Saved' && !p.$('[data-session]'), 'two.txt: ' + p.status('two.txt') + ', session notice ' + !!p.$('[data-session]'));
		p.pick('three.txt', 'four.txt', 'five.txt');
		p.xhr('three.txt').respond(401, {});
		want(p.$('[data-session]') && p.status('five.txt') === 'Failed: your session ended' && !p.xhr('five.txt'),
			'after another 401: notice ' + !!p.$('[data-session]') + ', five.txt ' + p.status('five.txt'));
	},

	// "Saved as" names what the server kept whenever that differs from the
	// visitor's name, except for a change of Unicode form only.
	async renamed() {
		const p = load(), cases = [
			['Q3: results?.xlsx', {name: 'Q3_ results_.xlsx', renamed: false}, 'Saved as Q3_ results_.xlsx'],
			['cafe\u0301.txt', {name: 'caf\u00e9.txt', renamed: false}, 'Saved'],
			['b.txt', {name: 'b (1).txt', renamed: true}, 'Saved as b (1).txt'],
			['-rf.txt', {name: '_-rf.txt', renamed: false}, 'Saved as _-rf.txt'],
			['plain.txt', {name: 'plain.txt', renamed: false}, 'Saved'],
			['x\u202egnp.exe', {name: 'xgnp.exe', renamed: false}, 'Saved as xgnp.exe'],
		];
		p.pick(...cases.map(c => c[0]));
		cases.forEach(([name, answer, text], i) => {
			p.xhr(name).sent();
			p.xhr(name).respond(201, answer);
			want(p.status(i) === text, JSON.stringify(name) + ': ' + p.status(i) + ', want ' + text);
		});
	},

	// The error boundary takes the page's own errors only, and the page's
	// promise chains end in it.
	async 'error boundary'() {
		let p = load();
		for (const filename of ['chrome-extension://abcdef/content.js', 'https://cdn.example/x.js', '']) {
			p.win.dispatchEvent(new Event('error', {filename, message: 'Script error.'}));
			want(!p.basic(), 'an error from ' + (filename || 'nowhere') + ' switched to basic mode');
		}
		p.win.dispatchEvent(new Event('unhandledrejection', {reason: new Error('not ours')}));
		want(!p.basic(), 'a stray promise rejection switched to basic mode');
		p.win.dispatchEvent(new Event('error', {filename: PAGE, message: 'TypeError'}));
		want(p.basic(), "the page's own error left the page enhanced");

		p = load({listing: '{}'}); // a listing the rows can't be built from
		p.pick('x.txt');
		p.xhr('x.txt').sent();
		p.xhr('x.txt').respond(201, {name: 'x.txt'});
		await p.tick(1000);
		want(p.basic(), 'a failed reload of the rows after uploads left the page enhanced');

		p = load({listing: '{}', truncated: true});
		const fq = p.$('[data-filter]');
		fq.value = 'a';
		fq.dispatchEvent(new Event('input'));
		await p.tick(10);
		want(p.basic(), 'a failed listing for the filter left the page enhanced');
	},

	// Rebuilding the rows from JSON gives the focus back to the same control
	// of the same row; when that row is gone, to the list.
	async focus() {
		const p = load(), row = href => p.$$('tbody tr').find(tr => p.$('a', tr).getAttribute('href') === href);
		const upload = async name => {
			p.pick(name);
			p.xhr(name).sent();
			p.xhr(name).respond(201, {name});
			await p.tick(1000);
		};
		const copy = p.$('button', row('/inbox/b.txt'));
		copy.focus();
		await upload('c.txt');
		const now = p.doc.activeElement;
		want(now !== copy && now.isConnected && now.getAttribute('aria-label') === 'Copy link to b.txt',
			'after the reload the focus is on ' + now.tagName + ' ' + (now.getAttribute('aria-label') || ''));
		p.$('a', row('/inbox/a.txt')).focus();
		await upload('d.txt');
		want(p.doc.activeElement === p.$('a', row('/inbox/a.txt')), 'after the reload the focus is on ' + p.doc.activeElement.tagName + ', not the link to a.txt');
		p.$('a', row('/inbox/a.txt')).focus();
		const j = JSON.parse(listing);
		j.entries = j.entries.filter(e => e.name !== 'a.txt');
		p.win.fetch = async () => ({status: 200, ok: true, json: async () => j});
		await upload('e.txt');
		want(p.doc.activeElement === p.$('#files'), 'with its row gone the focus is on ' + p.doc.activeElement.tagName + ', not the list');
	},
};

if (!scenarios[scenario]) {
	console.log('no scenario ' + JSON.stringify(scenario));
	process.exit(2);
}
await scenarios[scenario]();
await settle();
if (fails.length) {
	console.log(fails.join('\n'));
	process.exit(1);
}
