# Kordo

Клиент для Windows для собственных VPN/прокси-серверов. Модель простая: **«мой сервер → мои приложения»**, без ручных JSON-конфигов.
Транспорт, маршрутизацию и TUN делает [sing-box](https://github.com/SagerNet/sing-box) 1.14.4. Он встроен как библиотека. Kordo добавляет модель данных, генератор конфигов, супервизор, kill switch и интерфейс.

> Kordo — независимый проект, он не связан с sing-box/SagerNet. Лицензия: GPL-3.0-or-later.

## Возможности

- **Серверы:** VLESS (TCP / Reality / TLS, ws, grpc, httpupgrade, http), Shadowsocks (AEAD, 2022, SIP002), WireGuard (`.conf`, `wireguard://`). Добавляются вставкой ссылки, импортом `.conf` или ручной формой.
- **Приложения → сервер:** приложение можно выбрать из запущенных, из установленных, через диалог файла (`.exe`/`.lnk`) или перетащить в окно. Маршрут на выбор: сервер, напрямую или блок. Приложения из Microsoft Store определяются по имени пакета, поэтому правило переживает обновления.
- **Два режима:** «только выбранные через VPN» или «всё через VPN, кроме выбранных».
- **Подпроцессы:** для каждого приложения задаётся `inherit` / `direct` / `ask`. Дочерние программы запоминаются автоматически. Для общих программ (cmd, powershell, node, python, java, msedgewebview2…) Kordo всегда спрашивает.
- **Правила меняются без разрыва соединений:** приложения лежат в local rule-set, sing-box перечитывает его сам. Изменения серверов, DNS или TUN ведут к перезапуску ядра.
- **Kill switch (WFP):** режимы `off`, `vpn_apps` и `all`. Фильтры persistent: они переживают падение службы и перезагрузку. При ручном отключении фильтры снимаются.
- **DNS:** весь DNS перехватывается ядром. По умолчанию запросы VPN-приложений идут через DoH по их серверу, а приложений «напрямую» — через DoH напрямую: так сайты с CDN отдают им ближние узлы. Порты 53/853 блокируются на физических интерфейсах для всех, кроме ядра.
- **Устойчивость:** netmon (IP Helper) и выход из сна запускают health-check каждого сервера через его outbound (HTTP 204). После 2 провалов подряд соединения закрываются. После 3 ядро перезапускается с backoff 1→60 с и jitter.
- **Активность:** экран «какое приложение куда ушло» (данные из Clash API ядра).
- **Безопасность:** GUI работает без прав администратора. Всё привилегированное выполняет служба `LocalSystem`. Связь идёт через named pipe `\\.\pipe\kordo` с DACL. Параметры серверов зашифрованы DPAPI, папка `%ProgramData%\Kordo` доступна только SYSTEM и администраторам.

## Установка

Скачайте `Kordo-Setup-<версия>.exe` со страницы [Releases](https://github.com/PeaChack/Kordo/releases) (рядом лежит `.sha256`) и запустите. Нужны 64-битная Windows 10/11 и права администратора.

Установщик:
- кладёт `Kordo.exe`, `kordo-svc.exe`, `kordoctl.exe` в `%ProgramFiles%\Kordo`, добавляет ярлык в меню «Пуск» и запись в «Программы и компоненты»;
- регистрирует и запускает службу `KordoSvc` (автозапуск, перезапуск при сбое);
- при обновлении останавливает службу, закрывает окно Kordo, заменяет файлы и запускает службу снова. Если служба была зарегистрирована из другой папки (ручная установка из `bin\`), он удаляет её и регистрирует заново из Program Files. Запись автозапуска окна тоже переносится на новый путь;
- проверяет Microsoft Edge WebView2 Runtime (на Windows 11 он есть) и, если его нет, предлагает скачать его установщик с сайта Microsoft в браузере (сам установщик Kordo ничего не скачивает).

Удаление — через «Программы и компоненты» или `%ProgramFiles%\Kordo\uninstall.exe`. Сначала удаляется служба и снимаются все WFP-фильтры kill switch, потом файлы, ярлык и автозапуск. Настройки и серверы в `%ProgramData%\Kordo` по умолчанию остаются; деинсталлятор спросит, удалить ли их.

**Обновления.** При запуске (и дважды в сутки, пока окно открыто) Kordo проверяет последний релиз на GitHub; предварительные версии не предлагаются. Если есть новее, в боковой панели появляется «Доступна версия …»: «Обновить» скачивает установщик, сверяет его SHA-256 с файлом из релиза и запускает (Windows попросит права администратора). Проверку можно выключить или запустить вручную в «Настройках». Сборки без версии (`dev`) обновления не проверяют.

Тихий режим: `Kordo-Setup-<версия>.exe /S`, удаление `uninstall.exe /S` (настройки при этом сохраняются).

> Подписи кода пока нет, поэтому SmartScreen может предупредить о неизвестном издателе: «Подробнее» → «Выполнить в любом случае». Сверить файл можно по SHA-256 из релиза.

### Ручная установка (для разработки)

```bash
bin\kordo-svc.exe install
```

Команду нужно запускать от имени администратора: она регистрирует службу `KordoSvc` и сразу её запускает. После этого откройте `bin\Kordo.exe` (права администратора не нужны). Если потом поставить Kordo установщиком, он сам перенесёт службу в Program Files.

Отладочный запуск службы в консоли (тоже от администратора): `kordo-svc.exe run`. Остановка с ожиданием: `kordo-svc.exe stop`. Удаление: `kordo-svc.exe uninstall` (заодно снимает все WFP-фильтры).

## Сборка

Нужен Go из `go.mod` (сейчас 1.26.10). CGO и Node.js не нужны: фронтенд написан на чистом HTML/JS без сборщика. Версия продукта лежит в `build/version.txt`, теги сборки ядра — в `build/tags.txt` (`with_gvisor,with_wireguard,with_utls,with_clash_api`).

На Windows, только бинарники:

```bash
powershell -ExecutionPolicy Bypass -File build/build.ps1 [-Version 0.3.0]
```

Результат появится в `bin/`: `Kordo.exe` (GUI), `kordo-svc.exe` (служба), `kordoctl.exe` (CLI).

### Установщик

Установщик собирается на Linux (или macOS): бинарники кросс-компилируются (`GOOS=windows GOARCH=amd64`, без CGO), иконки и манифесты встраиваются через `go-winres`, затем [NSIS](https://nsis.sourceforge.io/) собирает `build/installer/kordo.nsi`.

```bash
sudo apt install nsis          # нужен makensis 3.08+
build/build-installer.sh 0.3.0 # без аргумента берётся build/version.txt
```

Результат: `bin/*.exe` и `dist/Kordo-Setup-0.3.0.exe` + `.sha256`. Версия попадает в `service.Version`, в ресурсы exe и в установщик.

### Подпись кода

Подписи пока нет, но место под неё готово: если задать переменную `KORDO_SIGN_CMD`, `build-installer.sh` вызовет её с путём к файлу последним аргументом для каждого exe в `bin/`, для деинсталлятора и для самого установщика (через `!finalize`/`!uninstfinalize` NSIS). Команда должна подписать файл на месте. Например:

```bash
# Linux, osslsigncode (обёртка, потому что osslsigncode пишет в новый файл)
cat > sign.sh <<'SH'
#!/bin/sh
osslsigncode sign -pkcs12 cert.pfx -pass "$PFX_PASS" -h sha256 \
  -t http://timestamp.digicert.com -in "$1" -out "$1.signed" && mv "$1.signed" "$1"
SH
KORDO_SIGN_CMD="$PWD/sign.sh" build/build-installer.sh 0.3.0
```

На Windows подойдёт `signtool sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /a`. В CI сертификат нужно положить в секреты репозитория и передать `KORDO_SIGN_CMD` в шаг сборки.

### CI и релизы

GitHub Actions (`.github/workflows/ci.yml`):
- в каждом PR и на `main`: `gofmt`, `go vet` и `go test` на `windows-latest` (часть пакетов и golden-конфиги только для Windows), установщик собирается на `ubuntu-latest` и прикладывается к запуску артефактом (версия `<version.txt>-dev.<номер запуска>`);
- на тег `v*` дополнительно создаётся GitHub Release с `Kordo-Setup-<версия>.exe` и `.sha256`. Теги с суффиксом (`v0.3.0-rc1`) публикуются как pre-release.

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
cmd/kordo-svc  точка входа службы
cmd/kordoctl   CLI
testdata/        golden-конфиги и фикстуры провайдеров
build/           build.ps1, build-installer.sh, installer/kordo.nsi, version.txt, tags.txt
docs/            ADR
```

## Тесты

```bash
go test -tags with_gvisor,with_wireguard,with_utls,with_clash_api ./...
```

Тесты проходят и на Linux, и на Windows; пакеты из `windows/` собираются только под Windows, на Linux их проверяет `GOOS=windows go vet`.

`engine` прогоняет каждый сгенерированный конфиг через строгий парсер и конструктор sing-box: это эквивалент `sing-box check`. Golden-файлы обновляются так: `UPDATE_GOLDEN=1 go test ./core/...`.

Превью интерфейса в браузере с мок-данными: `node build/devserver.js`, затем откройте http://localhost:5178.

Автотест интерфейса на тех же мок-данных (проходит все экраны и основные кнопки, падает на любой ошибке JS): `node build/ui-smoke.js` (нужен `playwright`, в CI ставится сам).

## Ограничения текущей версии

См. [docs/ADR-0001-mvp-decisions.md](docs/ADR-0001-mvp-decisions.md). Кратко:
- подпроцессы отслеживаются опросом снимков процессов раз в 2 с (ETW пока нет), поэтому первое соединение очень короткоживущего потомка может пройти мимо правила;
- точный PID-режим для общих exe требует патча sing-box (этап v1.1);
- иконки в трее пока нет: окно можно закрыть, служба продолжит работать;
- Android и подписи кода пока нет.
