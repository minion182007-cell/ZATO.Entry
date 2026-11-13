'use strict';

const CITY = 'sarov';
const TOKEN_KEY = 'zato.token';
const $ = (id) => document.getElementById(id);

// h создаёт DOM-узел; текст всегда вставляется как текст, не как HTML.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === 'class') el.className = v;
    else if (k === 'style') el.style.cssText = v;
    else if (k === 'value') el.value = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c != null && c !== false) el.append(c.nodeType ? c : String(c));
  }
  return el;
}

// Ключ доступа хранится в браузере; хранилище может быть недоступно.
const session = {
  get token() { try { return localStorage.getItem(TOKEN_KEY) || ''; } catch { return this._t || ''; } },
  set token(v) {
    this._t = v;
    try { v ? localStorage.setItem(TOKEN_KEY, v) : localStorage.removeItem(TOKEN_KEY); } catch { /* только на время сеанса */ }
  },
};

async function api(path, { method, body, auth } = {}) {
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (auth && session.token) headers.Authorization = 'Bearer ' + session.token;
  const res = await fetch('/api/v1' + path, {
    method: method || (body === undefined ? 'GET' : 'POST'),
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || 'Ошибка запроса');
    err.status = res.status;
    throw err;
  }
  return data;
}

const fmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' });
const fmtShort = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: '2-digit', year: '2-digit', timeZone: 'UTC' });
const date = (s) => new Date(s + 'T00:00:00Z');
const long = (s) => fmt.format(date(s));
const short = (s) => fmtShort.format(date(s));

