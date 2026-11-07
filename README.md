# rxkb: drxinfra knowledge base for Directum RX over MCP

**Public MCP server with the drxinfra.ru knowledge base for Directum RX administrators.** An AI agent (Claude Code, Cursor, Claude Desktop, VS Code, Cline and any client with remote MCP) asks it directly: look up an error by a log line in a reference of about 200 errors (RX services, Directum Launcher, PostgreSQL, RabbitMQ, Elasticsearch, certificates, Keycloak, nginx, Docker, Kubernetes), read articles, search the site, size servers for RX and for a local AI assistant with the same numbers as the site calculators, check component versions, walk the upgrade checklist and the weighted security self-assessment. Endpoint `https://drxinfra.ru/mcp` (Streamable HTTP, MCP 2026-07-28 and earlier), read-only, no sign-in, no outbound connections, request contents are not stored. Content is in Russian.

```sh
claude mcp add --transport http rxkb https://drxinfra.ru/mcp
```

MCP-сервер с базой знаний [drxinfra.ru](https://drxinfra.ru) для администраторов Directum RX. ИИ-агент спрашивает его сам, без браузера и копирования со страниц: что значит строка из лога, как это проверить и исправить, сколько серверов нужно под RX, какое железо под локальную модель ИИ, какие версии PostgreSQL и Elasticsearch поддерживаются, что не забыть при обновлении.

Адрес: `https://drxinfra.ru/mcp`. Только чтение, без регистрации и ключей. Страница для людей: [drxinfra.ru/mcp.html](https://drxinfra.ru/mcp.html).

## Подключение

**Claude Code**

```sh
claude mcp add --transport http rxkb https://drxinfra.ru/mcp
```

**Cursor** (`~/.cursor/mcp.json` или `.cursor/mcp.json` в проекте)

```json
{ "mcpServers": { "rxkb": { "url": "https://drxinfra.ru/mcp" } } }
```

**VS Code** (`.vscode/mcp.json`)

```json
{ "servers": { "rxkb": { "type": "http", "url": "https://drxinfra.ru/mcp" } } }
```

**Claude Desktop и claude.ai**: Settings → Connectors → Add custom connector, адрес `https://drxinfra.ru/mcp`.

**Другие клиенты** (Cline, Roo Code, Continue, OpenCode, Cherry Studio и агенты на моделях DeepSeek, Qwen, GigaChat): тип сервера Streamable HTTP, тот же адрес. Клиенту, который умеет только stdio, поможет мост: `npx -y mcp-remote https://drxinfra.ru/mcp`.

Проверить, что сервер отвечает, можно без клиента:

```sh
curl -s https://drxinfra.ru/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

## Инструменты

| Инструмент | Что делает |
|---|---|
| `find_error` | строка лога или текст ошибки → статьи справочника: дословная ошибка, что это значит, как проверить, что сделать, ссылка. Те же шаблоны и тот же порядок, что в поле разбора на [drxinfra.ru/errors](https://drxinfra.ru/errors/) |
| `get_article` | полный текст статьи блога или страницы ошибки по id или адресу на drxinfra.ru |
| `search_site` | поиск по статьям, ошибкам и страницам инструментов с той же выдачей, что у поиска на сайте |
| `size_rx` | серверы, vCPU, память и диски под RX по ролям для прода, теста и препрода. Считает та же модель, что [калькулятор сайзинга](https://drxinfra.ru/sizing.html), и ответ содержит ссылку, по которой этот же расчёт открывается на сайте |
| `size_ai` | железо под локального ИИ-помощника RX по нашим замерам llama.cpp на CPU: серверы, память, время ответа, качество ответов, сравнение моделей, как [на сайте](https://drxinfra.ru/ai-sizing.html) |
| `component_versions` | версии ОС, Docker, PostgreSQL и совместимых СУБД, MS SQL, RabbitMQ, Elasticsearch и Kubernetes для RX 4.12 и 25.2 по документам вендора. Для других версий честно говорит, что данных нет |
| `upgrade_checklist` | чек-лист обновления RX: 44 пункта в 7 этапах, отметки и ссылка, по которой они открываются на [странице чек-листа](https://drxinfra.ru/upgrade-checklist.html) |
| `security_self_check` | 22 вопроса самооценки безопасности с весами; по ответам балл, зона и список дыр в том же порядке, что [на сайте](https://drxinfra.ru/security-check.html) |

Каждый ответ заканчивается ссылкой на страницу drxinfra.ru, откуда он взят, и строкой, куда писать, если нужна помощь.

Примеры вопросов агенту:

- «В логе WebServer `Npgsql.PostgresException: 53300: sorry, too many clients already`, что делать?»
- «Посчитай серверы под RX на 1500 пользователей, Kubernetes, отказоустойчивость, 300 тысяч документов в год»
- «Какое железо нужно под локальную модель для помощника RX на 500 сотрудников?»
- «Какие версии Elasticsearch поддерживает RX 25.2?»
- «Пройдём самооценку безопасности нашего контура по вопросам drxinfra»

## Что сервер видит и что хранит

- Сервер получает только аргументы вызова: строку лога, запрос поиска, параметры расчёта. Перед отправкой строки лога уберите из неё пароли, токены, имена серверов и персональные данные: для разбора нужна только сама ошибка.
- Аргументы нигде не сохраняются. В журнале сервиса на каждый запрос одна строка: метод, имя инструмента, имя клиента, код ответа, размеры и время. Адресов и аргументов там нет. Журнал веб-сервера пишет обрезанный адрес (последний октет нулевой) и путь `/mcp` без тела запроса, как для всего сайта.
- Сервер никуда не ходит: все данные в одном файле, собранном из исходников сайта при выкладке.
- Ограничения: частота запросов с одного адреса, одновременные запросы, размер запроса и время ответа. Подробно: [docs/security.md](docs/security.md).

## Как устроено

```
сайт (статьи, справочник ошибок, калькуляторы) ──hack/build-kb.py──▶ kb.json ──▶ rxkb ──Streamable HTTP──▶ ИИ-агент
```

- `hack/build-kb.py` собирает `kb.json` из исходников сайта: markdown статей и ошибок, индекс поиска, модели калькуляторов, вопросы и чек-листы из разметки страниц. Только стандартная библиотека Python.
- Модель сайзинга RX сервер выполняет как есть: это тот же `assets/sizing.js`, что считает в браузере, во встроенном интерпретаторе JavaScript ([goja](https://github.com/dop251/goja)). Скрипт приходит из репозитория сайта при сборке, запрос передаёт только проверенные числа и значения из списка.
- Разбор строки лога, поиск, железо под ИИ, самооценка и чек-лист повторены на Go строка в строку за скриптами сайта. `hack/parity/site-check.mjs` прогоняет одни и те же входы через сервер и через страницы сайта в headless Chrome и сравнивает ответы. Если скрипт сайта меняется, сервер пишет предупреждение при загрузке базы.
- Протокол: [официальный Go SDK MCP](https://github.com/modelcontextprotocol/go-sdk), режим без сессий, ответы JSON. Клиенты на протоколе 2026-07-28 работают через `server/discover`, старые (2025-11-25, 2025-06-18, 2025-03-26) через `initialize`.

## Разработка

```sh
go test ./...                                    # тесты на тестовом мини-сайте testdata/site
go run . -kb testdata/kb.json -listen 127.0.0.1:8790
python3 hack/build-kb.py --site ../landing --out /tmp/kb.json   # база из настоящего сайта
go run . -kb /tmp/kb.json -check                 # что в базе
node hack/parity/site-check.mjs --mcp http://127.0.0.1:8790/mcp --site https://drxinfra.ru/
```

Проверки перед коммитом и в CI: `gofmt`, `go vet`, `go test`, `hack/leakcheck.sh`, `hack/vulncheck.sh` (trivy по бинарнику и образу). Выкладка на сервер: [docs/deploy.md](docs/deploy.md). Изменения: [CHANGELOG.md](CHANGELOG.md).

## Лицензия

Код под Apache 2.0, см. [LICENSE](LICENSE) и [NOTICE](NOTICE). Имена rxkb и drxinfra лицензия не передаёт: форк распространяйте под другим именем. Тексты статей и справочника drxinfra.ru в репозиторий не входят и под эту лицензию не попадают: сервер отдаёт их со ссылкой на сайт.

Directum RX — продукт компании Directum. Проект с вендором не связан.
