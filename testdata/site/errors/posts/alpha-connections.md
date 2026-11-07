---
title: FATAL: alpha pool is exhausted
what: Кончились соединения в тестовом пуле
component: PostgreSQL
date: 2026-01-10
description: Тестовая ошибка про соединения для проверки сопоставления.
match: alpha pool is exhausted || ALPHA-\d{3}
---

## Что это значит

Пул соединений тестового сервиса заполнен. Подробнее в [статье](../blog/first-post.html).

## Как проверить

```sql
select count(*) from pg_stat_activity;
```

## Что сделать

1. Увеличить пул.
2. Найти утечку соединений.