function localISO(offsetDays = 0) {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

let profile;
const state = { scenario: 'business', answers: {}, overrides: {} };

// ---------- Вкладки ----------
function showTab(name) {
  document.querySelectorAll('.tabs button').forEach((b) => b.setAttribute('aria-selected', b.dataset.tab === name));
  for (const t of ['plan', 'check', 'cabinet']) $('tab-' + t).hidden = t !== name;
}
document.querySelectorAll('.tabs button').forEach((btn) => btn.addEventListener('click', () => showTab(btn.dataset.tab)));

// ---------- Дорожная карта ----------
const scenarioOf = (id) => profile.scenarios.find((s) => s.id === id);

function renderScenarioControls() {
  const sc = scenarioOf(state.scenario);
  $('scenarios').replaceChildren(...profile.scenarios.map((s) =>
    h('button', {
      type: 'button', role: 'radio', 'aria-checked': String(s.id === sc.id),
      onclick: () => selectScenario(s.id),
    }, s.title)));
  $('scenario-desc').textContent = sc.description;

  state.answers = {};
  $('questions').replaceChildren(h('legend', null, 'Анкета'), ...sc.questions.map((q) => {
    state.answers[q.id] = q.default;
    if (q.type === 'bool') {
      const box = h('input', { type: 'checkbox', onchange: (e) => { state.answers[q.id] = e.target.checked; buildPlan(); } });
      box.checked = Boolean(q.default);
      return h('label', { class: 'check' }, box, q.label);
    }
    return h('label', { class: 'field' }, q.label,
      h('input', {
        type: 'number', min: 0, max: 1000, value: q.default,
        onchange: (e) => { state.answers[q.id] = Number(e.target.value); buildPlan(); },
      }));
  }));

  state.overrides = {};
  const adjustable = sc.steps.filter((s) => s.adjustable);
  $('sliders').replaceChildren(h('legend', null, 'Сроки задаёт эксперт'), ...adjustable.map((s) => {
    const out = h('output', null, s.days + ' дн.');
    const lo = s.min_days ?? s.days, hi = s.max_days ?? s.days;
    return h('label', { class: 'slider' },
      h('span', null, s.title + ': ', out),
      h('input', {
        type: 'range', min: Math.max(1, Math.floor(lo / 2)), max: hi * 2, value: s.days,
        oninput: (e) => {
          out.textContent = e.target.value + ' дн.';
          state.overrides[s.id] = Number(e.target.value);
          buildPlan();
        },
      }));
  }));
}

function selectScenario(id) {
  state.scenario = id;
  if (location.hash !== '#' + id) history.replaceState(null, '', '#' + id);
  renderScenarioControls();
  buildPlan();
}

function planRequest() {
  return {
    city: CITY, scenario: state.scenario, start_date: $('start-date').value,
    answers: state.answers, overrides: state.overrides,
  };
}

let planSeq = 0;
async function buildPlan() {
  const seq = ++planSeq;
  $('plan-notice').hidden = true;
  $('plan-saved').replaceChildren();
  try {
    const plan = await api('/plans', { body: planRequest() });
    if (seq === planSeq) renderPlan(plan);
  } catch (err) {
    if (seq === planSeq) $('hero').replaceChildren(h('p', { class: 'err' }, err.message));
  }
}

function renderPlan(plan) {
  const goal = plan.steps.find((s) => s.id === scenarioOf(plan.scenario)?.goal) || plan.steps[plan.steps.length - 1];
  $('hero').replaceChildren(
    h('div', { class: 'label' }, goal.title + ' — расчётная дата'),
    h('div', { class: 'date' }, long(plan.target_date)),
    h('div', { class: 'range' },
      plan.earliest_date === plan.latest_date
        ? `Через ${plan.total_days} дн. от старта`
        : `Вилка: ${long(plan.earliest_date)} — ${long(plan.latest_date)} · ${plan.total_days} дн. от старта`,
      plan.assumptions ? ` · допущений в расчёте: ${plan.assumptions}` : ' · все сроки заданы экспертом'));

  const first = plan.steps.find((s) => s.id === plan.first_action);
  $('first-action').replaceChildren(...(first ? [
    h('b', null, `Первое действие: ${first.title}`),
    `Срок — ${long(first.end)}` + (first.description ? ` · ${first.description}` : ''),
  ] : []));

  const t0 = date(plan.start_date).getTime();
  const span = Math.max(1, Math.max(...plan.steps.map((s) => date(s.end).getTime())) - t0 + 864e5);
  const rows = plan.steps.map((s) => {
    const left = (date(s.start).getTime() - t0) / span * 100;
    const width = s.days === 0 ? 0 : (date(s.end).getTime() - date(s.start).getTime() + 864e5) / span * 100;
    return h('tr', { class: [s.critical && 'critical', s.assumption && !s.overridden && 'assumption'].filter(Boolean).join(' ') },
      h('td', null, s.title,
        s.critical && h('span', { class: 'tag crit' }, 'критический путь'),
        s.refusal_point && h('span', { class: 'tag' }, 'возможен отказ'),
        s.assumption && !s.overridden && h('span', { class: 'tag assume', title: s.note || '' }, 'допущение'),
        h('div', { class: 'owner' }, s.owner_title || '')),
      h('td', { class: 'num' }, s.days === 0 ? 'веха' : `${s.days} ${s.calendar === 'working' ? 'раб. дн.' : 'дн.'}`),
      h('td', { class: 'num' }, s.days === 0 ? short(s.start) : `${short(s.start)} – ${short(s.end)}`),
      h('td', { class: 'num' }, s.critical ? '—' : `${s.slack_days} дн.`),
      h('td', { class: 'gantt' }, h('div', { class: 'track' },
        h('div', { class: 'bar', style: `left:${left}%;width:${width}%` }))));
  });
  $('steps').replaceChildren(h('table', null,
    h('thead', null, h('tr', null, ...['Шаг', 'Срок', 'Даты', 'Резерв', 'График'].map((t) => h('th', null, t)))),
    h('tbody', null, rows)));
  $('limits').replaceChildren(...plan.limits.map((l) => h('li', null, l)));
}

function copyField(value) {
  const input = h('input', { type: 'text', readonly: '', value, onfocus: (e) => e.target.select() });
  const btn = h('button', {
    type: 'button', class: 'ghost small',
    onclick: async () => {
      try { await navigator.clipboard.writeText(value); btn.textContent = 'Скопировано'; } catch { input.select(); }
    },
  }, 'Копировать');
  return h('div', { class: 'copy' }, input, btn);
}

$('plan-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const box = $('plan-saved');
  try {
    const plan = await api('/plans', { body: { ...planRequest(), save: true }, auth: true });
    box.replaceChildren(
      h('p', { class: 'muted' }, session.token
        ? 'План сохранён в кабинете. Ссылка для коллег:'
        : 'План сохранён на 30 дней. Ссылка для коллег:'),
      copyField(`${location.origin}/#plan=${plan.id}`));
    if (session.token) loadCabinet();
  } catch (err) {
    box.replaceChildren(h('p', { class: 'err' }, err.message));
  }
});

async function openSavedPlan(id) {
  showTab('plan');
  try {
    const plan = await api('/plans/' + encodeURIComponent(id));
    planSeq++;
    renderPlan(plan);
    const note = $('plan-notice');
    note.hidden = false;
    note.replaceChildren(
      `Сохранённый план «${plan.title}», старт ${short(plan.start_date)} · `,
      h('a', { href: '#' + plan.scenario }, 'Построить новый расчёт'));
  } catch (err) {
    $('hero').replaceChildren(h('p', { class: 'err' }, err.message));
  }
}

