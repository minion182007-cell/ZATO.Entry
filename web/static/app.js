'use strict';

const CITY = 'sarov';
const $ = (id) => document.getElementById(id);

// h создаёт DOM-узел; текст всегда вставляется как текст, не как HTML.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (v !== false && v != null) el.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c != null && c !== false) el.append(c.nodeType ? c : String(c));
  }
  return el;
}

async function api(path, body) {
  const res = await fetch('/api/v1' + path, body === undefined ? undefined : {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || 'Ошибка запроса');
  return data;
}

const fmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });
const fmtShort = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: '2-digit', year: '2-digit' });
const date = (s) => new Date(s + 'T00:00:00Z');
const long = (s) => fmt.format(date(s));
const short = (s) => fmtShort.format(date(s));
const todayISO = () => new Date().toISOString().slice(0, 10);

let profile;
const state = { scenario: 'business', answers: {}, overrides: {} };

// ---------- Вкладки ----------
document.querySelectorAll('.tabs button').forEach((btn) => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tabs button').forEach((b) => b.setAttribute('aria-selected', b === btn));
    for (const t of ['plan', 'check', 'renew']) $('tab-' + t).hidden = t !== btn.dataset.tab;
  });
});

// ---------- Дорожная карта ----------
function scenario() {
  return profile.scenarios.find((s) => s.id === state.scenario);
}

