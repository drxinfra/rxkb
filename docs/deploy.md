# rxkb: выкладка

Сервис рассчитан на тот же сервер, что и сайт: статический бинарник под systemd за nginx. Docker не нужен (образ есть для тех, кому удобнее контейнер, см. ниже).

```
nginx :443  location = /mcp  ──▶  rxkb 127.0.0.1:8790  ◀── /var/lib/rxkb/kb.json  ◀── выкладка сайта (rsync)
```

## Первая установка

1. Бинарник: собрать (`hack/build.sh v0.1.0`) или взять архив со страницы Releases, сверить сумму с `SHA256SUMS` и происхождение (`gh attestation verify <архив> --repo drxinfra/rxkb`), положить в `/usr/local/bin/rxkb` (root, 0755).
2. Каталог базы: `/var/lib/rxkb`, владелец — пользователь, от которого выкладывается сайт, права 0755. Сервис читает оттуда только `kb.json`.
3. Первая база: `python3 hack/build-kb.py --site <репозиторий сайта> --out kb.json` и скопировать в `/var/lib/rxkb/kb.json` (0644). Проверка: `rxkb -kb /var/lib/rxkb/kb.json -check`.
4. Unit: [deploy/rxkb.service](../deploy/rxkb.service) в `/etc/systemd/system/`, `systemctl daemon-reload`, `systemctl enable --now rxkb`. Проверка: `curl -s 127.0.0.1:8790/healthz`, `systemd-analyze security rxkb`.
5. nginx: зоны лимитов и `location = /mcp` из [deploy/nginx-mcp.conf](../deploy/nginx-mcp.conf), `nginx -t`, `systemctl reload nginx`.
6. Проверка снаружи: `tools/list` запросом curl из README и `node hack/parity/site-check.mjs --mcp https://<сайт>/mcp --site https://<сайт>/`.

## Обновление базы

База пересобирается при каждой выкладке сайта и копируется в `/var/lib/rxkb/kb.json` (rsync пишет во временный файл и подменяет его целиком). Сервис раз в 30 секунд смотрит на время и размер файла и подхватывает новую базу без перезапуска. Немедленно: `systemctl reload rxkb` (SIGHUP).

Если новая база не загрузилась (битый файл, другой формат, изменилась модель калькулятора так, что она не считает), сервис пишет причину в журнал и продолжает отвечать по старой. Если на сайте изменился один из скриптов, логику которых сервис повторяет на Go (`aisizing.js`, `errors.js`, `search.js`, `seccheck.js`, `upgrade.js`), база загружается, а в журнале появляется предупреждение: сверьте порт (`hack/parity/site-check.mjs`) и обновите суммы в `internal/kb/kb.go`.

## Обновление сервиса

```sh
install -m 0755 rxkb /usr/local/bin/rxkb.new && mv /usr/local/bin/rxkb.new /usr/local/bin/rxkb
systemctl restart rxkb
```

Откат: вернуть прежний бинарник (держите его рядом как `rxkb.prev`) и перезапустить.

## Удаление

```sh
systemctl disable --now rxkb
rm /etc/systemd/system/rxkb.service /usr/local/bin/rxkb && systemctl daemon-reload
```

Убрать `include` сниппета и файл зон из nginx, `nginx -t`, `systemctl reload nginx`. Каталог `/var/lib/rxkb` можно оставить: в нём только открытые данные сайта.

## Контейнер

```sh
docker build -t rxkb .
docker run --rm -p 127.0.0.1:8790:8790 -v /var/lib/rxkb:/data:ro --read-only --cap-drop ALL --security-opt no-new-privileges rxkb
```

Внутри образа слушает `0.0.0.0:8790`, база `/data/kb.json`. Перед контейнером так же нужен nginx с лимитами.

## Настройки

| Флаг | Переменная | По умолчанию | Что |
|---|---|---|---|
| `-listen` | `RXKB_LISTEN` | `127.0.0.1:8790` | адрес |
| `-kb` | `RXKB_FILE` | `/var/lib/rxkb/kb.json` | база |
| `-reload` | | `30s` | как часто смотреть, не сменилась ли база |
| | `RXKB_RATE` | `2` | запросов в секунду с одного адреса |
| | `RXKB_BURST` | `30` | всплеск с одного адреса |
| | `RXKB_MAX_CONCURRENT` | `16` | одновременных запросов всего |
| | `RXKB_MAX_BODY` | `131072` | байт в запросе |
| `-check` | | | загрузить базу, показать, что в ней, и выйти |
| `-version` | | | версия |

Адрес клиента для лимитов сервис берёт из `X-Real-IP` только тогда, когда запрос пришёл с localhost, то есть от nginx.