// ---------- Предпроверка пакета ----------
const FIELDS = {
  full_name: ['ФИО', 'text'], birth_date: ['Дата рождения', 'date'], citizenship: ['Гражданство', 'text'],
  passport_series: ['Серия паспорта', 'text'], passport_number: ['Номер паспорта', 'text'],
  passport_issued: ['Дата выдачи паспорта', 'date'], registration_address: ['Адрес регистрации', 'text'],
  employer: ['Работодатель', 'text'], position: ['Должность', 'text'], purpose: ['Цель въезда', 'text'],
};

// Сгенерированные данные: первый человек проходит проверку, второй — нет.
const SAMPLE_PERSONS = [
  {
    full_name: 'Тестов Тест Тестович', birth_date: '1990-05-10', citizenship: 'РФ',
    passport_series: '0000', passport_number: '000001', passport_issued: '2015-06-01',
    registration_address: 'г. Тестовый, ул. Примерная, д. 1', employer: 'ООО «Пример»',
    position: 'Монтажник', purpose: 'Работы по договору',
    documents: ['passport_copy', 'questionnaire', 'petition', 'employment_proof', 'application'],
  },
  {
    full_name: 'Образцова Анна Петровна', birth_date: '1981-02-01', citizenship: 'РФ',
    passport_series: '0000', passport_number: '00002', passport_issued: '2001-03-01',
    registration_address: 'г. Тестовый, ул. Примерная, д. 2', employer: 'ООО «Пример»',
    position: '', purpose: 'Работы по договору',
    documents: ['passport_copy', 'questionnaire'],
  },
];

let persons = [{ documents: [] }];
let issues = [];

const passType = () => profile.pass_types.find((p) => p.id === $('pass-type').value);

function renderPersons() {
  const pt = passType();
  $('persons').replaceChildren(...persons.map((person, i) => {
    const mine = issues.filter((x) => x.person === i);
    const bad = new Set(mine.map((x) => x.field));
    return h('div', { class: 'card person' },
      h('div', { class: 'person-head' },
        h('b', null, `Человек №${i + 1}`),
        persons.length > 1 && h('button', {
          type: 'button', class: 'link',
          onclick: () => { persons.splice(i, 1); issues = []; renderPersons(); $('check-summary').replaceChildren(); },
        }, 'Убрать')),
      h('div', { class: 'grid' }, pt.required_fields.map((f) => {
        const [label, type] = FIELDS[f];
        return h('label', { class: 'field' + (bad.has(f) ? ' bad' : '') }, label,
          h('input', {
            type, autocomplete: 'off', value: person[f] || '',
            oninput: (e) => { person[f] = e.target.value; },
          }));
      })),
      h('div', { class: 'docs' }, pt.documents.map((d) => {
        const box = h('input', {
          type: 'checkbox',
          onchange: (e) => {
            person.documents = (person.documents || []).filter((x) => x !== d.id);
            if (e.target.checked) person.documents.push(d.id);
          },
        });
        box.checked = (person.documents || []).includes(d.id);
        return h('label', { class: 'check' + (bad.has(d.id) ? ' bad' : '') }, box, d.title + (d.required ? '' : ' (необязательно)'));
      })),
      mine.map((x) => h('div', { class: 'issue ' + x.severity }, x.message)));
  }));
}

$('add-person').addEventListener('click', () => { persons.push({ documents: [] }); renderPersons(); });
$('sample-persons').addEventListener('click', () => {
  persons = structuredClone(SAMPLE_PERSONS);
  issues = [];
  $('check-summary').replaceChildren();
  renderPersons();
});

$('check-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const box = $('check-summary');
  try {
    const rep = await api('/passes/validate', {
      body: { city: CITY, pass_type: $('pass-type').value, entry_date: $('entry-date').value, persons },
    });
    issues = rep.issues;
    box.replaceChildren(
      h('div', { class: 'status ' + (rep.ok ? 'ok' : 'bad') },
        rep.ok ? 'Пакет готов к подаче' : 'Пакет вернут — исправьте ошибки'),
      h('p', { class: 'muted' },
        `Готовы: ${rep.persons_ready} из ${rep.persons_checked} · ошибок: ${rep.errors} · предупреждений: ${rep.warnings}`),
      h('p', { class: 'muted' }, `Реалистичная дата въезда при подаче сегодня: ${long(rep.realistic_entry_date)}`),
      ...rep.issues.filter((x) => x.person < 0).map((x) => h('div', { class: 'issue ' + x.severity }, x.message)));
    renderPersons();
  } catch (err) {
    box.replaceChildren(h('p', { class: 'err' }, err.message));
  }
});

