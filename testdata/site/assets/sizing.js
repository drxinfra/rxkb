/* Игрушечная модель для тестов rxkb: тот же интерфейс RXSizing, что у калькулятора сайта,
   формулы простые. users = 99999 зацикливает расчёт: так проверяется прерывание по времени. */
(function (root) {
  'use strict';
  var VERSIONS = ['2.0', '1.0'];
  var DEFAULTS = { ver: '2.0', users: 300, years: 5, ha: 0, test: 1, os: 'linux' };
  function even(v) { return Math.ceil(v / 2 - 1e-9) * 2; }

  function contour(kind, title, U, inp) {
    var n = inp.ha && kind === 'prod' ? 2 : 1;
    var nodes = [
      { role: 'Сервер приложений', n: n, cpu: even(2 + U / 100), ram: even(8 + U / 50), disk: [{ name: 'ОС', gb: 60 }], sw: 'RX ' + inp.ver, note: '' },
      { role: 'СУБД', n: n, cpu: 4, ram: 8, disk: [{ name: 'ОС', gb: 60 }, { name: 'данные', gb: 100 * inp.years / 5 }], sw: 'PostgreSQL', note: '' }
    ];
    var tot = { vms: 0, cpu: 0, ram: 0, disk: 0 };
    nodes.forEach(function (x) {
      x.diskTotal = x.disk.reduce(function (s, d) { return s + d.gb; }, 0);
      tot.vms += x.n; tot.cpu += x.n * x.cpu; tot.ram += x.n * x.ram; tot.disk += x.n * x.diskTotal;
    });
    return { kind: kind, title: title, U: U, T: U, M: 0, ha: !!(inp.ha && kind === 'prod'), nodes: nodes, k8sQuota: null, totals: tot,
      backupGB: 50, dbGB: 20, storeGB: 30, logs: { perDay: 0.5, days: 14, gb: 10, metricsGb: 10 }, notes: [] };
  }

  function compute(inp) {
    if (inp.users === 99999) { for (;;) { /* бесконечный цикл для теста прерывания */ } }
    var U = Math.max(5, inp.users | 0);
    var cs = [contour('prod', 'Продуктивный контур', U, inp)];
    if (inp.test) cs.push(contour('test', 'Тестовый контур', Math.max(10, Math.round(U * 0.2)), inp));
    var g = { vms: 0, cpu: 0, ram: 0, disk: 0, backup: 0 };
    cs.forEach(function (c) { g.vms += c.totals.vms; g.cpu += c.totals.cpu; g.ram += c.totals.ram; g.disk += c.totals.disk; g.backup += c.backupGB; });
    var w = [{ lvl: 'info', text: 'Тестовое замечание.' }];
    if (inp.os === 'windows') w.push({ lvl: 'warn', text: 'Windows лицензируется отдельно.' });
    return { inp: inp, d: { U: U, T: U, M: 0, storeGB: 30, dbGB: 20, docsTotal: 1000 }, contours: cs, grand: g, warnings: w };
  }

  function toText(res) {
    var L = ['Тестовый сайзинг ' + res.inp.ver];
    res.contours.forEach(function (c) { L.push(c.title + ': ' + c.totals.vms + ' ВМ, ' + c.totals.cpu + ' vCPU'); });
    L.push('');
    L.push('Расчёт: ' + (root.location ? root.location.href : 'drxinfra.ru/sizing'));
    return L.join('\n');
  }

  var KEYS = Object.keys(DEFAULTS);
  function encode(inp) {
    return KEYS.filter(function (k) { return String(inp[k]) !== String(DEFAULTS[k]); })
      .map(function (k) { return k + '=' + encodeURIComponent(inp[k]); }).join('&');
  }
  function decode(hash) {
    var inp = {}; KEYS.forEach(function (k) { inp[k] = DEFAULTS[k]; });
    (hash || '').replace(/^#/, '').split('&').forEach(function (kv) {
      var p = kv.split('='); if (p.length !== 2 || KEYS.indexOf(p[0]) < 0) return;
      inp[p[0]] = typeof DEFAULTS[p[0]] === 'number' ? (parseFloat(decodeURIComponent(p[1])) || 0) : decodeURIComponent(p[1]);
    });
    return inp;
  }

  var api = { VERSIONS: VERSIONS, DEFAULTS: DEFAULTS, compute: compute, toText: toText, encode: encode, decode: decode };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  root.RXSizing = api;
  if (typeof document === 'undefined') return;
})(typeof window !== 'undefined' ? window : this);
