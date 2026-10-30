# Kordo для разработчиков

Как устроен Kordo, как его собрать, протестировать и выпустить. Для пользователей — [README](../README.md).

## Устройство

Транспорт, маршрутизацию и TUN делает [sing-box](https://github.com/SagerNet/sing-box) 1.14.4, встроенный как Go-библиотека. Kordo добавляет модель данных, генератор конфигов, супервизор, kill switch и интерфейс.

Три программы:
- `kordo-svc.exe` — служба `KordoSvc` (LocalSystem): TUN, ядро sing-box, kill switch (WFP), отслеживание процессов. Слушает JSON-RPC на named pipe `\\.\pipe\kordo` (DACL: SYSTEM, администраторы, интерактивные пользователи).
- `Kordo.exe` — окно на Wails v2 (WebView2), работает без прав администратора и всё делает через службу. Значок в трее — `energye/systray`.
- `kordoctl.exe` — CLI к той же службе.

Данные: `%ProgramData%\Kordo` (доступ только SYSTEM и администраторам): `state.json` с профилем и серверами (параметры серверов зашифрованы DPAPI), `config.json` и `rules/*.json` для ядра, `logs/`.

Ключевые решения:
- **Правила приложений — local rule-set.** Ядро само перечитывает файлы, поэтому добавление или смена маршрута приложения не рвёт соединения. Смена серверов, DNS, MTU или стека перезапускает ядро.
- **DNS следует маршруту.** В режиме «Через VPN» DNS-правила повторяют порядок правил маршрутизации: VPN-приложения резолвятся через DoH по своему серверу, прямые — через DoH напрямую.
- **Подпроцессы** узнаются через ETW (Microsoft-Windows-Kernel-Process, ProcessStart) плюс опрос снимков раз в 15 с; без ETW — опрос раз в 2 с. Политика (`inherit` / `direct` / `ask`, общие программы вроде cmd/node/python) — в `core/proctree`.
- **Проверки серверов** — HTTP 204 через outbound каждого сервера раз в 30 с (раз в 5 с, пока сервер недоступен). Сервер считается упавшим после ≥2 неудач подряд в течение ≥10 с; тогда закрываются только его соединения. Ядро из-за упавшего сервера не перезапускается.
- **Kill switch** — постоянные (persistent) фильтры WFP, переживают падение службы и перезагрузку; снимаются при ручном отключении и при удалении.

Подробнее — [ADR-0001](ADR-0001-mvp-decisions.md).

### Структура

```
core/            платформонезависимое ядро
  model/         Server, AppRule, Profile
  providers/     vless, shadowsocks, wireguard (+ golden-тесты)
  configgen/     Profile → config.json + rules/apps-*.json (детерминированно, golden-тесты)
  supervisor/    машина состояний, проверки серверов, backoff, hot reload
  proctree/      политика подпроцессов
  rpcapi/        JSON-RPC 2.0 построчно + типы
  version/       сравнение версий (обновления)
engine/          sing-box как библиотека (Check/Start/URLTest/Clash API)
windows/
  service/       служба: связка всего + RPC-обработчики
  store/         state.json + DPAPI
  killswitch/    WFP (tailscale/wf, локальная копия в third_party/wf)
  netmon/        NotifyIpInterfaceChange / RouteChange2 / UnicastIpAddressChange
  procs/         процессы, установленные приложения, ETW
  gui/           Wails-приложение (frontend/ — HTML/CSS/JS без сборщика), трей, обновления
cmd/kordo-svc    точка входа службы
cmd/kordoctl     CLI
testdata/        golden-конфиги и фикстуры провайдеров
build/           сборка, установщик, подпись, превью и автотест интерфейса
docs/            ADR, эта страница
```

## Сборка

Нужен Go из `go.mod` (сейчас 1.26.10). CGO и Node.js для сборки не нужны. Версия продукта — `build/version.txt`, теги сборки ядра — `build/tags.txt` (`with_gvisor,with_wireguard,with_utls,with_clash_api`).

На Windows, только бинарники в `bin/`:

```bash
powershell -ExecutionPolicy Bypass -File build/build.ps1 [-Version 0.3.0]
```

Ручная установка из `bin/` (от администратора): `bin\kordo-svc.exe install` — регистрирует и запускает службу; потом `bin\Kordo.exe`. Другие команды службы: `run` (в консоли, для отладки), `start`, `stop` (ждёт остановки), `uninstall` (заодно снимает все фильтры WFP). Если потом поставить Kordo установщиком, он перенесёт службу в Program Files.

### Установщик

Собирается на Linux (или macOS): кросс-компиляция `GOOS=windows GOARCH=amd64` без CGO, иконки и манифесты — `go-winres`, затем [NSIS](https://nsis.sourceforge.io/) собирает `build/installer/kordo.nsi`.

```bash
sudo apt install nsis          # нужен makensis 3.08+
build/build-installer.sh 0.3.0 # без аргумента берётся build/version.txt
```

Результат: `bin/*.exe` и `dist/Kordo-Setup-0.3.0.exe` + `.sha256`. Версия попадает в `service.Version`, `main.appVersion` (окно), ресурсы exe и установщик.

Что делает установщик: ставит в `%ProgramFiles%\Kordo`; при обновлении останавливает службу, закрывает окно и заменяет файлы; службу, зарегистрированную из другой папки, удаляет и регистрирует заново; проверяет WebView2 и при отсутствии предлагает открыть загрузку у Microsoft (сам ничего не скачивает). Деинсталлятор удаляет службу и фильтры WFP **до** файлов. Тихий режим: `/S`.

### Подпись кода

Если задана переменная `KORDO_SIGN_CMD`, `build-installer.sh` вызывает её для каждого exe в `bin/`, для деинсталлятора и для установщика (через `!finalize`/`!uninstfinalize` NSIS), передавая путь к файлу последним аргументом. Готовая обёртка для `osslsigncode`:

```bash
KORDO_PFX=cert.pfx KORDO_PFX_PASSWORD=... \
KORDO_SIGN_CMD="$PWD/build/sign-osslsigncode.sh" build/build-installer.sh 0.3.0
```

На Windows подойдёт `signtool sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /a`.

**В CI** подпись включается сама, если в репозитории есть секреты (Settings → Secrets and variables → Actions): `SIGN_PFX_BASE64` — сертификат `.pfx` в base64 (`base64 -w0 cert.pfx`) и `SIGN_PFX_PASSWORD`. Без них сборка остаётся неподписанной.

Где взять сертификат: [SignPath Foundation](https://signpath.org) (бесплатно для open-source, по заявке; подписывает через свой GitHub Action, это потребует отдельного шага в CI), обычный OV-сертификат (Certum, Sectigo и др., от ~$100–300 в год, подходит под секреты выше), Azure Trusted Signing (доступен не во всех странах).

Подписанный Kordo запускает обновление только с действительной подписью (WinVerifyTrust); неподписанные сборки проверяют только SHA-256 из релиза.

## Тесты

```bash
go test -tags "$(cat build/tags.txt)" ./...
```

Проходят на Linux и на Windows; пакеты из `windows/` собираются только под Windows — на Linux их проверяет `GOOS=windows go vet`. Тест ETW (`windows/procs`) требует прав администратора (на Windows-раннерах GitHub они есть).

- `engine` прогоняет каждый сгенерированный конфиг через парсер и конструктор sing-box (аналог `sing-box check`).
- Golden-файлы: `UPDATE_GOLDEN=1 go test ./core/...`.
- Превью интерфейса в браузере на мок-данных: `node build/devserver.js` → http://localhost:5178.
- Автотест интерфейса на тех же мок-данных (все экраны и основные кнопки, падает на любой ошибке JS): `node build/ui-smoke.js` (нужен `playwright`).

## CI и релизы

`.github/workflows/ci.yml`:
- в каждом PR и на `main`: `gofmt`, `go vet` и `go test` на Windows и на Linux, автотест интерфейса, сборка установщика на Linux (артефакт, версия `<version.txt>-dev.<номер запуска>`);
- на тег `v*` — GitHub Release с `Kordo-Setup-<версия>.exe` и `.sha256`; теги с суффиксом (`v0.3.0-rc1`) — pre-release (обновление в окне их не предлагает).

Выпуск версии:

```bash
echo 0.3.0 > build/version.txt && git commit -am "Kordo 0.3.0"
git tag v0.3.0 && git push origin main v0.3.0
```

## CLI

```text
kordoctl status | connect | disconnect
kordoctl add "vless://…"            # или add @wg.conf
kordoctl route "C:\Program Files\Mozilla Firefox\firefox.exe" <server-id>
kordoctl default <server-id|direct>
kordoctl killswitch off|vpn_apps|all
kordoctl killswitch-clear           # аварийно снять фильтры
kordoctl activity | rules | servers | logs [core]
```

## Известные ограничения

- Правило для нового подпроцесса применяется после того, как ядро перечитает rule-set, поэтому самые первые пакеты очень короткоживущего процесса могут пройти по маршруту по умолчанию.
- Точный режим по PID для общих exe (один `node.exe` через VPN, другой — нет) требует патча sing-box.
- Android-версии пока нет.