// ---------- Кабинет и продления ----------
const STATUS = { planned: 'по плану', due: 'пора подавать', overdue: 'срок подачи пропущен', expired: 'пропуск истёк' };
let freshToken = '';

function cabinetError(err) {
  if (err.status === 401) {
    session.token = '';
    renderLogin('Ключ доступа не подошёл.');
    return;
  }
  const box = $('cabinet-error');
  if (box) box.textContent = err.message;
}

function renderLogin(message) {
  const name = h('input', { type: 'text', maxlength: 200, required: '', placeholder: 'ООО «Пример»' });
  const key = h('input', { type: 'password', required: '', autocomplete: 'off', placeholder: 'zk_…' });
  const error = h('p', { class: 'err' }, message || '');
  $('cabinet').replaceChildren(h('div', { class: 'layout two' },
    h('form', {
      class: 'panel',
      onsubmit: async (e) => {
        e.preventDefault();
        try {
          const res = await api('/orgs', { body: { name: name.value, city: CITY } });
          session.token = res.token;
          freshToken = res.token;
          loadCabinet();
        } catch (err) { error.textContent = err.message; }
      },
    },
    h('b', null, 'Создать кабинет организации'),
    h('p', { class: 'muted' }, 'Кабинет хранит штат со сроками пропусков и сохранённые планы. Паспортные данные в нём не хранятся.'),
    h('label', { class: 'field' }, 'Название организации', name),
    h('button', { type: 'submit' }, 'Создать кабинет'),
    error),
    h('form', {
      class: 'panel',
      onsubmit: (e) => { e.preventDefault(); session.token = key.value.trim(); loadCabinet(); },
    },
    h('b', null, 'Войти по ключу доступа'),
    h('p', { class: 'muted' }, 'Ключ выдаётся при создании кабинета.'),
    h('label', { class: 'field' }, 'Ключ доступа', key),
    h('button', { type: 'submit' }, 'Войти'))));
}

async function loadCabinet() {
  if (!session.token) { renderLogin(); return; }
  try {
    const [org, cal, plans] = await Promise.all([
      api('/orgs/me', { auth: true }), api('/orgs/me/renewals', { auth: true }), api('/orgs/me/plans', { auth: true }),
    ]);
    renderCabinet(org, cal, plans);
  } catch (err) {
    if (err.status === 401) { session.token = ''; renderLogin('Ключ доступа не подошёл.'); } else renderLogin(err.message);
  }
}

async function staffAction(path, options) {
  try {
    await api(path, { ...options, auth: true });
    loadCabinet();
  } catch (err) { cabinetError(err); }
}

function parseImport(text) {
  return text.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => {
    const parts = l.split(';').map((p) => p.trim());
    return parts.length >= 3
      ? { id: parts[0], name: parts[1], pass_expires: parts[2] }
      : { id: parts[0], name: '', pass_expires: parts[1] || '' };
  });
}

