#!/usr/bin/env sh
# Страж утечек: в публичной репе не должно быть адресов серверов, учёток и чужих имён.
# Запускается в CI и перед коммитом. Общие проверки (пароль в открытую, IP-адреса) живут здесь,
# а имена и адреса, которые искать, в репе не лежат: переменная LEAKCHECK_PATTERN (секрет CI)
# или локальный файл .leakcheck (одна регулярка на строку, файл в .gitignore).
set -eu
cd "$(dirname "$0")/.."
pat="password[\"']?[=:][\"']?[a-z0-9]|[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}"
private="${LEAKCHECK_PATTERN:-}"
if [ -z "$private" ] && [ -f .leakcheck ]; then
  private=$(grep -v '^[[:space:]]*\(#\|$\)' .leakcheck | paste -sd'|' -)
fi
if [ -n "$private" ]; then
  pat="$pat|$private"
else
  echo "leakcheck: личных шаблонов нет (LEAKCHECK_PATTERN или .leakcheck), проверяю только общие"
fi
# Исключения: localhost и 0.0.0.0, документационные сети RFC 5737 в тестах, версии модулей в go.sum,
# тестовый пароль в web_test.go (проверяет, что аргументы не попадают в журнал).
if git ls-files -z | grep -zv '^hack/leakcheck.sh$' | xargs -0 grep -EnI -i "$pat" 2>/dev/null \
  | grep -Ev '127\.0\.0\.1|0\.0\.0\.0|203\.0\.113\.|198\.51\.100\.|go\.sum:|Hunter2|LICENSE:'; then
  echo "leakcheck: найдены подозрительные строки, см. выше" >&2
  exit 1
fi
echo "leakcheck: чисто"
