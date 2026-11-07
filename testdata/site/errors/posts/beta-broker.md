---
title: BetaBrokerException: endpoints were not reachable
what: Сервис не достучался до тестового брокера
component: RabbitMQ
date: 2026-01-11
description: Тестовая ошибка брокера для проверки сопоставления и поиска по словам.
match: BetaBrokerException || endpoints were not reachable
---

## Что это значит

Брокер недоступен с сервера сервиса.

## Как проверить

Проверить порт 5672 с сервера сервиса.

## Что сделать

Поднять брокер или поправить адрес в настройках, см. [калькулятор](../sizing.html).