function renderCabinet(org, cal, plans) {
  const code = h('input', { type: 'text', maxlength: 64, required: '', placeholder: 'С-001' });
  const name = h('input', { type: 'text', maxlength: 200, placeholder: 'необязательно' });
  const expires = h('input', { type: 'date', required: '' });
  const bulk = h('textarea', { rows: 5, spellcheck: 'false', placeholder: 'С-002; Монтажник; 2027-01-04\nС-003; 2027-06-01' });
  const feedURL = `${location.origin}/api/v1/feeds/${org.feed_token}/renewals.ics`;

  const side = h('div', { class: 'panel' },
    h('div', { class: 'person-head' }, h('b', null, org.name),
      h('button', { type: 'button', class: 'link', onclick: () => { session.token = ''; freshToken = ''; renderLogin(); } }, 'Выйти')),
    freshToken && h('div', { class: 'notice' },
      h('b', null, 'Сохраните ключ доступа — он показывается один раз'), copyField(freshToken)),
    h('form', {
      class: 'stack',
      onsubmit: (e) => {
        e.preventDefault();
        staffAction('/orgs/me/staff', { body: [{ id: code.value, name: name.value, pass_expires: expires.value }] });
      },
    },
    h('b', null, 'Добавить сотрудника'),
    h('label', { class: 'field' }, 'Код', code),
    h('label', { class: 'field' }, 'Имя или должность', name),
    h('label', { class: 'field' }, 'Пропуск действует до', expires),
    h('button', { type: 'submit' }, 'Добавить')),
    h('form', {
      class: 'stack',
      onsubmit: (e) => { e.preventDefault(); staffAction('/orgs/me/staff', { body: parseImport(bulk.value) }); },
    },
    h('b', null, 'Загрузить списком'),
    h('p', { class: 'muted' }, 'По строке на сотрудника: код; имя; ГГГГ-ММ-ДД. Существующие коды обновляются.'),
    bulk,
    h('button', { type: 'submit', class: 'ghost' }, 'Загрузить')),
    h('p', { class: 'err', id: 'cabinet-error' }));

  const counts = Object.entries(cal.counts).filter(([, n]) => n).map(([k, n]) => `${STATUS[k]}: ${n}`).join(' · ');
  const main = h('div', { class: 'result' },
    h('div', { class: 'card' },
      h('div', { class: 'status' }, cal.items.length ? `Пакетов к подаче: ${cal.batches.length}` : 'Штат пока пуст'),
      h('p', { class: 'muted' }, `Пакет на продление подаётся за ${cal.submit_before_days} дн. до окончания пропуска.` + (counts ? ' ' + counts : ''))),
    cal.batches.length > 0 && h('div', { class: 'card' },
      h('b', null, 'Пакеты'),
      cal.batches.map((b) => h('div', { class: 'issue' },
        h('span', { class: 'st-' + b.status }, `до ${long(b.submit_by)}`), ` — ${b.staff.join(', ')}`))),
    cal.items.length > 0 && h('div', { class: 'steps' }, h('table', null,
      h('thead', null, h('tr', null, ...['Сотрудник', 'Пропуск до', 'Подать до', 'Осталось', 'Статус', ''].map((t) => h('th', null, t)))),
      h('tbody', null, cal.items.map((it) => h('tr', null,
        h('td', null, it.id, it.name && h('div', { class: 'owner' }, it.name)),
        h('td', { class: 'num' }, short(it.pass_expires)),
        h('td', { class: 'num' }, short(it.submit_by)),
        h('td', { class: 'num' }, `${it.days_to_submit} дн.`),
        h('td', { class: 'st-' + it.status }, STATUS[it.status]),
        h('td', { class: 'num' },
          h('button', {
            type: 'button', class: 'link', title: `Новый срок — ${long(it.next_expires)}`,
            onclick: () => staffAction(`/orgs/me/staff/${encodeURIComponent(it.id)}/renew`, { body: {} }),
          }, 'Продлён'),
          ' ',
          h('button', {
            type: 'button', class: 'link danger',
            onclick: () => staffAction(`/orgs/me/staff/${encodeURIComponent(it.id)}`, { method: 'DELETE' }),
          }, 'Удалить'))))))),
    h('div', { class: 'card' },
      h('b', null, 'Подписка на календарь'),
      h('p', { class: 'muted' }, 'Добавьте адрес в Outlook, Google Календарь или Яндекс Календарь — сроки подачи будут обновляться сами. Ссылка даёт доступ только на чтение.'),
      copyField(feedURL)),
    h('div', { class: 'card' },
      h('b', null, 'Сохранённые планы'),
      plans.length === 0
        ? h('p', { class: 'muted' }, 'Пока нет. Постройте дорожную карту и нажмите «Сохранить план».')
        : plans.map((p) => h('div', { class: 'issue' },
          h('a', { href: '#plan=' + p.id }, `${p.title}: ${long(p.target_date)}`),
          h('span', { class: 'muted inline' }, ` · старт ${short(p.start_date)}`)))));

  $('cabinet').replaceChildren(h('div', { class: 'layout' }, side, main));
  freshToken = '';
}

// ---------- Старт ----------
function route() {
  const hash = location.hash.slice(1);
  if (hash.startsWith('plan=')) { openSavedPlan(hash.slice(5)); return; }
  showTab('plan');
  selectScenario(scenarioOf(hash) ? hash : state.scenario);
}

(async function init() {
  try {
    profile = await api('/profiles/' + CITY);
  } catch (err) {
    $('hero').replaceChildren(h('p', { class: 'err' }, err.message));
    return;
  }
  $('city').textContent = profile.name;
  $('disclaimer').textContent = profile.disclaimer;
  $('start-date').value = localISO();
  $('start-date').addEventListener('change', buildPlan);
  $('show-assumptions').addEventListener('change', (e) => document.body.classList.toggle('assumptions', e.target.checked));

  $('pass-type').replaceChildren(...profile.pass_types.map((p) => h('option', { value: p.id }, p.title)));
  $('pass-type').value = 'work';
  $('pass-type').addEventListener('change', () => { issues = []; renderPersons(); });
  $('entry-date').value = localISO(40);
  renderPersons();

  renderScenarioControls();
  window.addEventListener('hashchange', route);
  route();
  loadCabinet();
})();
