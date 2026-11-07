#!/usr/bin/env sh
# Проверка уязвимостей: собирает rxkb под linux/amd64 и прогоняет trivy по бинарнику
# (stdlib Go и модули из go.sum). Падает на HIGH и CRITICAL, у которых уже есть исправление:
# такие закрываются подъёмом Go или модуля. Без исправления — печатаются, но не валят сборку.
# Аргумент: путь к готовому бинарнику, архив образа (.tar из docker save) или ссылка на образ.
set -eu
cd "$(dirname "$0")/.."
command -v trivy >/dev/null || { echo "нет trivy: brew install trivy или hack/install-trivy.sh" >&2; exit 2; }
target="${1:-}"
if [ -z "$target" ]; then
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$tmp/rxkb" .
  target="$tmp/rxkb"
fi
case "$target" in
  *.tar) mode="image --input" ;;
  *) if [ -e "$target" ]; then mode=rootfs; else mode=image; fi ;;
esac
echo "trivy $mode $target"
# Полный список для журнала, затем гейт.
# shellcheck disable=SC2086
trivy $mode "$target" --quiet --scanners vuln --severity MEDIUM,HIGH,CRITICAL
# shellcheck disable=SC2086
trivy $mode "$target" --quiet --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1
echo "уязвимостей HIGH/CRITICAL с исправлением нет"
