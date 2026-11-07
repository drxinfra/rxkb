#!/usr/bin/env python3
"""Собирает базу знаний rxkb (kb.json) из исходников сайта drxinfra.ru.

Сервер rxkb ничего не читает из сети: всё, что он отвечает, лежит в одном файле kb.json.
Файл собирается из репозитория сайта при каждой выкладке и кладётся рядом с сервером.

Что берётся из сайта:
  errors/posts/*.md            справочник ошибок: шаблоны для журналов и разделы статьи
  blog/posts/*.md              статьи блога (черновики пропускаются, как на сайте)
  search.json                  индекс поиска по сайту (его собирает build.py сайта)
  assets/sizing.js             модель калькулятора сайзинга, сервер выполняет её как есть
  assets/aisizing-data.js      замеры для калькулятора железа под ИИ; формулы из assets/aisizing.js
                               повторены в коде сервера
  assets/{aisizing,errors,search,seccheck,upgrade}.js
                               только суммы: логику этих скриптов сервер повторяет сам и по суммам
                               замечает, что сайт стал считать иначе
  ai-sizing.html               значения формы калькулятора ИИ по умолчанию
  rx-linux-guide.html          таблица версий компонентов
  upgrade-checklist.html       чек-лист обновления
  security-check.html          вопросы самооценки безопасности с весами

  python3 hack/build-kb.py                         сайт лежит в ../landing рядом с репозиторием
  python3 hack/build-kb.py --site ПУТЬ --out ФАЙЛ  другой путь к сайту и к результату

Плейсхолдеры сайта подставляются так же, как в deploy/build_dist.sh сайта, из переменных
SITE_DOMAIN, TG_USERNAME и CONTACT_EMAIL (по умолчанию значения drxinfra.ru).
Только стандартная библиотека Python 3.8+.
"""
import argparse
import hashlib
import html
import html.parser
import io
import json
import os
import re
import subprocess
import sys
import urllib.parse

FORMAT = 1
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Синонимы тегов, как TAG_ALIASES в build.py сайта.
TAG_ALIASES = {"бэкапы": "резервное копирование", "информационная безопасность": "безопасность"}
ERROR_SECTIONS = {"Что это значит": "meaning", "Как проверить": "check", "Что сделать": "fix"}
# Неподставленных плейсхолдеров в базе быть не должно. Свои запрещённые слова (то, что сборка
# сайта не пускает в dist) можно добавить регуляркой в переменной KB_FORBIDDEN: в публичной
# репе их нет.
FORBIDDEN = re.compile(r"PLACEHOLDER|domain\.example" + ("|" + os.environ["KB_FORBIDDEN"] if os.environ.get("KB_FORBIDDEN") else ""), re.I)


class Fail(Exception):
    pass


# ---------------------------------------------------------------- markdown


def front_matter(path):
    """Фронт-маттер и тело, как их читает build.py сайта."""
    with io.open(path, encoding="utf-8") as f:
        text = f.read().replace("\r\n", "\n")
    m = re.match(r"^---\s*\n(.*?)\n---\s*\n(.*)$", text, re.S)
    if not m:
        raise Fail("%s: нет фронт-маттера ---" % path)
    meta = {}
    for line in m.group(1).splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            meta[k.strip().lower()] = v.strip()
    return meta, m.group(2)


def sections(body):
    """Разделы второго уровня: {заголовок: markdown}."""
    out, name, buf = {}, None, []
    for line in body.split("\n"):
        h = re.match(r"^##\s+(.+?)\s*$", line)
        if h and not line.startswith("###"):
            if name is not None:
                out[name] = "\n".join(buf).strip()
            name, buf = h.group(1), []
        elif name is not None:
            buf.append(line)
    if name is not None:
        out[name] = "\n".join(buf).strip()
    return out


def absolute_links(md, base):
    """Относительные ссылки статьи в полные адреса сайта: агент отдаёт их человеку как есть."""
    def fix(target):
        t = target.strip()
        if not t or re.match(r"^[a-z][a-z0-9+.-]*:", t, re.I):
            return target
        return urllib.parse.urljoin(base, t)

    md = re.sub(r"(\]\()([^)\s]+)(\))", lambda m: m.group(1) + fix(m.group(2)) + m.group(3), md)
    md = re.sub(r'(href=")([^"]+)(")', lambda m: m.group(1) + fix(m.group(2)) + m.group(3), md)
    return md


