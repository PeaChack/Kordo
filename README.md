# Portico

Клиент для Windows для собственных VPN/прокси-серверов. Модель простая: **«мой сервер → мои приложения»**, без ручных JSON-конфигов.
Транспорт, маршрутизацию и TUN делает [sing-box](https://github.com/SagerNet/sing-box) 1.14.4. Он встроен как библиотека. Portico добавляет модель данных, генератор конфигов, супервизор, kill switch и интерфейс.

> Portico — независимый проект, он не связан с sing-box/SagerNet. Лицензия: GPL-3.0-or-later.

## Возможности

- **Серверы:** VLESS (TCP / Reality / TLS, ws, grpc, httpupgrade, http), Shadowsocks (AEAD, 2022, SIP002), WireGuard (`.conf`, `wireguard://`). Добавляются вставкой ссылки, импортом `.conf` или ручной формой.
- **Приложения → сервер:** приложение можно выбрать из запущенных, из установленных, через диалог файла (`.exe`/`.lnk`) или перетащить в окно. Маршрут на выбор: сервер, напрямую или блок. Приложения из Microsoft Store определяются по имени пакета, поэтому правило переживает обновления.
- **Два режима:** «только выбранные через VPN» или «всё через VPN, кроме выбранных».
- **Подпроцессы:** для каждого приложения задаётся `inherit` / `direct` / `ask`. Дочерние программы запоминаются автоматически. Для общих программ (cmd, powershell, node, python, java, msedgewebview2…) Portico всегда спрашивает.
- **Правила меняются без разрыва соединений:** приложения лежат в local rule-set, sing-box перечитывает его сам. Изменения серверов, DNS или TUN ведут к перезапуску ядра.
- **Kill switch (WFP):** режимы `off`, `vpn_apps` и `all`. Фильтры persistent: они переживают падение службы и перезагрузку. При ручном отключении фильтры снимаются.
- **DNS:** весь DNS перехватывается ядром. По умолчанию запросы идут через DoH по выбранному серверу. Порты 53/853 блокируются на физических интерфейсах для всех, кроме ядра.
- **Устойчивость:** netmon (IP Helper) и выход из сна запускают health-check каждого сервера через его outbound (HTTP 204). После 2 провалов подряд соединения закрываются. После 3 ядро перезапускается с backoff 1→60 с и jitter.
- **Активность:** экран «какое приложение куда ушло» (данные из Clash API ядра).
- **Безопасность:** GUI работает без прав администратора. Всё привилегированное выполняет служба `LocalSystem`. Связь идёт через named pipe `\\.\pipe\portico` с DACL. Параметры серверов зашифрованы DPAPI, папка `%ProgramData%\Portico` доступна только SYSTEM и администраторам.

## Сборка

Нужен Go ≥ 1.25 (проверено на 1.26.10). CGO и Node.js не нужны: фронтенд написан на чистом HTML/JS без сборщика.

```bash
powershell -ExecutionPolicy Bypass -File build/build.ps1
```

Результат появится в `bin/`: `Portico.exe` (GUI), `portico-svc.exe` (служба), `porticoctl.exe` (CLI).
Теги сборки ядра лежат в `build/tags.txt` (`with_gvisor,with_wireguard,with_utls,with_clash_api`).

## Установка и запуск

```bash
bin\portico-svc.exe install
```

Команду нужно запускать от имени администратора: она регистрирует службу `PorticoSvc` (автозапуск, перезапуск при сбое) и сразу её запускает. После этого откройте `bin\Portico.exe` (права администратора не нужны).

Отладочный запуск службы в консоли (тоже от администратора): `portico-svc.exe run`. Удаление: `portico-svc.exe uninstall` (заодно снимает все WFP-фильтры).

### CLI

```text
porticoctl status | connect | disconnect
porticoctl add "vless://…"            # или add @wg.conf
porticoctl route "C:\Program Files\Mozilla Firefox\firefox.exe" <server-id>
porticoctl default <server-id|direct>
porticoctl killswitch off|vpn_apps|all
porticoctl killswitch-clear           # аварийно снять фильтры
porticoctl activity | rules | servers | logs [core]
```

## Структура

```
core/            платформонезависимое ядро
  model/         Server, AppRule, Profile
  providers/     vless, shadowsocks, wireguard (+ golden-тесты)
  configgen/     Profile → config.json + rules/apps-*.json (детерминированно, golden-тесты)
  supervisor/    машина состояний, health-check, backoff, hot reload
  proctree/      политика подпроцессов
  rpcapi/        JSON-RPC 2.0 построчно + типы
engine/          sing-box как библиотека (Check/Start/URLTest/Clash API)
windows/
  service/       служба: связка всего + RPC-обработчики
  store/         state.json + DPAPI
  killswitch/    WFP (tailscale/wf)
  netmon/        NotifyIpInterfaceChange / RouteChange2 / UnicastIpAddressChange
  procs/         процессы, установленные приложения, отслеживание запусков
  gui/           Wails-приложение (frontend/ — HTML/CSS/JS)
cmd/portico-svc  точка входа службы
cmd/porticoctl   CLI
testdata/        golden-конфиги и фикстуры провайдеров
docs/            ADR
```

## Тесты

```bash
go test -tags with_gvisor,with_wireguard,with_utls,with_clash_api ./...
```

`engine` прогоняет каждый сгенерированный конфиг через строгий парсер и конструктор sing-box: это эквивалент `sing-box check`. Golden-файлы обновляются так: `UPDATE_GOLDEN=1 go test ./core/...`.

Превью интерфейса в браузере с мок-данными: `node build/devserver.js`, затем откройте http://localhost:5178.

## Ограничения текущей версии

См. [docs/ADR-0001-mvp-decisions.md](docs/ADR-0001-mvp-decisions.md). Кратко:
- подпроцессы отслеживаются опросом снимков процессов раз в 2 с (ETW пока нет), поэтому первое соединение очень короткоживущего потомка может пройти мимо правила;
- точный PID-режим для общих exe требует патча sing-box (этап v1.1);
- иконки в трее пока нет: окно можно закрыть, служба продолжит работать;
- Android, подписи кода и инсталлятора пока нет.