function renderScenarioControls() {
  const sc = scenario();
  $('scenarios').replaceChildren(...profile.scenarios.map((s) =>
    h('button', {
      type: 'button', role: 'radio', 'aria-checked': s.id === sc.id,
      onclick: () => selectScenario(s.id),
    }, s.title)));
  $('scenario-desc').textContent = sc.description;

  state.answers = {};
  $('questions').replaceChildren(h('legend', null, 'Анкета'), ...sc.questions.map((q) => {
    state.answers[q.id] = q.default;
    if (q.type === 'bool') {
      return h('label', { class: 'check' },
        h('input', {
          type: 'checkbox', checked: q.default ? '' : false,
          onchange: (e) => { state.answers[q.id] = e.target.checked; buildPlan(); },
        }), q.label);
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
  if (!profile.scenarios.some((s) => s.id === id)) return;
  state.scenario = id;
  if (location.hash !== '#' + id) history.replaceState(null, '', '#' + id);
  renderScenarioControls();
  buildPlan();
}

let planSeq = 0;
async function buildPlan() {
  const seq = ++planSeq;
  try {
    const plan = await api('/plans', {
      city: CITY, scenario: state.scenario, start_date: $('start-date').value,
      answers: state.answers, overrides: state.overrides,
    });
    if (seq === planSeq) renderPlan(plan);
  } catch (err) {
    if (seq === planSeq) $('hero').replaceChildren(h('p', { class: 'err' }, err.message));
  }
}

function renderPlan(plan) {
  const goal = plan.steps.find((s) => s.id === scenario().goal);
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

// ---------- Предпроверка пакета ----------
const SAMPLE_PERSONS = [
  {
    full_name: 'Тестов Тест Тестович', birth_date: '1990-05-10', citizenship: 'РФ',
    passport_series: '0000', passport_number: '000001', passport_issued: '2015-06-01',
    registration_address: 'г. Тестовый, ул. Примерная, д. 1', employer: 'ООО «Пример»',
    position: 'Монтажник', purpose: 'Работы по договору',
    documents: ['passport_copy', 'questionnaire', 'petition', 'employment_proof'],
  },
  {
    full_name: 'Образцова Анна Петровна', birth_date: '1981-02-01', citizenship: 'РФ',
    passport_series: '0000', passport_number: '00002', passport_issued: '2001-03-01',
    registration_address: 'г. Тестовый, ул. Примерная, д. 2', employer: 'ООО «Пример»',
    position: '', purpose: 'Работы по договору',
    documents: ['passport_copy', 'questionnaire'],
  },
];

$('check-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const box = $('check-result');
  try {
    const persons = JSON.parse($('persons').value);
    const rep = await api('/passes/validate', {
      city: CITY, pass_type: $('pass-type').value, entry_date: $('entry-date').value, persons,
    });
    box.replaceChildren(h('div', { class: 'card' },
      h('div', { class: 'status ' + (rep.ok ? 'ok' : 'bad') },
        rep.ok ? 'Пакет готов к подаче' : 'Пакет вернут — исправьте ошибки'),
      h('p', { class: 'muted' },
        `Готовы: ${rep.persons_ready} из ${rep.persons_checked} · ошибок: ${rep.errors} · предупреждений: ${rep.warnings} · ` +
        `реалистичная дата въезда при подаче сегодня: ${long(rep.realistic_entry_date)}`),
      ...rep.issues.map((i) => h('div', { class: 'issue ' + i.severity },
        h('b', null, i.person >= 0 ? `Человек №${i.person + 1}. ` : 'Пакет. '), i.message))));
  } catch (err) {
    box.replaceChildren(h('p', { class: 'err' }, err instanceof SyntaxError ? 'Пакет: некорректный JSON.' : err.message));
  }
});

// ---------- Продления ----------
const STATUS = { planned: 'по плану', due: 'пора подавать', overdue: 'срок подачи пропущен', expired: 'пропуск истёк' };

function staffFromText() {
  return $('staff').value.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => {
    const [id, expires] = l.split(';').map((p) => p.trim());
    return { id, pass_expires: expires || '' };
  });
}

$('renew-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const box = $('renew-result');
  try {
    const cal = await api('/renewals/calendar', { city: CITY, staff: staffFromText() });
    box.replaceChildren(
      h('div', { class: 'card' },
        h('div', { class: 'status' }, `Пакетов к подаче: ${cal.batches.length}`),
        h('p', { class: 'muted' }, `Пакет подаётся за ${cal.submit_before_days} дн. до окончания пропуска. ` +
          Object.entries(cal.counts).filter(([, n]) => n).map(([k, n]) => `${STATUS[k]}: ${n}`).join(' · '))),
      h('div', { class: 'steps' }, h('table', null,
        h('thead', null, h('tr', null, ...['Сотрудник', 'Пропуск до', 'Подать до', 'Осталось', 'Статус'].map((t) => h('th', null, t)))),
        h('tbody', null, cal.items.map((it) => h('tr', null,
          h('td', null, it.name || it.id),
          h('td', { class: 'num' }, short(it.pass_expires)),
          h('td', { class: 'num' }, short(it.submit_by)),
          h('td', { class: 'num' }, `${it.days_to_submit} дн.`),
          h('td', { class: 'st-' + it.status }, STATUS[it.status])))))));
  } catch (err) {
    box.replaceChildren(h('p', { class: 'err' }, err.message));
  }
});

$('ics').addEventListener('click', async () => {
  const res = await fetch('/api/v1/renewals/calendar?format=ics', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ city: CITY, staff: staffFromText() }),
  });
  if (!res.ok) {
    $('renew-result').replaceChildren(h('p', { class: 'err' }, (await res.json()).error));
    return;
  }
  const a = h('a', { href: URL.createObjectURL(await res.blob()), download: 'renewals.ics' });
  a.click();
  URL.revokeObjectURL(a.href);
});

function addDays(n) {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

// ---------- Старт ----------
(async function init() {
  try {
    profile = await api('/profiles/' + CITY);
  } catch (err) {
    $('hero').replaceChildren(h('p', { class: 'err' }, err.message));
    return;
  }
  $('city').textContent = profile.name;
  $('disclaimer').textContent = profile.disclaimer;
  $('start-date').value = todayISO();
  $('start-date').addEventListener('change', buildPlan);
  $('show-assumptions').addEventListener('change', (e) => document.body.classList.toggle('assumptions', e.target.checked));

  $('pass-type').replaceChildren(...profile.pass_types.map((p) => h('option', { value: p.id }, p.title)));
  $('pass-type').value = 'work';
  $('entry-date').value = addDays(40);
  $('persons').value = JSON.stringify(SAMPLE_PERSONS, null, 2);
  $('staff').value = [['С-001', 200], ['С-002', 52], ['С-003', 50], ['С-004', 30], ['С-005', -5]]
    .map(([id, n]) => `${id}; ${addDays(n)}`).join('\n');

  const fromHash = () => {
    const id = location.hash.slice(1);
    selectScenario(profile.scenarios.some((s) => s.id === id) ? id : state.scenario);
  };
  window.addEventListener('hashchange', fromHash);
  fromHash();
})();