# ---------------------------------------------------------------- html


class Node(object):
    __slots__ = ("tag", "attrs", "children", "parent")

    def __init__(self, tag, attrs, parent):
        self.tag, self.attrs, self.children, self.parent = tag, attrs, [], parent

    def classes(self):
        return set((self.attrs.get("class") or "").split())

    def walk(self):
        for c in self.children:
            if isinstance(c, Node):
                yield c
                for d in c.walk():
                    yield d

    def find_all(self, tag=None, cls=None, **attrs):
        out = []
        for n in self.walk():
            if tag and n.tag != tag:
                continue
            if cls and cls not in n.classes():
                continue
            if any(n.attrs.get(k) != v for k, v in attrs.items()):
                continue
            out.append(n)
        return out

    def find(self, tag=None, cls=None, **attrs):
        r = self.find_all(tag, cls, **attrs)
        return r[0] if r else None

    def text(self):
        parts = []
        for c in self.children:
            parts.append(c.text() if isinstance(c, Node) else c)
        return "".join(parts)

    def first_text(self):
        """Как firstChild.textContent в браузере: текст до первого вложенного элемента."""
        if not self.children:
            return ""
        c = self.children[0]
        return c.text() if isinstance(c, Node) else c


class Tree(html.parser.HTMLParser):
    VOID = {"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr"}

    def __init__(self):
        html.parser.HTMLParser.__init__(self, convert_charrefs=True)
        self.root = Node("#root", {}, None)
        self.cur = self.root

    def handle_starttag(self, tag, attrs):
        n = Node(tag, dict((k, v if v is not None else "") for k, v in attrs), self.cur)
        self.cur.children.append(n)
        if tag not in self.VOID:
            self.cur = n

    def handle_startendtag(self, tag, attrs):
        self.cur.children.append(Node(tag, dict((k, v if v is not None else "") for k, v in attrs), self.cur))

    def handle_endtag(self, tag):
        n = self.cur
        while n is not None and n.tag != tag:
            n = n.parent
        if n is not None and n.parent is not None:
            self.cur = n.parent

    def handle_data(self, data):
        self.cur.children.append(data)


def parse_html(path):
    t = Tree()
    with io.open(path, encoding="utf-8") as f:
        t.feed(f.read())
    t.close()
    return t.root


def clean(s):
    return re.sub(r"\s+", " ", s).strip()


# ---------------------------------------------------------------- части базы


def build_errors(site, url):
    posts = os.path.join(site, "errors", "posts")
    out = []
    for name in sorted(os.listdir(posts)):
        if not name.endswith(".md") or name.startswith("_"):
            continue
        meta, body = front_matter(os.path.join(posts, name))
        for k in ("title", "what", "component", "date", "description", "match"):
            if not meta.get(k):
                raise Fail("errors/%s: нет поля %s" % (name, k))
        slug = name[:-3]
        page = url + "errors/" + slug + ".html"
        body = absolute_links(body.strip(), page)
        sec = sections(body)
        out.append({
            "id": slug, "title": meta["title"], "what": meta["what"], "component": meta["component"],
            "date": meta["date"], "description": meta["description"],
            "patterns": [p.strip() for p in meta["match"].split("||") if p.strip()],
            "sections": dict((ERROR_SECTIONS[k], v) for k, v in sec.items() if k in ERROR_SECTIONS),
            "markdown": body, "url": page,
        })
    if not out:
        raise Fail("в errors/posts нет ни одной ошибки")
    return out


def build_articles(site, url):
    posts = os.path.join(site, "blog", "posts")
    out = []
    for name in sorted(os.listdir(posts)):
        if not name.endswith(".md") or name.startswith("_"):
            continue
        meta, body = front_matter(os.path.join(posts, name))
        for k in ("title", "date", "description"):
            if not meta.get(k):
                raise Fail("blog/%s: нет поля %s" % (name, k))
        if meta.get("draft", "").lower() in ("true", "yes", "1"):
            continue
        tags, seen = [], set()
        for t in (x.strip() for x in meta.get("tags", "").split(",")):
            t = TAG_ALIASES.get(t.lower(), t)
            if t and t.lower() not in seen:
                seen.add(t.lower())
                tags.append(t)
        slug = name[:-3]
        page = url + "blog/" + slug + ".html"
        out.append({
            "id": slug, "title": meta["title"], "date": meta["date"], "updated": meta.get("updated") or meta["date"],
            "description": meta["description"], "tags": tags, "readtime": meta.get("readtime", ""),
            "markdown": absolute_links(body.strip(), page), "url": page,
        })
    out.sort(key=lambda a: a["date"], reverse=True)
    return out


def build_search(site, url):
    path = os.path.join(site, "search.json")
    with io.open(path, encoding="utf-8") as f:
        docs = json.load(f)
    out = []
    for d in docs:
        doc = dict((k, d.get(k, "")) for k in ("u", "k", "t", "d", "h", "x"))
        doc["u"] = urllib.parse.urljoin(url, doc["u"])
        out.append(doc)
    if not out:
        raise Fail("search.json пустой: соберите сайт (build.py)")
    return out


def sha256(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def label_of(node, cls):
    """Подпись поля формы: ближайший предок с классом fld и его <span class="l">."""
    n = node.parent
    while n is not None and "fld" not in n.classes():
        n = n.parent
    if n is None:
        return ""
    s = n.find("span", cls=cls)
    return clean(s.text()) if s is not None else ""


def build_sizing(site, url):
    path = os.path.join(site, "assets", "sizing.js")
    with io.open(path, encoding="utf-8") as f:
        script = f.read()
    if "RXSizing" not in script or "function compute" not in script:
        raise Fail("assets/sizing.js: не похоже на модель калькулятора (нет RXSizing и compute)")
    # Поля формы: какие значения у переключателей и какие поля флажки. Сервер строит по ним схему
    # входа инструмента, чтобы агент не передал того, чего нет на странице (ссылка на расчёт
    # тогда открыла бы на сайте другие цифры).
    root = parse_html(os.path.join(site, "sizing.html"))
    form = root.find("form", id="calc-form")
    if form is None:
        raise Fail("sizing.html: нет формы #calc-form")
    fields = {}
    for inp in form.find_all("input"):
        name, typ = inp.attrs.get("name"), inp.attrs.get("type")
        if not name:
            continue
        if typ == "number":
            unit = inp.parent.find("em") if inp.parent is not None else None
            fields[name] = {"kind": "number", "label": label_of(inp, "l"), "min": float(inp.attrs.get("min") or 0),
                            "unit": clean(unit.text()) if unit is not None else ""}
        elif typ == "radio":
            f = fields.setdefault(name, {"kind": "radio", "label": label_of(inp, "l"), "choices": []})
            span = inp.parent.find("span") if inp.parent is not None else None
            f["choices"].append({"value": inp.attrs.get("value", ""), "label": clean(span.text()) if span is not None else ""})
        elif typ == "checkbox":
            t = inp.parent.find("span", cls="t") if inp.parent is not None else None
            fields[name] = {"kind": "checkbox", "label": clean(t.first_text()) if t is not None else ""}
    for sel in form.find_all("select"):
        if sel.attrs.get("name"):
            fields[sel.attrs["name"]] = {"kind": "select", "label": label_of(sel, "l")}
    presets = [{"label": clean(b.text()), "hash": b.attrs.get("data-preset", "")} for b in root.find_all("button") if "data-preset" in b.attrs]
    return {"script": script, "sha256": sha256(path), "page": url + "sizing.html", "fields": fields, "presets": presets}


def build_aisizing(site, url):
    path = os.path.join(site, "assets", "aisizing-data.js")
    with io.open(path, encoding="utf-8") as f:
        text = f.read()
    m = re.search(r"window\.AISIZING\s*=\s*(\{.*\})\s*;?\s*$", text, re.S)
    if not m:
        raise Fail("assets/aisizing-data.js: нет window.AISIZING = {...}")
    data = json.loads(m.group(1))
    # Порядок ключей важен: первый сервер в списке выбран на странице по умолчанию.
    hw_order = list(data.get("hardware", {}).keys())
    model_order = list(data.get("models", {}).keys())
    root = parse_html(os.path.join(site, "ai-sizing.html"))
    form = root.find("form", id="ais-form")
    if form is None:
        raise Fail("ai-sizing.html: нет формы #ais-form")
    defaults, choices = {}, {}
    for inp in form.find_all("input"):
        name, typ = inp.attrs.get("name"), inp.attrs.get("type")
        if typ == "number" and name:
            defaults[name] = float(inp.attrs.get("value") or 0)
        elif typ == "radio" and name:
            label = inp.parent.find("span") if inp.parent is not None else None
            choices.setdefault(name, []).append({"value": inp.attrs.get("value", ""), "label": clean(label.text()) if label else ""})
            if "checked" in inp.attrs:
                defaults[name] = inp.attrs.get("value", "")
    for k in ("users", "share", "qph", "target", "toolset", "kv"):
        if k not in defaults:
            raise Fail("ai-sizing.html: в форме нет поля %s" % k)
    return {
        "data": data, "hardware_order": hw_order, "model_order": model_order, "defaults": defaults, "choices": choices,
        "page": url + "ai-sizing.html",
    }


def build_versions(site, url):
    root = parse_html(os.path.join(site, "rx-linux-guide.html"))
    sec = root.find("section", id="versions")
    if sec is None:
        raise Fail("rx-linux-guide.html: нет раздела #versions")
    table = sec.find("table")
    cols = [clean(th.text()) for th in table.find("thead").find_all("th")]
    rows = []
    for tr in table.find("tbody").find_all("tr"):
        cells = [clean(td.text()) for td in tr.find_all("td")]
        if len(cells) != len(cols):
            raise Fail("rx-linux-guide.html: в строке таблицы версий %d ячеек вместо %d" % (len(cells), len(cols)))
        rows.append({"component": cells[0], "values": cells[1:]})
    sub = sec.find("p", cls="sub")
    notes = [clean(p.text()) for p in sec.find_all("p", cls="aside-note")]
    return {"columns": cols[1:], "rows": rows, "source": clean(sub.text()) if sub else "", "notes": notes,
            "page": url + "rx-linux-guide.html#versions"}


def build_upgrade(site, url):
    root = parse_html(os.path.join(site, "upgrade-checklist.html"))
    form = root.find("form", id="ck-form")
    if form is None:
        raise Fail("upgrade-checklist.html: нет формы #ck-form")
    phases, n = [], 0
    for ph in form.find_all("fieldset", cls="phase"):
        head = clean(ph.find("h3").first_text())
        m = re.match(r"^(\d+)\s*·\s*(.+)$", head)
        intro = ph.find("p")
        items = []
        for ck in ph.find_all("label", cls="ck"):
            n += 1
            t = ck.find("span", cls="t")
            note = t.find("small")
            items.append({"n": n, "name": ck.find("input").attrs.get("name", ""), "text": clean(t.first_text()),
                          "note": clean(note.text()) if note else "", "optional": "opt" in ck.classes()})
        phases.append({"n": int(m.group(1)) if m else len(phases) + 1, "title": m.group(2) if m else head,
                       "intro": clean(intro.text()) if intro else "", "items": items})
    if not n:
        raise Fail("upgrade-checklist.html: нет пунктов")
    return {"phases": phases, "count": n, "page": url + "upgrade-checklist.html"}


def build_security(site, url):
    root = parse_html(os.path.join(site, "security-check.html"))
    form = root.find("form", id="sec-form")
    if form is None:
        raise Fail("security-check.html: нет формы #sec-form")
    labels = {}
    qs = []
    for grp in form.find_all("fieldset", cls="qgroup"):
        sev = grp.attrs.get("data-sev", "")
        pill = grp.find("span", cls="pill")
        if pill is not None:
            labels[sev] = clean(pill.text())
        for q in grp.find_all("div", cls="q"):
            href = q.attrs.get("data-href", "")
            qs.append({
                "id": q.attrs.get("id", ""), "severity": sev, "weight": float(q.attrs.get("data-w") or 0),
                "text": clean(q.find("p", cls="qt").text()), "hint": clean(q.find("p", cls="qh").text()) if q.find("p", cls="qh") else "",
                "fix": q.attrs.get("data-fix", ""), "ref": q.attrs.get("data-ref", ""),
                "href": urllib.parse.urljoin(url, href) if href else "",
            })
    if not qs:
        raise Fail("security-check.html: нет вопросов")
    return {"questions": qs, "severity_labels": labels, "page": url + "security-check.html"}


def build_ports(site):
    """Суммы скриптов сайта, логику которых сервер повторяет в своём коде.
    Сервер сверяет их со своими и предупреждает, если сайт считает уже иначе."""
    return dict((name, sha256(os.path.join(site, "assets", name)))
                for name in ("aisizing.js", "errors.js", "search.js", "seccheck.js", "upgrade.js"))


def site_commit(site):
    """Коммит сайта, из которого собрана база. Пусто, если каталог сайта не корень своей репы
    (например, тестовый сайт внутри репы rxkb): тогда коммит был бы чужой."""
    try:
        top = subprocess.run(["git", "-C", site, "rev-parse", "--show-toplevel"], capture_output=True, text=True, timeout=10)
        if os.path.realpath(top.stdout.strip() or "/nonexistent") != os.path.realpath(site):
            return ""
        out = subprocess.run(["git", "-C", site, "rev-parse", "--short=12", "HEAD"], capture_output=True, text=True, timeout=10)
        return out.stdout.strip()
    except Exception:
        return ""


def substitute(obj, repl):
    if isinstance(obj, str):
        for a, b in repl:
            obj = obj.replace(a, b)
        return obj
    if isinstance(obj, list):
        return [substitute(x, repl) for x in obj]
    if isinstance(obj, dict):
        return dict((k, substitute(v, repl)) for k, v in obj.items())
    return obj


def build(site, domain, telegram, email):
    url = "https://%s/" % domain
    # Порядок замен как в build_dist.sh сайта.
    repl = [("DOMAIN_PLACEHOLDER", domain), ("TELEGRAM_PLACEHOLDER", telegram), ("@telegram", "@" + telegram),
            ("mail@domain.example", email), ("mail@domain", email)]
    kb = {
        "format": FORMAT, "site": url.rstrip("/"), "help": url + "#contact", "source": site_commit(site),
        "errors": build_errors(site, url), "articles": build_articles(site, url), "search": build_search(site, url),
        "sizing": build_sizing(site, url), "aisizing": build_aisizing(site, url), "versions": build_versions(site, url),
        "upgrade": build_upgrade(site, url), "security": build_security(site, url), "ports": build_ports(site),
    }
    kb = substitute(kb, repl)
    text = json.dumps(kb, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    bad = FORBIDDEN.search(text)
    if bad:
        a = max(0, bad.start() - 60)
        raise Fail("в базе то, чего там быть не должно: …%s…" % text[a:bad.end() + 60])
    return kb, text


def main(argv):
    ap = argparse.ArgumentParser(description="Собирает kb.json для rxkb из исходников сайта drxinfra.ru")
    ap.add_argument("--site", default=os.path.join(os.path.dirname(ROOT), "landing"), help="репозиторий сайта")
    ap.add_argument("--out", default="kb.json", help="куда записать базу")
    a = ap.parse_args(argv)
    domain = os.environ.get("SITE_DOMAIN") or "drxinfra.ru"
    telegram = os.environ.get("TG_USERNAME") or "drxinfra_bot"
    email = os.environ.get("CONTACT_EMAIL") or "support@drxinfra.ru"
    try:
        kb, text = build(os.path.abspath(a.site), domain, telegram, email)
    except (Fail, OSError, ValueError, AttributeError) as e:
        print("build-kb: %s" % e, file=sys.stderr)
        return 1
    tmp = a.out + ".tmp"
    with io.open(tmp, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)
    os.replace(tmp, a.out)
    print("%s: ошибок %d, статей %d, страниц в поиске %d, вопросов %d, пунктов чек-листа %d, %d КБ" % (
        a.out, len(kb["errors"]), len(kb["articles"]), len(kb["search"]), len(kb["security"]["questions"]),
        kb["upgrade"]["count"], len(text.encode("utf-8")) // 1024))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
